package store

import (
	"context"
	"net/url"
	"os"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"go.mongodb.org/mongo-driver/v2/x/mongo/driver/connstring"
)

// Set AUDIT_EXPLORER_TEST_MONGO_URI only for the disposable synthetic fixture.
func TestMongoFixtureAnalyze(t *testing.T) {
	uri := os.Getenv("AUDIT_EXPLORER_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("set AUDIT_EXPLORER_TEST_MONGO_URI to use the disposable fixture")
	}
	connection := audit.ConnectionInput{URI: uri, Username: os.Getenv("AUDIT_EXPLORER_TEST_MONGO_USERNAME"), Password: os.Getenv("AUDIT_EXPLORER_TEST_MONGO_PASSWORD"), AuthSource: os.Getenv("AUDIT_EXPLORER_TEST_MONGO_AUTH_SOURCE")}
	parsedURI, err := url.Parse(uri)
	if err != nil {
		t.Fatal("parse disposable fixture URI")
	}
	if parsedURI.User != nil {
		connection.Username = parsedURI.User.Username()
		connection.Password, _ = parsedURI.User.Password()
		parsedURI.User = nil
		connection.URI = parsedURI.String()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s := New()
	defer func() { _ = s.Close(context.Background()) }()
	sourceIDs := make([]string, 0, 2)
	for _, database := range []string{"audit_fixture_one", "audit_fixture_two"} {
		source, err := s.AddSource(ctx, audit.SourceInput{Label: database, URI: connection.URI, Username: connection.Username, Password: connection.Password, AuthSource: connection.AuthSource, Database: database, Collection: "ApiAuditLog"})
		if err != nil {
			t.Fatalf("add disposable fixture source %s: %v", database, err)
		}
		sourceIDs = append(sourceIDs, source.ID)
	}

	currentStart := time.Date(2026, 10, 22, 14, 59, 0, 0, time.UTC)
	currentEnd := time.Date(2026, 10, 22, 15, 14, 0, 0, time.UTC)
	q := audit.Query{
		SourceIDs: sourceIDs,
		Start:     currentStart, End: currentEnd,
		BaselineStart: currentStart.Add(-15 * time.Minute), BaselineEnd: currentStart,
		Kind: "synchronous", Outcome: "all", Metric: "total", BucketSeconds: 60,
	}
	result, err := s.Analyze(ctx, q)
	if err != nil {
		t.Fatalf("analyze disposable fixture: %v", err)
	}
	if len(result.Sources) != 2 {
		t.Fatalf("got %d source results, want 2", len(result.Sources))
	}
	for _, source := range result.Sources {
		if !source.OK {
			t.Errorf("fixture source %s failed: %s", source.SourceID, source.Error)
		}
	}
}

func TestMongoFixtureConnectionDiscovery(t *testing.T) {
	uri := os.Getenv("AUDIT_EXPLORER_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("set AUDIT_EXPLORER_TEST_MONGO_URI to use the disposable fixture")
	}
	input := audit.ConnectionInput{URI: uri, Username: os.Getenv("AUDIT_EXPLORER_TEST_MONGO_USERNAME"), Password: os.Getenv("AUDIT_EXPLORER_TEST_MONGO_PASSWORD"), AuthSource: os.Getenv("AUDIT_EXPLORER_TEST_MONGO_AUTH_SOURCE")}
	parsed, err := url.Parse(uri)
	if err != nil {
		t.Fatal("parse disposable fixture URI")
	}
	if parsed.User != nil {
		input.Username = parsed.User.Username()
		input.Password, _ = parsed.User.Password()
		parsed.User = nil
		input.URI = parsed.String()
	}
	composed, err := composeMongoURI(input)
	if err != nil {
		t.Fatal("validate disposable fixture connection fields")
	}
	if _, err := connstring.ParseAndValidate(composed); err != nil {
		t.Fatal("MongoDB driver rejected the composed disposable fixture URI")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, err := defaultClientFactory(ctx, composed, nil)
	if err != nil {
		t.Fatal("create disposable fixture client")
	}
	defer func() { _ = client.Close(context.Background()) }()
	checker := client.(connectionTestClient)
	if err := checker.Ping(ctx); err != nil {
		t.Fatal("disposable fixture ping failed")
	}
	databases, err := checker.ListDatabases(ctx)
	if err != nil {
		t.Fatal("disposable fixture database discovery failed")
	}
	wanted := map[string]bool{"audit_allowed_one": false, "audit_allowed_two": false}
	for _, name := range databases {
		if _, ok := wanted[name]; ok {
			wanted[name] = true
		}
		if name == "audit_not_allowed" {
			t.Fatal("hidden disposable fixture database was returned")
		}
	}
	for name, found := range wanted {
		if !found {
			t.Fatalf("authorized fixture database %q was not discovered", name)
		}
	}
}
