package store

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"reflect"
	"sort"
	"syscall"

	"api-audit-explorer/internal/audit"
	mongov1 "go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/x/mongo/driver/topology"
	topologyv1 "go.mongodb.org/mongo-driver/x/mongo/driver/topology"
)

const maxDiscoveredDatabases = 1000

type connectionTestClient interface {
	Ping(context.Context) error
	ListDatabases(context.Context) ([]string, error)
}

// ValidateConnectionSettings checks syntax and local TLS material only. It
// does not resolve DNS, open a socket, or authenticate to MongoDB.
func (s *Store) ValidateConnectionSettings(input audit.ConnectionInput) error {
	if _, err := composeMongoURI(input); err != nil {
		return sanitizedValidationError(err)
	}
	if _, err := buildTLSConfig(input.TLSMode, input.CACertificate); err != nil {
		return sanitizedValidationError(err)
	}
	return nil
}

// TestConnection checks a temporary MongoDB connection and returns only the
// authorized database names. It never adds a source or retains the client.
func (s *Store) TestConnection(ctx context.Context, input audit.ConnectionInput) (audit.ConnectionResult, error) {
	if err := s.ValidateConnectionSettings(input); err != nil {
		return audit.ConnectionResult{}, err
	}
	uri, err := composeMongoURI(input)
	if err != nil {
		return audit.ConnectionResult{}, sanitizedValidationError(err)
	}
	if err := acquire(ctx, s.global); err != nil {
		return audit.ConnectionResult{}, err
	}
	defer func() { <-s.global }()

	tlsConfig, err := buildTLSConfig(input.TLSMode, input.CACertificate)
	if err != nil {
		return audit.ConnectionResult{}, err
	}
	client, err := s.newClient(ctx, uri, tlsConfig)
	if err != nil {
		return audit.ConnectionResult{}, connectionTestFailure(ctx, "connection", err)
	}
	defer closeClientWithTimeout(client)

	tester, ok := client.(connectionTestClient)
	if !ok {
		return audit.ConnectionResult{}, &audit.ConnectionError{
			Stage: "connection", Code: "network_failure", Message: "The MongoDB connection could not be tested.",
		}
	}
	if err := tester.Ping(ctx); err != nil {
		return audit.ConnectionResult{}, connectionTestFailure(ctx, "connection", err)
	}
	databases, err := tester.ListDatabases(ctx)
	if err != nil {
		return audit.ConnectionResult{}, connectionTestFailure(ctx, "discovery", err)
	}

	sort.Strings(databases)
	unique := make([]string, 0, min(len(databases), maxDiscoveredDatabases))
	for _, database := range databases {
		if len(unique) > 0 && unique[len(unique)-1] == database {
			continue
		}
		if len(unique) == maxDiscoveredDatabases {
			return audit.ConnectionResult{Databases: unique, Warning: "Database list was capped at 1000 names."}, nil
		}
		unique = append(unique, database)
	}
	return audit.ConnectionResult{Databases: unique}, nil
}

func connectionTestFailure(ctx context.Context, stage string, cause error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var safeConnectionError *audit.ConnectionError
	if errors.As(cause, &safeConnectionError) {
		return safeConnectionError
	}
	return categorizeConnectionError(stage, cause)
}

func sanitizedValidationError(cause error) error {
	code, message := "invalid_settings", "Connection settings are invalid. Check the URI, TLS settings, credentials, and authentication database."
	if errors.Is(cause, audit.ErrLimit) {
		code, message = "certificate_too_large", "The custom CA certificate must be 256 KiB or smaller."
	}
	return &audit.ConnectionError{Stage: "validation", Code: code, Message: message}
}

func mongoClientSetupFailure(cause error) error {
	classified := categorizeConnectionError("connection", cause)
	var connectionError *audit.ConnectionError
	if errors.As(classified, &connectionError) &&
		(connectionError.Code == "dns_failure" || connectionError.Code == "network_timeout") {
		return connectionError
	}
	return &audit.ConnectionError{
		Stage: "validation", Code: "invalid_settings",
		Message: "MongoDB connection settings are invalid. Check the URI and connection options.",
	}
}

func categorizeConnectionError(stage string, cause error) error {
	causes := connectionErrorCauses(cause)
	for _, wrapped := range causes {
		if code, ok := mongoCommandCode(wrapped); ok {
			switch code {
			case 18:
				return &audit.ConnectionError{Stage: "authentication", Code: "credentials_rejected", Message: "MongoDB rejected the credentials. Check the username, password, and authentication database."}
			case 13:
				if stage == "discovery" {
					return &audit.ConnectionError{Stage: "discovery", Code: "permission_denied", Message: "Connected, but this account is not allowed to list databases."}
				}
				return &audit.ConnectionError{Stage: "authentication", Code: "permission_denied", Message: "MongoDB connected, but the account is not authorized for this operation."}
			}
		}
		if isLegacyAuthenticationError(wrapped) {
			return &audit.ConnectionError{Stage: "authentication", Code: "credentials_rejected", Message: "MongoDB rejected the credentials. Check the username, password, and authentication database."}
		}
	}

	for _, wrapped := range causes {
		var unknownAuthority x509.UnknownAuthorityError
		if errors.As(wrapped, &unknownAuthority) {
			return &audit.ConnectionError{Stage: "tls", Code: "certificate_untrusted", Message: "The TLS certificate is not trusted. Check the selected CA certificate or system roots."}
		}
		var hostnameError x509.HostnameError
		if errors.As(wrapped, &hostnameError) {
			return &audit.ConnectionError{Stage: "tls", Code: "hostname_mismatch", Message: "The server name does not match its TLS certificate."}
		}
		var certificateInvalid x509.CertificateInvalidError
		if errors.As(wrapped, &certificateInvalid) && certificateInvalid.Reason == x509.Expired {
			return &audit.ConnectionError{Stage: "tls", Code: "certificate_expired", Message: "The TLS certificate is expired or not yet valid."}
		}
		var recordHeader tls.RecordHeaderError
		if errors.As(wrapped, &recordHeader) {
			return &audit.ConnectionError{Stage: "tls", Code: "handshake_failed", Message: "The TLS handshake failed. Check the server TLS settings."}
		}
		var alert tls.AlertError
		if errors.As(wrapped, &alert) {
			return &audit.ConnectionError{Stage: "tls", Code: "handshake_failed", Message: "The TLS handshake failed. Check the server TLS settings."}
		}
		if isTLSVerificationError(wrapped) {
			return &audit.ConnectionError{Stage: "tls", Code: "tls_failure", Message: "TLS verification failed. Check the server certificate and TLS settings."}
		}
	}

	for _, wrapped := range causes {
		var dnsError *net.DNSError
		if errors.As(wrapped, &dnsError) {
			return &audit.ConnectionError{Stage: "connection", Code: "dns_failure", Message: "The host name could not be resolved. Check the address or VPN connection."}
		}
	}
	for _, wrapped := range causes {
		var networkError net.Error
		if errors.Is(wrapped, context.DeadlineExceeded) || (errors.As(wrapped, &networkError) && networkError.Timeout()) {
			return &audit.ConnectionError{Stage: "connection", Code: "network_timeout", Message: "The connection attempt timed out. Check network access and the server port."}
		}
	}
	for _, wrapped := range causes {
		if errors.Is(wrapped, syscall.ECONNREFUSED) {
			return &audit.ConnectionError{Stage: "connection", Code: "connection_refused", Message: "The server refused the connection. Check the server port and firewall."}
		}
	}
	if stage == "discovery" {
		return &audit.ConnectionError{Stage: "discovery", Code: "database_listing_failed", Message: "Connected, but authorized databases could not be listed."}
	}
	return &audit.ConnectionError{Stage: "connection", Code: "network_failure", Message: "The server could not be reached. Check network access and the server port."}
}

func mongoCommandCode(err error) (int32, bool) {
	if command, ok := mongoCommandError(err); ok {
		return command.Code, true
	}
	var value mongov1.CommandError
	if errors.As(err, &value) {
		return int32(value.Code), true
	}
	var pointer *mongov1.CommandError
	if errors.As(err, &pointer) && pointer != nil {
		return int32(pointer.Code), true
	}
	return 0, false
}

func isLegacyAuthenticationError(err error) bool {
	// mongo-driver v1.10's auth.Error intentionally keeps its wrapped server
	// response private and does not expose Unwrap. Inspect only its concrete
	// package/type identity, never its error text (which can contain details).
	t := reflect.TypeOf(err)
	if t == nil {
		return false
	}
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return t.PkgPath() == "go.mongodb.org/mongo-driver/x/mongo/driver/auth" && t.Name() == "Error"
}

// connectionErrorCauses also inspects each server's LastError in the driver's
// topology snapshot. ServerSelectionError may wrap a timeout while the useful
// DNS/TLS/socket cause is attached to an individual server description.
func connectionErrorCauses(err error) []error {
	causes := []error{err}
	// Follow wrapped driver errors and server-selection snapshots. Legacy
	// mongo-driver wraps authentication errors in topology.ConnectionError,
	// sometimes several levels below the public operation error.
	for i := 0; i < len(causes) && i < 64; i++ {
		cause := causes[i]
		var selection topology.ServerSelectionError
		if errors.As(cause, &selection) {
			if selection.Wrapped != nil {
				causes = append(causes, selection.Wrapped)
			}
			for _, server := range selection.Desc.Servers {
				if server.LastError != nil {
					causes = append(causes, server.LastError)
				}
			}
		}
		var legacySelection topologyv1.ServerSelectionError
		if errors.As(cause, &legacySelection) {
			if legacySelection.Wrapped != nil {
				causes = append(causes, legacySelection.Wrapped)
			}
			for _, server := range legacySelection.Desc.Servers {
				if server.LastError != nil {
					causes = append(causes, server.LastError)
				}
			}
		}
		if unwrapped, ok := cause.(interface{ Unwrap() error }); ok {
			if inner := unwrapped.Unwrap(); inner != nil {
				causes = append(causes, inner)
			}
		}
		if multiple, ok := cause.(interface{ Unwrap() []error }); ok {
			causes = append(causes, multiple.Unwrap()...)
		}
	}
	return causes
}

func mongoCommandError(err error) (mongo.CommandError, bool) {
	var value mongo.CommandError
	if errors.As(err, &value) {
		return value, true
	}
	var pointer *mongo.CommandError
	if errors.As(err, &pointer) && pointer != nil {
		return *pointer, true
	}
	return mongo.CommandError{}, false
}

func isTLSVerificationError(err error) bool {
	var verificationError *tls.CertificateVerificationError
	return errors.As(err, &verificationError)
}
