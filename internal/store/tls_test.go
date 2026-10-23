package store

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"net"
	"net/url"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"go.mongodb.org/mongo-driver/v2/x/mongo/driver/connstring"
)

func TestTLSConfigVerifiesTrustAndHostnameUnlessInsecureIsExplicit(t *testing.T) {
	certificatePEM, certificate := selfSignedTLSCertificate(t)
	server := startLocalTLSServer(t, certificate)

	systemConfig, err := buildTLSConfig("system", "")
	if err != nil {
		t.Fatal("build system-root TLS config")
	}
	if systemConfig == nil || systemConfig.MinVersion != tls.VersionTLS12 || systemConfig.InsecureSkipVerify {
		t.Fatalf("system TLS config=%#v, want TLS 1.2+ with verification", systemConfig)
	}
	if err := localTLSHandshake(server, "localhost", systemConfig); err == nil {
		t.Fatal("system roots unexpectedly trusted a self-signed certificate")
	}

	customConfig, err := buildTLSConfig("custom", certificatePEM)
	if err != nil {
		t.Fatal("build custom-root TLS config")
	}
	if err := localTLSHandshake(server, "localhost", customConfig); err != nil {
		t.Fatalf("custom CA did not trust certificate for matching hostname: %v", err)
	}
	if err := localTLSHandshake(server, "wrong.local", customConfig); err == nil {
		t.Fatal("custom CA config accepted a hostname mismatch")
	}

	insecureConfig, err := buildTLSConfig("insecure", "")
	if err != nil {
		t.Fatal("build explicit insecure TLS config")
	}
	if insecureConfig == nil || !insecureConfig.InsecureSkipVerify || insecureConfig.MinVersion != tls.VersionTLS12 {
		t.Fatalf("explicit insecure TLS config=%#v, want TLS 1.2+ and skip verification", insecureConfig)
	}
	if err := localTLSHandshake(server, "wrong.local", insecureConfig); err != nil {
		t.Fatalf("explicit insecure TLS did not permit test certificate: %v", err)
	}
}

func TestTLSConfigModesValidateCertificateAndUnresolvedVaultID(t *testing.T) {
	if cfg, err := buildTLSConfig("uri", ""); err != nil || cfg != nil {
		t.Fatalf("URI mode config=%#v err=%v, want nil config", cfg, err)
	}
	if cfg, err := buildTLSConfig("off", ""); err != nil || cfg != nil {
		t.Fatalf("off mode config=%#v err=%v, want nil config", cfg, err)
	}
	for _, tc := range []struct {
		mode, certificate string
	}{
		{"custom", ""},
		{"custom", "not a PEM certificate"},
		{"system", "certificate material is not allowed in this mode"},
		{"insecure", "certificate material is not allowed in this mode"},
		{"off", "certificate material is not allowed in this mode"},
		{"unknown", ""},
	} {
		if _, err := buildTLSConfig(tc.mode, tc.certificate); err == nil {
			t.Errorf("buildTLSConfig(%q, certificate) succeeded", tc.mode)
		}
	}
	certificatePEM, _ := selfSignedTLSCertificate(t)
	_, privateKeyCertificate := selfSignedTLSCertificate(t)
	privateKeyPEM, err := x509.MarshalPKCS8PrivateKey(privateKeyCertificate.PrivateKey)
	if err != nil {
		t.Fatal("marshal test key")
	}
	privateBundle := certificatePEM + string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKeyPEM}))
	for _, invalidBundle := range []string{privateBundle, certificatePEM + "untrusted trailing text"} {
		if _, err := buildTLSConfig("custom", invalidBundle); err == nil {
			t.Fatal("custom CA accepted a private-key block or trailing non-PEM data")
		}
	}
	secondCertificate, _ := selfSignedTLSCertificate(t)
	if _, err := buildTLSConfig("custom", certificatePEM+"\n"+secondCertificate); err != nil {
		t.Fatalf("custom CA rejected a valid multi-certificate bundle: %v", err)
	}
	if _, err := buildTLSConfig("custom", string(make([]byte, maxCACertificateBytes+1))); err == nil {
		t.Fatal("custom CA larger than 256 KiB was accepted")
	}

	_, _, _, err = validateProfile(audit.SourceInput{URI: "mongodb://host", Database: "db", Collection: "audit", SavedConnectionID: "vault-reference"})
	if err == nil {
		t.Fatal("store accepted an unresolved saved connection reference")
	}
}

func TestExplicitTLSModesOverrideURIOptionsAndURIInsecureIsRejected(t *testing.T) {
	uri := "mongodb://host/?tlsInsecure=true&tlsAllowInvalidCertificates=true&tlsCAFile=%2Fprivate%2Fca.pem&ssl=true&retryWrites=true"
	for _, tc := range []struct {
		mode, wantTLS string
	}{
		{"system", "true"},
		{"custom", "true"},
		{"insecure", "true"},
		{"off", "false"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			input := audit.ConnectionInput{URI: uri, TLSMode: tc.mode}
			if tc.mode == "custom" {
				input.CACertificate, _ = selfSignedTLSCertificate(t)
			}
			composed, err := composeMongoURI(input)
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := url.Parse(composed)
			if err != nil {
				t.Fatal(err)
			}
			query := parsed.Query()
			if query.Get("tls") != tc.wantTLS || query.Get("retrywrites") != "true" {
				t.Fatalf("TLS mode %q produced query %v", tc.mode, query)
			}
			for key := range query {
				if key == "tlsinsecure" || key == "tlsallowinvalidcertificates" || key == "tlscafile" || key == "ssl" {
					t.Fatalf("explicit TLS mode retained conflicting URI option %q", key)
				}
			}
			if _, err := connstring.ParseAndValidate(composed); err != nil {
				t.Fatalf("MongoDB driver rejected explicit TLS mode URI: %v", err)
			}
		})
	}
	if _, err := composeMongoURI(audit.ConnectionInput{URI: "mongodb://host/?tlsInsecure=true", TLSMode: "uri"}); err == nil {
		t.Fatal("URI TLS mode accepted disabled certificate verification")
	}
}

func TestURITLSModePreservesVerifiedURIOptions(t *testing.T) {
	uri := "mongodb://host/?tls=true&tlsCAFile=%2Fetc%2Fmongo%2Fca.pem&retryWrites=true"
	composed, err := composeMongoURI(audit.ConnectionInput{URI: uri, TLSMode: "uri"})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(composed)
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if query.Get("tls") != "true" || query.Get("tlscafile") != "/etc/mongo/ca.pem" || query.Get("retrywrites") != "true" {
		t.Fatalf("URI TLS mode did not preserve its verified TLS options: %v", query)
	}
	if _, err := connstring.ParseAndValidate(composed); err != nil {
		t.Fatalf("MongoDB driver rejected URI TLS mode URI: %v", err)
	}
}

func TestConnectionKeySeparatesTLSModeAndCustomTrust(t *testing.T) {
	certificatePEM, _ := selfSignedTLSCertificate(t)
	profiles := []audit.SourceInput{
		{Label: "system", URI: "mongodb://host", TLSMode: "system", Database: "db", Collection: "audit"},
		{Label: "insecure", URI: "mongodb://host", TLSMode: "insecure", Database: "db", Collection: "audit"},
		{Label: "custom", URI: "mongodb://host", TLSMode: "custom", CACertificate: certificatePEM, Database: "db", Collection: "audit"},
		{Label: "custom whitespace", URI: "mongodb://host", TLSMode: "custom", CACertificate: certificatePEM + "\n", Database: "db", Collection: "audit"},
	}
	keys := make(map[string]bool)
	for _, input := range profiles {
		_, _, key, err := validateProfile(input)
		if err != nil {
			t.Fatal(err)
		}
		if keys[key] {
			t.Fatalf("TLS settings shared a connection key: mode=%q", input.TLSMode)
		}
		keys[key] = true
	}
}

func selfSignedTLSCertificate(t *testing.T) (string, tls.Certificate) {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal("generate test key")
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal("create test certificate")
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal("marshal test key")
	}
	certificatePEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})
	certificate, err := tls.X509KeyPair([]byte(certificatePEM), keyPEM)
	if err != nil {
		t.Fatal("load test certificate")
	}
	return certificatePEM, certificate
}

func startLocalTLSServer(t *testing.T, certificate tls.Certificate) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal("listen for local TLS test")
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				server := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12})
				_ = server.Handshake()
				_ = server.Close()
			}()
		}
	}()
	return listener.Addr().String()
}

func localTLSHandshake(address, serverName string, config *tls.Config) error {
	clientConfig := config.Clone()
	clientConfig.ServerName = serverName
	dialer := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 2 * time.Second}, Config: clientConfig}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return err
	}
	return conn.Close()
}
