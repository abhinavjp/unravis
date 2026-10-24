package store

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"api-audit-explorer/internal/audit"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/x/mongo/driver/connstring"
)

type fakeClient struct {
	probes      atomic.Int32
	closes      atomic.Int32
	probeErr    error
	pingErr     error
	listErr     error
	databases   []string
	aggregateFn func(context.Context, string, string, mongo.Pipeline) (mongoCursor, error)
}

func (f *fakeClient) Probe(context.Context, string, string) (string, string, error) {
	f.probes.Add(1)
	return "7.0.1", "", f.probeErr
}
func (f *fakeClient) Ping(context.Context) error { return f.pingErr }
func (f *fakeClient) ListDatabases(context.Context) ([]string, error) {
	return append([]string(nil), f.databases...), f.listErr
}
func (f *fakeClient) Aggregate(ctx context.Context, db, collection string, pipeline mongo.Pipeline) (mongoCursor, error) {
	if f.aggregateFn != nil {
		return f.aggregateFn(ctx, db, collection, pipeline)
	}
	return &fakeCursor{}, nil
}
func (f *fakeClient) Close(context.Context) error { f.closes.Add(1); return nil }

func input(uri, db, collection string) audit.SourceInput {
	return audit.SourceInput{Label: "  Production  ", URI: uri, Database: db, Collection: collection}
}

func authenticatedInput(uri, username, password, db, collection string) audit.SourceInput {
	in := input(uri, db, collection)
	in.Username, in.Password = username, password
	return in
}

func TestValidateProfileRejectsUnsafeOrMalformedInput(t *testing.T) {
	bad := []audit.SourceInput{
		input("https://mongodb.example", "db", "audit"),
		input("mongodb://host", "", "audit"),
		input("mongodb://host", "db", "audit/sub"),
		input("mongodb://host/?tlsInsecure=true", "db", "audit"),
		input("mongodb://host/?tlsAllowInvalidCertificates=1", "db", "audit"),
		input("mongodb://host/?tlsAllowInvalidHostnames=true", "db", "audit"),
		input("mongodb://host/?bad=%zz", "db", "audit"),
		input("mongodb://user:secret@host", "db", "audit"),
	}
	for _, in := range bad {
		if _, _, _, err := validateProfile(in); err == nil {
			t.Errorf("validateProfile(%q, %q) accepted unsafe input", in.URI, in.Collection)
		}
	}
}

func TestAddSourceReusesOnlySameAuthenticatedConnection(t *testing.T) {
	var created atomic.Int32
	clients := []*fakeClient{}
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) {
		created.Add(1)
		c := &fakeClient{}
		clients = append(clients, c)
		return c, nil
	})
	first, err := s.AddSource(context.Background(), authenticatedInput("HOST", "user", "secret", "Payroll", "Audit"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.AddSource(context.Background(), authenticatedInput("mongodb://host", "user", "secret", "Other", "Audit"))
	if err != nil {
		t.Fatal(err)
	}
	third, err := s.AddSource(context.Background(), authenticatedInput("mongodb://host", "other", "secret", "Third", "Audit"))
	if err != nil {
		t.Fatal(err)
	}
	if created.Load() != 2 {
		t.Fatalf("created %d clients, want 2", created.Load())
	}
	if clients[0].probes.Load() != 2 || clients[1].probes.Load() != 1 {
		t.Fatal("expected both databases on same-auth connection to be probed")
	}
	if _, err := s.AddSource(context.Background(), authenticatedInput("mongodb://host", "other", "secret", "Third", "Audit")); !errors.Is(err, errDuplicate) {
		t.Fatalf("duplicate source error = %v", err)
	}
	if err := s.DeleteSource(context.Background(), first.ID); err != nil {
		t.Fatal(err)
	}
	if clients[0].closes.Load() != 0 {
		t.Fatal("shared connection closed while a profile still references it")
	}
	if err := s.DeleteSource(context.Background(), second.ID); err != nil {
		t.Fatal(err)
	}
	if clients[0].closes.Load() != 1 {
		t.Fatal("last profile deletion did not close shared connection")
	}
	if err := s.DeleteSource(context.Background(), third.ID); err != nil {
		t.Fatal(err)
	}
}

func TestComposeMongoURIAcceptsBareHostsAndPreservesMongoSchemes(t *testing.T) {
	for _, tc := range []struct {
		name, input, wantScheme, wantHost string
	}{
		{"bare hostname", "uk-01-mongo.brainpayroll.co.uk", "mongodb", "uk-01-mongo.brainpayroll.co.uk"},
		{"hostname and port", "mongo.internal:27018", "mongodb", "mongo.internal:27018"},
		{"bracketed IPv6 and port", "[::1]:27019", "mongodb", "[::1]:27019"},
		{"mongodb URI", "mongodb://mongo.internal:27018/appdb?tls=true", "mongodb", "mongo.internal:27018"},
		{"mongodb SRV URI", "mongodb+srv://cluster.example.net/appdb?tls=true", "mongodb+srv", "cluster.example.net"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			uri, err := composeMongoURI(audit.ConnectionInput{URI: tc.input, Username: "reader", Password: "secret"})
			if err != nil {
				t.Fatalf("compose URI: %v", err)
			}
			if tc.wantScheme == "mongodb+srv" {
				parsed, err := url.Parse(uri)
				if err != nil {
					t.Fatal(err)
				}
				if parsed.Scheme != tc.wantScheme || parsed.Host != tc.wantHost {
					t.Fatalf("preserved SRV scheme/host=%q/%q, want %q/%q", parsed.Scheme, parsed.Host, tc.wantScheme, tc.wantHost)
				}
				return
			}
			parsed, err := connstring.ParseAndValidate(uri)
			if err != nil {
				t.Fatalf("MongoDB driver rejected composed URI: %v", err)
			}
			if parsed.Scheme != tc.wantScheme || len(parsed.Hosts) != 1 {
				t.Fatalf("parsed scheme/hosts=%q/%v, want %q and one host", parsed.Scheme, parsed.Hosts, tc.wantScheme)
			}
			if tc.wantHost == "uk-01-mongo.brainpayroll.co.uk" && parsed.Hosts[0] != tc.wantHost {
				t.Fatalf("bare-host driver target=%q, want %q (driver supplies port 27017)", parsed.Hosts[0], tc.wantHost)
			}
		})
	}
}

func TestComposeMongoURIRejectsWebSchemesAndUnshemedPaths(t *testing.T) {
	for _, input := range []string{
		"http://mongo.internal",
		"https://mongo.internal",
		"mongo.internal/appdb",
		"mongo.internal\\appdb",
	} {
		t.Run(input, func(t *testing.T) {
			if _, err := composeMongoURI(audit.ConnectionInput{URI: input}); err == nil {
				t.Fatalf("accepted ambiguous or non-MongoDB target %q", input)
			}
		})
	}
}

func TestAddSourceEnforcesLimitAndCancellation(t *testing.T) {
	var created atomic.Int32
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) {
		created.Add(1)
		return &fakeClient{}, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.AddSource(ctx, input("mongodb://host", "db", "audit")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled add error = %v", err)
	}
	if created.Load() != 0 {
		t.Fatal("canceled add called client factory")
	}
	for i := 0; i < maxSources; i++ {
		in := input("mongodb://host", "db", "audit"+string(rune('a'+i)))
		if _, err := s.AddSource(context.Background(), in); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}
	if _, err := s.AddSource(context.Background(), input("mongodb://host", "db", "audit-z")); !errors.Is(err, errSourceLimit) {
		t.Fatalf("limit error = %v", err)
	}
}

func TestSourceJSONNeverContainsURIOrCredentials(t *testing.T) {
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) { return &fakeClient{}, nil })
	source, err := s.AddSource(context.Background(), authenticatedInput("mongodb://host/db?authSource=admin", "user", "top-secret", "db", "audit"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "top-secret") || strings.Contains(string(b), "mongodb") || strings.Contains(string(b), "uri") {
		t.Fatalf("public Source leaked connection details: %s", b)
	}
}

func TestComposeMongoURIEscapesCredentialsAndOverridesAuthSource(t *testing.T) {
	password := "p@ss:/?%雪 #"
	uri, err := composeMongoURI(audit.ConnectionInput{
		URI: "mongodb://HOST/mydb?authSource=uriAuth&retryWrites=true", Username: "reader@ops", Password: password, AuthSource: "explicitAuth",
	})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(uri)
	if err != nil {
		t.Fatal(err)
	}
	username := parsed.User.Username()
	decodedPassword, hasPassword := parsed.User.Password()
	if username != "reader@ops" || !hasPassword || decodedPassword != password {
		t.Fatalf("decoded URI credentials differ: username=%q password=%q hasPassword=%t", username, decodedPassword, hasPassword)
	}
	query := parsed.Query()
	if query.Get("authsource") != "explicitAuth" || query.Get("retrywrites") != "true" || parsed.Path != "/mydb" {
		t.Fatalf("composed URI lost or failed to override settings: %s", parsed.Redacted())
	}
}

func TestComposeMongoURIPreservesURIAuthSourceAndDefaultPathSemantics(t *testing.T) {
	for _, tc := range []struct {
		name, input, wantAuthSource, wantPath string
	}{
		{"uri auth source", "mongodb://host/appdb?authSource=authdb", "authdb", "/appdb"},
		{"path without auth source", "mongodb://host/appdb", "", "/appdb"},
		{"default admin source", "mongodb://host", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			uri, err := composeMongoURI(audit.ConnectionInput{URI: tc.input, Username: "reader", Password: "secret"})
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := url.Parse(uri)
			if err != nil {
				t.Fatal(err)
			}
			if parsed.Query().Get("authsource") != tc.wantAuthSource || parsed.Path != tc.wantPath {
				t.Fatalf("URI query/path = %q/%q, want %q/%q", parsed.Query().Get("authsource"), parsed.Path, tc.wantAuthSource, tc.wantPath)
			}
			if _, err := connstring.ParseAndValidate(uri); err != nil {
				t.Fatalf("MongoDB driver rejected composed URI: %v", err)
			}
		})
	}
}

func TestTestConnectionSanitizesWrongCredentialsAndAlwaysClosesClient(t *testing.T) {
	client := &fakeClient{pingErr: errors.New("mongodb://reader:secret@host/appdb: authentication failed")}
	var gotURI string
	s := newStore(func(_ context.Context, uri string, _ *tls.Config) (clientHandle, error) {
		gotURI = uri
		return client, nil
	})
	input := audit.ConnectionInput{URI: "mongodb://host/appdb", Username: "reader", Password: "secret"}
	_, err := s.TestConnection(context.Background(), input)
	if !errors.Is(err, audit.ErrInvalidInput) {
		t.Fatalf("TestConnection error=%v, want invalid input", err)
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "reader") || strings.Contains(err.Error(), "mongodb://") {
		t.Fatalf("connection error leaked credentials/URI: %v", err)
	}
	if !strings.Contains(gotURI, "reader:secret@") {
		t.Fatalf("connection credentials were not composed into URI: %s", gotURI)
	}
	if client.closes.Load() != 1 {
		t.Fatalf("temporary client close count=%d, want 1", client.closes.Load())
	}
}

func TestTestConnectionRejectsEmbeddedURIUserInfoBeforeConnecting(t *testing.T) {
	var factoryCalled atomic.Bool
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) {
		factoryCalled.Store(true)
		return &fakeClient{}, nil
	})
	_, err := s.TestConnection(context.Background(), audit.ConnectionInput{URI: "mongodb://reader:top-secret@host/appdb"})
	var connectionError *audit.ConnectionError
	if !errors.Is(err, audit.ErrInvalidInput) || !errors.As(err, &connectionError) || connectionError.Stage != "validation" || connectionError.Code != "invalid_settings" {
		t.Fatalf("embedded URI credential error=%v", err)
	}
	if strings.Contains(err.Error(), "top-secret") || strings.Contains(err.Error(), "reader") || factoryCalled.Load() {
		t.Fatalf("embedded credentials leaked or factory called: err=%v called=%t", err, factoryCalled.Load())
	}
}

func TestTestConnectionSortsDeduplicatesAndCapsDatabaseNames(t *testing.T) {
	names := make([]string, 0, 1002)
	for i := 1001; i >= 0; i-- {
		names = append(names, fmt.Sprintf("db-%04d", i))
	}
	names = append(names, "db-0000")
	client := &fakeClient{databases: names}
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) { return client, nil })
	result, err := s.TestConnection(context.Background(), audit.ConnectionInput{URI: "mongodb://host"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Databases) != 1000 || result.Warning == "" || result.Databases[0] != "db-0000" || result.Databases[len(result.Databases)-1] != "db-0999" {
		t.Fatalf("capped result=%d warning=%q first/last=%q/%q", len(result.Databases), result.Warning, result.Databases[0], result.Databases[len(result.Databases)-1])
	}
	if client.closes.Load() != 1 {
		t.Fatalf("temporary client close count=%d, want 1", client.closes.Load())
	}
	profiles, err := s.Sources(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 0 {
		t.Fatalf("connection test persisted %d source profiles, want none", len(profiles))
	}
}

func TestDeleteSourceCancellationLeavesProfileAndConnectionAvailable(t *testing.T) {
	var client *fakeClient
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) {
		client = &fakeClient{}
		return client, nil
	})
	source, err := s.AddSource(context.Background(), input("mongodb://host", "db", "audit"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.DeleteSource(ctx, source.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled deletion = %v", err)
	}
	items, err := s.Sources(context.Background())
	if err != nil || len(items) != 1 {
		t.Fatalf("canceled deletion removed profile: sources=%d err=%v", len(items), err)
	}
	if client.closes.Load() != 0 {
		t.Fatal("canceled deletion closed client")
	}
}

type fakeCursor struct {
	docs    []any
	idx     int
	current any
	err     error
}

func (c *fakeCursor) Next(context.Context) bool {
	if c.idx >= len(c.docs) {
		return false
	}
	c.current = c.docs[c.idx]
	c.idx++
	return true
}
func (c *fakeCursor) Decode(dst any) error {
	switch target := dst.(type) {
	case *bucketDocument:
		v, ok := c.current.(bucketDocument)
		if !ok {
			return errors.New("unexpected fake cursor document")
		}
		*target = v
	case *requestDocument:
		v, ok := c.current.(requestDocument)
		if !ok {
			return errors.New("unexpected fake cursor document")
		}
		*target = v
	case *bson.Raw:
		v, ok := c.current.(bson.Raw)
		if !ok {
			return errors.New("unexpected fake cursor document")
		}
		*target = v
	default:
		return errors.New("unsupported fake cursor target")
	}
	return nil
}
func (c *fakeCursor) Err() error                  { return c.err }
func (c *fakeCursor) Close(context.Context) error { return nil }
func TestSourceConnectionGroupingUsesOpaquePublicIdentity(t *testing.T) {
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) { return &fakeClient{}, nil })
	first := authenticatedInput("mongodb://host-a", "reader", "private-password", "payroll", "audit")
	first.ConnectionLabel = "UK production"
	a, err := s.AddSource(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	second := first
	second.Database = "employee"
	b, err := s.AddSource(context.Background(), second)
	if err != nil {
		t.Fatal(err)
	}
	third := first
	third.URI = "mongodb://host-b"
	third.ConnectionLabel = "Australia production"
	c, err := s.AddSource(context.Background(), third)
	if err != nil {
		t.Fatal(err)
	}
	if a.ConnectionID == "" || a.ConnectionID != b.ConnectionID || a.ConnectionID == c.ConnectionID {
		t.Fatal("connection grouping incorrect")
	}
	if a.ConnectionLabel != "UK production" || b.ConnectionLabel != "UK production" || c.ConnectionLabel != "Australia production" {
		t.Fatal("connection labels incorrect")
	}
	values, _ := s.Sources(context.Background())
	encoded, _ := json.Marshal(values)
	if strings.Contains(string(encoded), "private-password") || strings.Contains(string(encoded), "mongodb://") {
		t.Fatal("private connection identity exposed")
	}
}
func TestConnectionLabelDefaultsToHostAndIsBounded(t *testing.T) {
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) { return &fakeClient{}, nil })
	in := authenticatedInput("mongodb://example.test:27017/?appName=private-option", "reader", "private-password", "db", "audit")
	src, err := s.AddSource(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if src.ConnectionLabel != "example.test:27017" {
		t.Fatal("default connection label must be host only")
	}
	in.Database = "other"
	in.ConnectionLabel = strings.Repeat("x", 121)
	if _, err = s.AddSource(context.Background(), in); !errors.Is(err, audit.ErrInvalidInput) {
		t.Fatal("oversized public label accepted")
	}
}

func TestSourceSelectionKeySurvivesRestartWithoutCrossingDatasets(t *testing.T) {
	makeStore := func() *Store {
		return newStore(func(context.Context, string, *tls.Config) (clientHandle, error) { return &fakeClient{}, nil })
	}
	first := authenticatedInput("mongodb://HOST-A/?appName=one", "reader", "private-password", "Payroll", "ApiAuditLog")
	a, err := makeStore().AddSource(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	changed := first
	changed.Password = "changed-private-password"
	changed.URI = "mongodb://host-a/?appName=two"
	changed.ConnectionLabel = "Renamed connection"
	b, err := makeStore().AddSource(context.Background(), changed)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.SelectionKey) != 64 || a.SelectionKey != b.SelectionKey || a.ID == b.ID {
		t.Fatal("logical source identity must survive session IDs, credentials and label changes")
	}
	changed.URI = "mongodb://host-b"
	c, err := makeStore().AddSource(context.Background(), changed)
	if err != nil {
		t.Fatal(err)
	}
	if a.SelectionKey == c.SelectionKey {
		t.Fatal("different servers must not share selection identity")
	}
	changed = first
	changed.Collection = "ArchiveAuditLog"
	d, err := makeStore().AddSource(context.Background(), changed)
	if err != nil {
		t.Fatal(err)
	}
	if a.SelectionKey == d.SelectionKey {
		t.Fatal("different collections must not share selection identity")
	}
}

func TestSourceSelectionKeyCanonicalizesDefaultPortAndSeedOrder(t *testing.T) {
	first := audit.SourceInput{URI: "mongodb://HOST-A", Database: "Payroll", Collection: "ApiAuditLog"}
	key := sourceSelectionKey(first)
	equivalent := first
	equivalent.URI = "mongodb://host-a:27017"
	if key != sourceSelectionKey(equivalent) {
		t.Fatal("default port spelling changed source identity")
	}
	different := first
	different.URI = "mongodb://host-a:27018"
	if key == sourceSelectionKey(different) {
		t.Fatal("different port must identify a different source")
	}
	first.URI = "mongodb://host-b:27017,host-a"
	equivalent.URI = "mongodb://HOST-A:27017,host-b"
	if sourceSelectionKey(first) != sourceSelectionKey(equivalent) {
		t.Fatal("seed ordering changed source identity")
	}
	first.URI = "mongodb://[::1]"
	equivalent.URI = "mongodb://[::1]:27017"
	if sourceSelectionKey(first) != sourceSelectionKey(equivalent) {
		t.Fatal("IPv6 default port changed source identity")
	}
}

func TestEquivalentDeploymentSpellingsCannotCreateAmbiguousSavedSelections(t *testing.T) {
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) { return &fakeClient{}, nil })
	first := authenticatedInput("mongodb://host", "reader", "private-password", "Payroll", "ApiAuditLog")
	if _, err := s.AddSource(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	first.URI = "mongodb://HOST:27017"
	if _, err := s.AddSource(context.Background(), first); !errors.Is(err, errDuplicate) {
		t.Fatal("equivalent deployment must not create duplicate stable selection keys")
	}
}

func TestPerSourceCapacityIsOne(t *testing.T) {
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) { return &fakeClient{}, nil })
	source, err := s.AddSource(context.Background(), authenticatedInput("HOST", "user", "secret", "Payroll", "Audit"))
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	got := cap(s.perSource[source.ID])
	s.mu.Unlock()
	if got != 1 {
		t.Fatalf("cap(perSource) = %d, want %d", got, audit.PerSourceConcurrency)
	}
}
