// Package storetest holds test helpers shared by the case store tests: a
// software Protector (so tests do not depend on DPAPI) and the contract suite
// that every casestore.Store implementation must pass.
package storetest

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
)

// Protector is an AES-GCM stand-in for DPAPI. It is for tests only.
type Protector struct{ aead cipher.AEAD }

// NewProtector builds a Protector from a 16, 24 or 32 byte key.
func NewProtector(key []byte) (*Protector, error) {
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	a, err := cipher.NewGCM(b)
	if err != nil {
		return nil, err
	}
	return &Protector{aead: a}, nil
}

// RandomProtector builds a Protector with a fresh random key.
func RandomProtector() *Protector {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(err)
	}
	p, err := NewProtector(key)
	if err != nil {
		panic(err)
	}
	return p
}

// magic is the first byte of every blob: fixed, and never '{', so a blob is not
// mistaken for the JSON header of a v2 case file (the nonce that follows is random
// and could start with '{' one time in 256).
const magic byte = 0x01

func (p *Protector) Protect(plain []byte) ([]byte, error) {
	nonce := make([]byte, 1+p.aead.NonceSize())
	nonce[0] = magic
	if _, err := rand.Read(nonce[1:]); err != nil {
		return nil, err
	}
	return p.aead.Seal(nonce, nonce[1:], plain, nil), nil
}

func (p *Protector) Unprotect(sealed []byte) ([]byte, error) {
	n := 1 + p.aead.NonceSize()
	if len(sealed) < n || sealed[0] != magic {
		return nil, errors.New("sealed blob too short or not from this protector")
	}
	return p.aead.Open(nil, sealed[1:n], sealed[n:], nil)
}
