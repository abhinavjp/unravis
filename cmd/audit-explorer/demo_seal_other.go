//go:build !windows

package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"

	"api-audit-explorer/internal/sealedfile"
)

// demoSeal gives the screenshot demo's case store a sealer off Windows, where
// there is no DPAPI. It is used only by openDemoCases (--demo with
// UNRAVIS_DEMO_CASES=1 and an empty AUDIT_CONFIG_DIR), so a Linux run of the
// demo can reach the case screens for audits and captures. The key is random,
// is fixed and public, so this is not protection: it only lets the store's
// sealed-file code run the same way it does on Windows, and a second start on
// the same folder opens what the first one wrote. The demo's cases are made-up
// data, and openDemoCases refuses a folder that holds real cases or a master key.
// Live mode never calls this and still fails closed off Windows.
func demoSeal() (sealedfile.Protector, error) {
	key := sha256.Sum256([]byte("unravis demo cases: not a secret"))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return ephemeralSealer{aead: aead}, nil
}

type ephemeralSealer struct{ aead cipher.AEAD }

func (s ephemeralSealer) Protect(plain []byte) ([]byte, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return s.aead.Seal(nonce, nonce, plain, nil), nil
}

func (s ephemeralSealer) Unprotect(blob []byte) ([]byte, error) {
	n := s.aead.NonceSize()
	if len(blob) < n {
		return nil, errors.New("sealed blob is too short")
	}
	return s.aead.Open(nil, blob[:n], blob[n:], nil)
}
