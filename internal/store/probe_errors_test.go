package store

import (
	"context"
	"crypto/tls"
	"errors"
	"reflect"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/mongo"

	"api-audit-explorer/internal/audit"
)

func TestMissingCollectionTellsAWrongNameFromAnEmptyCollection(t *testing.T) {
	names := []string{"system.views", "SystemApiAuditLog", "Users", "audit_archive", "AuditLogs"}
	if err := missingCollection("db", "SystemApiAuditLog", names, nil); err != nil {
		t.Fatalf("an existing collection (even an empty one) is not missing: %v", err)
	}
	if err := missingCollection("db", "audit", names, errors.New("not authorized to list collections")); err != nil {
		t.Fatalf("when collections cannot be listed the source must not be refused: %v", err)
	}
	err := missingCollection("db", "audit", names, nil)
	var missing *collectionMissingError
	if !errors.As(err, &missing) {
		t.Fatalf("expected collectionMissingError, got %v", err)
	}
	want := []string{"AuditLogs", "SystemApiAuditLog", "audit_archive"}
	if !reflect.DeepEqual(missing.hints, want) {
		t.Fatalf("hints = %v, want %v (audit-like names only, no system.* views)", missing.hints, want)
	}
}

func TestAuditHintsAreBounded(t *testing.T) {
	var names []string
	for i := 0; i < 30; i++ {
		names = append(names, "audit"+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	if got := auditHints(names); len(got) != audit.MaxCollectionHints {
		t.Fatalf("hints = %d, want %d", len(got), audit.MaxCollectionHints)
	}
}

func TestAddSourceNamesTheRealProbeFailure(t *testing.T) {
	cases := []struct {
		name  string
		probe error
		code  string
		hints []string
	}{
		{"wrong collection", &collectionMissingError{database: "Payroll", collection: "audit", hints: []string{"SystemApiAuditLog"}}, "collection_missing", []string{"SystemApiAuditLog"}},
		{"old server", errUnsupportedMongoVersion, "unsupported_version", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) {
				return &fakeClient{probeErr: tc.probe}, nil
			})
			_, err := s.AddSource(context.Background(), authenticatedInput("mongodb://host", "user", "secret", "Payroll", "audit"))
			var ce *audit.ConnectionError
			if !errors.As(err, &ce) {
				t.Fatalf("want a ConnectionError, got %v", err)
			}
			if ce.Code != tc.code || ce.Stage != "source" || !reflect.DeepEqual(ce.Collections, tc.hints) {
				t.Fatalf("got %+v", ce)
			}
			if !errors.Is(err, audit.ErrInvalidInput) {
				t.Fatal("a failed source check stays invalid input for older callers")
			}
			if strings.Contains(ce.Message, "secret") {
				t.Fatal("message holds the password")
			}
		})
	}
}

func TestAddSourceUnknownProbeFailureStaysGeneric(t *testing.T) {
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) {
		return &fakeClient{probeErr: errors.New("something odd")}, nil
	})
	_, err := s.AddSource(context.Background(), authenticatedInput("mongodb://host", "user", "secret", "Payroll", "audit"))
	var ce *audit.ConnectionError
	if errors.As(err, &ce) || !errors.Is(err, audit.ErrInvalidInput) {
		t.Fatalf("got %v", err)
	}
}

func TestAddSourceDuplicateIsADuplicate(t *testing.T) {
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) { return &fakeClient{}, nil })
	in := authenticatedInput("mongodb://host", "user", "secret", "Payroll", "SystemApiAuditLog")
	if _, err := s.AddSource(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	_, err := s.AddSource(context.Background(), in)
	if !errors.Is(err, audit.ErrDuplicate) || !errors.Is(err, audit.ErrInvalidInput) {
		t.Fatalf("duplicate error = %v", err)
	}
}

func TestAdaptiveProbeKeepsTheCollectionErrorWithoutSwitchingToLegacy(t *testing.T) {
	missing := &collectionMissingError{database: "db", collection: "audit"}
	modern := &fakeClient{probeErr: missing}
	h := &adaptiveMongoHandle{modern: modern}
	_, _, err := h.Probe(context.Background(), "db", "audit")
	if !errors.Is(err, missing) {
		t.Fatalf("err = %v", err)
	}
	if h.isLegacy() {
		t.Fatal("a wrong collection name must not switch the handle to the legacy driver")
	}
}

func TestDeniedCollectionOnlyForNotAuthorised(t *testing.T) {
	if deniedCollection(errors.New("network down"), nil, nil) != nil {
		t.Fatal("only a not-authorised answer (code 13) is a denied collection")
	}
	denied := mongo.CommandError{Code: 13, Message: "not authorized on Payroll to execute command find"}
	got := deniedCollection(denied, []string{"Users", "SystemApiAuditLog"}, nil)
	var d *collectionDeniedError
	if !errors.As(got, &d) || !reflect.DeepEqual(d.hints, []string{"SystemApiAuditLog"}) {
		t.Fatalf("got %v", got)
	}
	got = deniedCollection(denied, nil, errors.New("cannot list"))
	if !errors.As(got, &d) || len(d.hints) != 0 {
		t.Fatalf("an unlistable database still reports the refusal, without hints: %v", got)
	}
	failure := sourceAddFailure("Payroll", "ApiAuditLog", got)
	var ce *audit.ConnectionError
	if !errors.As(failure, &ce) || ce.Code != "permission_denied" || ce.Stage != "source" {
		t.Fatalf("got %v", failure)
	}
}
