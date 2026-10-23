package store

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"strings"

	"api-audit-explorer/internal/audit"
)

const maxCACertificateBytes = 256 << 10

func normalizeTLSMode(mode string) (string, error) {
	if mode == "" {
		return "uri", nil
	}
	switch mode {
	case "uri", "system", "custom", "insecure", "off":
		return mode, nil
	default:
		return "", fmt.Errorf("%w: unsupported TLS mode", audit.ErrInvalidInput)
	}
}

func buildTLSConfig(mode, caCertificate string) (*tls.Config, error) {
	mode, err := normalizeTLSMode(mode)
	if err != nil {
		return nil, err
	}
	if len(caCertificate) > maxCACertificateBytes {
		return nil, fmt.Errorf("%w: CA certificate exceeds the 256 KiB limit", audit.ErrLimit)
	}
	if mode != "custom" && caCertificate != "" {
		return nil, fmt.Errorf("%w: a CA certificate is only valid in custom TLS mode", audit.ErrInvalidInput)
	}
	switch mode {
	case "uri", "off":
		return nil, nil
	case "system":
		return &tls.Config{MinVersion: tls.VersionTLS12}, nil
	case "custom":
		if caCertificate == "" {
			return nil, fmt.Errorf("%w: custom TLS mode requires a CA certificate", audit.ErrInvalidInput)
		}
		roots, err := x509.SystemCertPool()
		if err != nil || roots == nil {
			return nil, fmt.Errorf("%w: system certificate roots are unavailable", audit.ErrInvalidInput)
		}
		if err := appendCertificateBundle(roots, []byte(caCertificate)); err != nil {
			return nil, fmt.Errorf("%w: CA certificate PEM must contain only valid CERTIFICATE blocks", audit.ErrInvalidInput)
		}
		return &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}, nil
	case "insecure":
		// InsecureSkipVerify deliberately disables certificate and hostname checks
		// only for the explicit insecure mode.
		return &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true}, nil
	default:
		return nil, fmt.Errorf("%w: unsupported TLS mode", audit.ErrInvalidInput)
	}
}

func appendCertificateBundle(roots *x509.CertPool, input []byte) error {
	remaining := input
	certificates := 0
	for len(bytes.TrimSpace(remaining)) > 0 {
		trimmed := bytes.TrimSpace(remaining)
		if !bytes.HasPrefix(trimmed, []byte("-----BEGIN ")) {
			return fmt.Errorf("non-PEM content")
		}
		block, rest := pem.Decode(trimmed)
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 {
			return fmt.Errorf("unsupported PEM block")
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return err
		}
		roots.AddCert(certificate)
		certificates++
		remaining = rest
	}
	if certificates == 0 {
		return fmt.Errorf("empty certificate bundle")
	}
	return nil
}

func tlsConnectionKeyPart(mode, caCertificate string) (string, error) {
	mode, err := normalizeTLSMode(mode)
	if err != nil {
		return "", err
	}
	if len(caCertificate) > maxCACertificateBytes {
		return "", fmt.Errorf("%w: CA certificate exceeds the 256 KiB limit", audit.ErrLimit)
	}
	if mode != "custom" && caCertificate != "" {
		return "", fmt.Errorf("%w: a CA certificate is only valid in custom TLS mode", audit.ErrInvalidInput)
	}
	sum := sha256.Sum256([]byte(caCertificate))
	return mode + ":" + hex.EncodeToString(sum[:]), nil
}

func removeURITLSOptions(query map[string][]string) {
	for name := range query {
		lower := strings.ToLower(name)
		if lower == "tls" || lower == "ssl" || strings.HasPrefix(lower, "tls") || strings.HasPrefix(lower, "ssl") {
			delete(query, name)
		}
	}
}
