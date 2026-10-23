package store

import (
	"context"
	"crypto/tls"
	"errors"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"go.mongodb.org/mongo-driver/event"
	bsonv2 "go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type legacySelectionClient struct {
	probeErr error
	probes   int
	closed   bool
	version  string
}

func (c *legacySelectionClient) Probe(context.Context, string, string) (string, string, error) {
	c.probes++
	if c.probeErr != nil {
		return "", "", c.probeErr
	}
	return c.version, "legacy index warning", nil
}
func (*legacySelectionClient) Aggregate(context.Context, string, string, mongo.Pipeline) (mongoCursor, error) {
	return nil, errors.New("unused")
}
func (*legacySelectionClient) Ping(context.Context) error { return nil }
func (*legacySelectionClient) ListDatabases(context.Context) ([]string, error) {
	return []string{"allowed"}, nil
}
func (c *legacySelectionClient) Close(context.Context) error { c.closed = true; return nil }

func TestAdaptiveMongoUsesLegacyOnlyAfterVerifiedMongo34WireVersion(t *testing.T) {
	modernError := errors.New("modern protocol operation failed")
	t.Run("verified 3.4 selects legacy and retries operation", func(t *testing.T) {
		modern := &fakeClient{probeErr: modernError}
		legacy := &legacySelectionClient{version: "3.4.24"}
		var probeURI string
		var probeTLS *tls.Config
		var legacyURI string
		var legacyTLS *tls.Config
		handle := newAdaptiveMongoHandle("mongodb://reader:secret@db.example/?authSource=admin", &tls.Config{MinVersion: tls.VersionTLS12}, modern)
		handle.probeWire = func(_ context.Context, uri string, cfg *tls.Config) (int32, error) {
			probeURI, probeTLS = uri, cfg
			return 5, nil
		}
		handle.newLegacyClient = func(_ context.Context, uri string, cfg *tls.Config) (clientHandle, error) {
			legacyURI, legacyTLS = uri, cfg
			return legacy, nil
		}
		version, warning, err := handle.Probe(context.Background(), "db", "audit")
		if err != nil || version != "3.4.24" || warning != "legacy index warning" {
			t.Fatalf("Probe() = %q, %q, %v", version, warning, err)
		}
		if !usesLegacyMongo(handle) || legacy.probes != 1 || modern.closes.Load() != 1 {
			t.Fatal("verified MongoDB 3.4 did not switch once and close the modern client")
		}
		if !strings.Contains(probeURI, "reader") || !strings.Contains(probeURI, "authSource=admin") {
			t.Fatal("capability probe did not receive the exact source URI needed to preserve transport options")
		}
		if probeTLS == nil || legacyTLS == nil || probeTLS.MinVersion != tls.VersionTLS12 || legacyTLS.MinVersion != tls.VersionTLS12 {
			t.Fatal("TLS configuration was not retained for capability probe and legacy client")
		}
		if !strings.Contains(legacyURI, "reader") {
			t.Fatal("legacy client did not retain its authenticated URI")
		}
	})

	for _, tc := range []struct {
		name string
		wire int32
		err  error
	}{
		{name: "modern wire version", wire: 6},
		{name: "capability probe failed", err: errors.New("probe failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			modern := &fakeClient{probeErr: modernError}
			handle := newAdaptiveMongoHandle("mongodb://db.example", nil, modern)
			handle.probeWire = func(context.Context, string, *tls.Config) (int32, error) { return tc.wire, tc.err }
			legacyCreated := false
			handle.newLegacyClient = func(context.Context, string, *tls.Config) (clientHandle, error) {
				legacyCreated = true
				return &legacySelectionClient{version: "3.4.24"}, nil
			}
			_, _, err := handle.Probe(context.Background(), "db", "audit")
			if !errors.Is(err, modernError) {
				t.Fatalf("Probe error=%v, want original modern failure", err)
			}
			if legacyCreated || usesLegacyMongo(handle) {
				t.Fatal("legacy driver selected without verified maxWireVersion 5")
			}
		})
	}

	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "authentication rejection", err: mongo.CommandError{Code: 18, Message: "private auth detail"}},
		{name: "TLS handshake failure", err: tls.RecordHeaderError{Msg: "private TLS detail"}},
	} {
		t.Run("no capability fallback on "+tc.name, func(t *testing.T) {
			modern := &fakeClient{probeErr: tc.err}
			handle := newAdaptiveMongoHandle("mongodb://db.example", nil, modern)
			probeCalled := false
			handle.probeWire = func(context.Context, string, *tls.Config) (int32, error) {
				probeCalled = true
				return 5, nil
			}
			handle.newLegacyClient = func(context.Context, string, *tls.Config) (clientHandle, error) {
				t.Fatal("legacy constructor must not run after auth/TLS errors")
				return nil, nil
			}
			_, _, err := handle.Probe(context.Background(), "db", "audit")
			if err == nil || probeCalled || usesLegacyMongo(handle) {
				t.Fatalf("auth/TLS error was not preserved without fallback; probe-called=%v; legacy=%v", probeCalled, usesLegacyMongo(handle))
			}
		})
	}
}

func TestWithoutMongoCredentialsRetainsTransportOptions(t *testing.T) {
	uri, err := withoutMongoCredentials("mongodb://reader:secret@db.example/?authSource=admin&tls=true&authMechanism=SCRAM-SHA-1&authMechanismProperties=CANONICALIZE_HOST_NAME%3Atrue")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(uri, "reader") || strings.Contains(uri, "secret") || strings.Contains(strings.ToLower(uri), "authsource") || strings.Contains(strings.ToLower(uri), "authmechanism") || !strings.Contains(uri, "tls=true") {
		t.Fatalf("probe URI did not strip all auth options while retaining transport: %q", uri)
	}
}

func TestRemainingLegacyMaxTimeUsesDeadlineAndRejectsExpiredContext(t *testing.T) {
	if got, err := remainingLegacyMaxTime(context.Background()); err != nil || got != queryTimeout {
		t.Fatalf("unbounded context maxTime=%v err=%v; want default query timeout", got, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	got, err := remainingLegacyMaxTime(ctx)
	if err != nil || got > 2*time.Second || got <= 0 {
		t.Fatalf("bounded context maxTime=%v err=%v", got, err)
	}
	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	if _, err := remainingLegacyMaxTime(expired); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired context error=%v; want deadline exceeded", err)
	}
}

func TestMongo34FixtureUsesLegacyDriverForAuthDiscoveryAndProbe(t *testing.T) {
	uri := os.Getenv("AUDIT_MONGO34_TEST_URI")
	if uri == "" {
		t.Skip("set AUDIT_MONGO34_TEST_URI to run against the disposable MongoDB 3.4 fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := defaultClientFactory(ctx, uri, nil)
	if err != nil {
		t.Fatalf("construct MongoDB test client: %v", err)
	}
	defer closeClientWithTimeout(client)
	tester, ok := client.(connectionTestClient)
	if !ok {
		t.Fatal("adaptive client does not expose test-connection methods")
	}
	if err := tester.Ping(ctx); err != nil {
		t.Fatalf("Ping against disposable MongoDB 3.4 fixture: %v", err)
	}
	if !usesLegacyMongo(client) {
		t.Fatal("fixture maxWireVersion 5 did not select the legacy adapter")
	}
	names, err := tester.ListDatabases(ctx)
	if err != nil {
		t.Fatalf("list fixture databases: %v", err)
	}
	if !containsDatabaseName(names, "audit_fixture_one") || !containsDatabaseName(names, "audit_fixture_two") {
		t.Fatalf("authorized fixture databases missing from legacy discovery: %v", names)
	}
	version, _, err := client.Probe(ctx, "audit_fixture_one", "ApiAuditLog")
	if err != nil || version != "3.4.24" {
		t.Fatalf("legacy source probe = version %q, error %v", version, err)
	}
}

func TestMongo34FixtureWrongPasswordIsSanitizedAsAuthenticationFailure(t *testing.T) {
	uri := os.Getenv("AUDIT_MONGO34_TEST_URI")
	if uri == "" {
		t.Skip("set AUDIT_MONGO34_TEST_URI to run against the disposable MongoDB 3.4 fixture")
	}
	parsed, err := url.Parse(uri)
	if err != nil {
		t.Fatal("fixture URI is invalid")
	}
	username := parsed.User.Username()
	parsed.User = url.UserPassword(username, "definitely-wrong-fixture-password")
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	client, err := defaultClientFactory(ctx, parsed.String(), nil)
	if err != nil {
		t.Fatal("could not initialize connection test")
	}
	defer closeClientWithTimeout(client)
	tester := client.(connectionTestClient)
	err = tester.Ping(ctx)
	if err == nil {
		t.Fatal("wrong fixture password unexpectedly connected")
	}
	classified := connectionTestFailure(ctx, "connection", err)
	var safe *audit.ConnectionError
	if !errors.As(classified, &safe) {
		t.Fatal("wrong fixture password was not classified as a safe connection error")
	}
	if safe.Stage != "authentication" || safe.Code != "credentials_rejected" {
		t.Fatalf("wrong fixture password was not classified as an authentication rejection (stage=%q code=%q)", safe.Stage, safe.Code)
	}
	if strings.Contains(classified.Error(), username) || strings.Contains(classified.Error(), "definitely-wrong") || strings.Contains(classified.Error(), "legacy_fixture") {
		t.Fatal("sanitized authentication failure reflected fixture credentials")
	}
}

func TestMongo34AggregateSendsServerMaxTimeMS(t *testing.T) {
	uri := os.Getenv("AUDIT_MONGO34_TEST_URI")
	if uri == "" {
		t.Skip("set AUDIT_MONGO34_TEST_URI to run against the disposable MongoDB 3.4 fixture")
	}
	var observed atomic.Int32
	monitor := &event.CommandMonitor{Started: func(_ context.Context, started *event.CommandStartedEvent) {
		if started.CommandName != "aggregate" {
			return
		}
		if value, ok := started.Command.Lookup("maxTimeMS").Int64OK(); ok {
			observed.Store(int32(value))
		} else if value, ok := started.Command.Lookup("maxTimeMS").Int32OK(); ok {
			observed.Store(value)
		}
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	client, err := newLegacyMongoHandleWithMonitor(ctx, uri, nil, monitor)
	if err != nil {
		t.Fatal("could not initialize fixture test client")
	}
	defer func() { _ = client.Close(context.Background()) }()
	cursor, err := client.Aggregate(ctx, "audit_fixture_one", "ApiAuditLog", mongo.Pipeline{
		bsonv2.D{{Key: "$match", Value: bsonv2.D{}}},
		bsonv2.D{{Key: "$limit", Value: int32(1)}},
	})
	if err != nil {
		t.Fatal("legacy aggregate failed")
	}
	if cursor.Next(ctx) {
		var raw bsonv2.Raw
		if err := cursor.Decode(&raw); err != nil {
			t.Fatal("legacy cursor did not decode through the v2 BSON boundary")
		}
		if _, ok := raw.Lookup("_id").ObjectIDOK(); !ok {
			t.Fatal("legacy cursor returned a document without the expected BSON ObjectID")
		}
	}
	if err := cursor.Err(); err != nil {
		t.Fatal("legacy cursor iteration failed")
	}
	_ = cursor.Close(ctx)
	if got := observed.Load(); got < 1 || got > 8000 {
		t.Fatalf("MongoDB 3.4 aggregate maxTimeMS=%d; want 1..8000", got)
	}
}

func containsDatabaseName(names []string, expected string) bool {
	for _, name := range names {
		if name == expected {
			return true
		}
	}
	return false
}
