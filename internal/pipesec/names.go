package pipesec

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// Kind is the role of a pipe. Per docs/desktop/PLAN.md section 1: "ui" is the shell's pipe, "ag" is the CLI and MCP pipe.
type Kind string

// The pipe roles.
const (
	KindUI Kind = "ui"
	KindAG Kind = "ag"
)

// PipePrefix is the namespace every pipe name starts with.
const PipePrefix = `\\.\pipe\`

// NewNamePair returns the ui and ag names of one owner. Both carry the same 128-bit nonce, so a locator that names two different nonces
// is known to be forged or mixed up (internal/locator checks this).
func NewNamePair() (ui, ag string, err error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", "", fmt.Errorf("pipesec: random name: %w", err)
	}
	n := hex.EncodeToString(b[:])
	return PipePrefix + "unravis." + string(KindUI) + "." + n, PipePrefix + "unravis." + string(KindAG) + "." + n, nil
}

// NewName returns a fresh pipe name, `\\.\pipe\unravis.<kind>.<32 lower-hex>`, with 128 random bits.
func NewName(kind Kind) (string, error) {
	if kind != KindUI && kind != KindAG {
		return "", fmt.Errorf("pipesec: unknown pipe kind %q", string(kind))
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("pipesec: random name: %w", err)
	}
	return PipePrefix + "unravis." + string(kind) + "." + hex.EncodeToString(b[:]), nil
}
