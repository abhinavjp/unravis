package store

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"
	"testing"

	"api-audit-explorer/internal/audit"
	mongov1 "go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/x/mongo/driver/description"
	"go.mongodb.org/mongo-driver/v2/x/mongo/driver/topology"
)

func TestValidateConnectionSettingsIsOfflineAndReturnsSanitizedErrors(t *testing.T) {
	factoryCalled := false
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) {
		factoryCalled = true
		return &fakeClient{}, nil
	})
	valid := audit.ConnectionInput{URI: "uk-01-mongo.brainpayroll.co.uk:27017", TLSMode: "system"}
	if err := s.ValidateConnectionSettings(valid); err != nil {
		t.Fatalf("offline validation rejected syntactically valid settings: %v", err)
	}
	if factoryCalled {
		t.Fatal("offline settings validation attempted a MongoDB connection")
	}

	invalid := audit.ConnectionInput{URI: "mongodb://user:secret-password@host/db", Username: "different-user"}
	err := s.ValidateConnectionSettings(invalid)
	var connectionError *audit.ConnectionError
	if !errors.As(err, &connectionError) || !errors.Is(err, audit.ErrInvalidInput) {
		t.Fatalf("invalid connection settings error=%v, want safe typed invalid-input error", err)
	}
	if connectionError.Stage != "validation" || connectionError.Code != "invalid_settings" {
		t.Fatalf("validation error stage/code=%q/%q", connectionError.Stage, connectionError.Code)
	}
	if strings.Contains(err.Error(), "secret-password") || strings.Contains(err.Error(), "different-user") || strings.Contains(err.Error(), "mongodb://") {
		t.Fatalf("validation error reflected connection input: %v", err)
	}
}

func TestConnectionFailureClassificationUsesSafeCategories(t *testing.T) {
	certificate := &x509.Certificate{}
	wrappedRefusal := topology.ServerSelectionError{Wrapped: fmt.Errorf("driver detail: %w", &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED})}
	tests := []struct {
		name, stage, wantStage, wantCode string
		cause                            error
		privateText                      string
	}{
		{"DNS", "connection", "connection", "dns_failure", &net.DNSError{Err: "no such host", Name: "private.internal.example"}, "private.internal.example"},
		{"server selection timeout", "connection", "connection", "network_timeout", topology.ServerSelectionError{Wrapped: context.DeadlineExceeded}, "context deadline"},
		{"network timeout", "connection", "connection", "network_timeout", topology.ServerSelectionError{Wrapped: context.DeadlineExceeded, Desc: description.Topology{Servers: []description.Server{{LastError: &net.OpError{Op: "dial", Net: "tcp", Err: testTimeoutError{}}}}}}, "socket timeout detail"},
		{"refused through topology wrapper", "connection", "connection", "connection_refused", wrappedRefusal, "driver detail"},
		{"untrusted certificate", "connection", "tls", "certificate_untrusted", x509.UnknownAuthorityError{Cert: certificate}, "unknown authority"},
		{"TLS error in topology snapshot", "connection", "tls", "certificate_untrusted", topology.ServerSelectionError{Wrapped: fmt.Errorf("selection wait: %w", context.DeadlineExceeded), Desc: description.Topology{Servers: []description.Server{{LastError: x509.UnknownAuthorityError{Cert: certificate}}}}}, "selection wait"},
		{"TLS handshake failure", "connection", "tls", "handshake_failed", tls.RecordHeaderError{Msg: "secret TLS server detail"}, "secret TLS server detail"},
		{"hostname mismatch", "connection", "tls", "hostname_mismatch", x509.HostnameError{Certificate: certificate, Host: "private.internal.example"}, "private.internal.example"},
		{"expired certificate", "connection", "tls", "certificate_expired", fmt.Errorf("secret private cert metadata: %w", x509.CertificateInvalidError{Cert: certificate, Reason: x509.Expired}), "secret private cert metadata"},
		{"authentication code", "connection", "authentication", "credentials_rejected", mongo.CommandError{Code: 18, Message: "password=secret-password"}, "secret-password"},
		{"legacy authentication code", "connection", "authentication", "credentials_rejected", mongov1.CommandError{Code: 18, Message: "password=secret-password"}, "secret-password"},
		{"database permission code", "discovery", "discovery", "permission_denied", mongo.CommandError{Code: 13, Message: "private database denied"}, "private database"},
		{"legacy database permission code", "discovery", "discovery", "permission_denied", mongov1.CommandError{Code: 13, Message: "private database denied"}, "private database"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := categorizeConnectionError(tc.stage, fmt.Errorf("outer driver wrapper: %w", tc.cause))
			var connectionError *audit.ConnectionError
			if !errors.As(err, &connectionError) || !errors.Is(err, audit.ErrInvalidInput) {
				t.Fatalf("classification error=%v, want typed safe input error", err)
			}
			if connectionError.Stage != tc.wantStage || connectionError.Code != tc.wantCode {
				t.Fatalf("stage/code=%q/%q, want %q/%q", connectionError.Stage, connectionError.Code, tc.wantStage, tc.wantCode)
			}
			if strings.Contains(err.Error(), tc.privateText) {
				t.Fatalf("safe error reflected raw driver detail %q: %v", tc.privateText, err)
			}
		})
	}
}

type testTimeoutError struct{}

func (testTimeoutError) Error() string   { return "socket timeout detail" }
func (testTimeoutError) Timeout() bool   { return true }
func (testTimeoutError) Temporary() bool { return true }

func TestTestConnectionClassifiesMongoAuthenticationAndDiscoveryFailures(t *testing.T) {
	for _, tc := range []struct {
		name, stage, wantCode string
		pingErr, listErr      error
	}{
		{name: "authentication", stage: "authentication", wantCode: "credentials_rejected", pingErr: mongo.CommandError{Code: 18, Message: "private server message"}},
		{name: "database discovery permission", stage: "discovery", wantCode: "permission_denied", listErr: mongo.CommandError{Code: 13, Message: "private database detail"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeClient{pingErr: tc.pingErr, listErr: tc.listErr}
			s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) { return client, nil })
			_, err := s.TestConnection(context.Background(), audit.ConnectionInput{URI: "mongodb://localhost"})
			var connectionError *audit.ConnectionError
			if !errors.As(err, &connectionError) {
				t.Fatalf("TestConnection error=%v, want typed connection error", err)
			}
			if connectionError.Stage != tc.stage || connectionError.Code != tc.wantCode {
				t.Fatalf("stage/code=%q/%q, want %q/%q", connectionError.Stage, connectionError.Code, tc.stage, tc.wantCode)
			}
			if strings.Contains(err.Error(), "private") {
				t.Fatalf("TestConnection exposed raw MongoDB message: %v", err)
			}
		})
	}
}

func TestTestConnectionClassifiesLocalMongoOptionErrorsAsValidation(t *testing.T) {
	s := New()
	_, err := s.TestConnection(context.Background(), audit.ConnectionInput{
		URI: "mongodb://127.0.0.1:27017/?connectTimeoutMS=not-a-number",
	})
	var connectionError *audit.ConnectionError
	if !errors.As(err, &connectionError) {
		t.Fatalf("TestConnection error=%v, want typed settings error", err)
	}
	if connectionError.Stage != "validation" || connectionError.Code != "invalid_settings" {
		t.Fatalf("stage/code=%q/%q, want validation/invalid_settings", connectionError.Stage, connectionError.Code)
	}
	if strings.Contains(err.Error(), "not-a-number") || strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("connection settings error exposed the raw URI: %v", err)
	}
}

func TestMongoClientSetupFailurePreservesWrappedDNSAndTimeoutCategories(t *testing.T) {
	tests := []struct {
		name, wantCode, privateText string
		cause                       error
	}{
		{
			name:        "wrapped DNS lookup",
			wantCode:    "dns_failure",
			privateText: "secret-srv-host.example",
			cause:       fmt.Errorf("SRV lookup details: %w", &net.DNSError{Err: "no such host", Name: "secret-srv-host.example"}),
		},
		{
			name:        "wrapped timeout",
			wantCode:    "network_timeout",
			privateText: "secret timeout detail",
			cause:       fmt.Errorf("secret timeout detail: %w", testTimeoutError{}),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := mongoClientSetupFailure(tc.cause)
			var connectionError *audit.ConnectionError
			if !errors.As(err, &connectionError) {
				t.Fatalf("setup error=%v, want typed safe error", err)
			}
			if connectionError.Stage != "connection" || connectionError.Code != tc.wantCode {
				t.Fatalf("stage/code=%q/%q, want connection/%q", connectionError.Stage, connectionError.Code, tc.wantCode)
			}
			if strings.Contains(err.Error(), tc.privateText) {
				t.Fatalf("safe setup error reflected raw DNS/network detail: %v", err)
			}
		})
	}
}
