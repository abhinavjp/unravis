package update

import (
	"crypto/ed25519"
	_ "embed"
	"encoding/hex"
	"fmt"
	"strings"
)

// trustedKeysText holds the public keys this program trusts, one per line: "stable <64 hex>" or "rc <64 hex>".
// Blank lines and lines starting with # are ignored. Keep two keys per role (current and next) so a key can be
// rotated without stranding installed copies. The file is filled in by the owner after running
// `sigtool keygen` (see docs/auto-updater/KEYS.md). With no keys, nothing verifies and nothing is ever installed: fail closed.
//
//go:embed trusted_keys.txt
var trustedKeysText string

// TrustedKeys parses the embedded key list.
func TrustedKeys() ([]Key, error) { return ParseKeys(trustedKeysText) }

// ParseKeys parses the text form of a key list.
func ParseKeys(text string) ([]Key, error) {
	var keys []Key
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 2 || (f[0] != ChannelStable && f[0] != ChannelRC) {
			return nil, fmt.Errorf("trusted keys line %d: want \"stable|rc <hex>\"", i+1)
		}
		raw, err := hex.DecodeString(f[1])
		if err != nil || len(raw) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("trusted keys line %d: bad public key", i+1)
		}
		keys = append(keys, Key{Role: f[0], Pub: raw})
	}
	return keys, nil
}
