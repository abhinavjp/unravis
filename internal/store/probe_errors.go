package store

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"api-audit-explorer/internal/audit"
)

// errUnsupportedMongoVersion is a server whose version this app cannot read.
var errUnsupportedMongoVersion = errors.New("unsupported MongoDB server version")

// collectionMissingError says the database has no collection of that name. Probe finds it when the read of the collection returns nothing
// and the database does not list it. A collection that exists but is empty is not this error. hints are the collections of the
// database whose name looks like an audit log, so a person can pick the right one.
type collectionMissingError struct {
	database, collection string
	hints                []string
}

func (e *collectionMissingError) Error() string {
	return fmt.Sprintf("collection %q does not exist in database %q", e.collection, e.database)
}

// collectionDeniedError is a read of the collection the server refused (MongoDB code 13). A role limited to one collection of the
// database lands here when the name asked for is another one. hints are the collections the account may read whose name looks like an
// audit log (known only on a server that can list them).
type collectionDeniedError struct {
	cause error
	hints []string
}

func (e *collectionDeniedError) Error() string { return "not authorised to read the collection" }
func (e *collectionDeniedError) Unwrap() error { return e.cause }

// deniedCollection wraps a failed read as collectionDeniedError when the server answered "not authorised" (code 13), else returns nil.
func deniedCollection(readErr error, names []string, listErr error) error {
	if !mongoCommandCodeIs(readErr, 13) {
		return nil
	}
	denied := &collectionDeniedError{cause: readErr}
	if listErr == nil {
		denied.hints = auditHints(names)
	}
	return denied
}

// isSourceFact is a probe failure that says something about the database (wrong or refused collection), not about the server
// generation, so there is nothing to gain by trying the legacy driver.
func isSourceFact(err error) bool {
	var missing *collectionMissingError
	var denied *collectionDeniedError
	return errors.As(err, &missing) || errors.As(err, &denied)
}

// auditHints keeps the names that look like an audit log (the text "audit" in any case), in name order, at most audit.MaxCollectionHints.
func auditHints(names []string) []string {
	out := make([]string, 0, audit.MaxCollectionHints)
	for _, name := range names {
		if strings.Contains(strings.ToLower(name), "audit") && !strings.HasPrefix(name, "system.") {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	if len(out) > audit.MaxCollectionHints {
		out = out[:audit.MaxCollectionHints]
	}
	return out
}

// sourceAddFailure words a failed probe of a new source in a fixed, safe category (never the address, user name or password). Anything
// it cannot name stays the generic invalid-input answer.
func sourceAddFailure(database, collection string, cause error) error {
	var missing *collectionMissingError
	var denied *collectionDeniedError
	switch {
	case errors.As(cause, &missing):
		return &audit.ConnectionError{
			Stage: "source", Code: "collection_missing", Collections: missing.hints,
			Message: fmt.Sprintf("The database %s has no collection called %s.", database, collection),
		}
	case errors.As(cause, &denied):
		return &audit.ConnectionError{
			Stage: "source", Code: "permission_denied", Collections: denied.hints,
			Message: fmt.Sprintf("The account is not allowed to read the collection %s in the database %s.", collection, database),
		}
	case errors.Is(cause, errUnsupportedMongoVersion):
		return &audit.ConnectionError{Stage: "source", Code: "unsupported_version", Message: "This MongoDB server version is not supported. Unravis reads MongoDB 3.4 and 4.4 or newer."}
	}
	var classified *audit.ConnectionError
	if errors.As(categorizeConnectionError("source", cause), &classified) {
		switch classified.Code {
		case "credentials_rejected":
			return classified
		case "permission_denied":
			return &audit.ConnectionError{Stage: "source", Code: "permission_denied", Message: fmt.Sprintf("The account is not allowed to read the collection %s in the database %s.", collection, database)}
		case "dns_failure", "network_timeout", "connection_refused", "certificate_untrusted", "hostname_mismatch", "certificate_expired", "handshake_failed", "tls_failure":
			return classified
		}
	}
	return fmt.Errorf("%w: MongoDB source validation failed; check the connection, permissions, and server version", audit.ErrInvalidInput)
}

// missingCollection decides, after a read of the collection found no documents, whether the collection is missing. names is the
// database's collection list. When the list could not be read (listErr, usually no permission to list) the answer is "not missing", so
// an empty but real collection is never refused.
func missingCollection(database, collection string, names []string, listErr error) error {
	if listErr != nil {
		return nil
	}
	for _, name := range names {
		if name == collection {
			return nil
		}
	}
	return &collectionMissingError{database: database, collection: collection, hints: auditHints(names)}
}

func mongoCommandCodeIs(err error, want int32) bool {
	code, ok := mongoCommandCode(err)
	return ok && code == want
}
