package blackbox

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"os"
	"strings"
)

// KeyHeader is the request header that carries the read key. The key is never
// accepted in a URL, where it would end up in logs and history.
const KeyHeader = "X-Blackbox-Key"

const keyPrefix = "ubk-"

var keyEnc = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewReadKey makes a read key: "ubk-" and 32 base32 characters in groups of
// four, from 20 random bytes. It is shown once at install; only its SHA-256 hash
// is kept on the recorder's PC.
func NewReadKey() (string, error) {
	b := make([]byte, 20)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	raw := keyEnc.EncodeToString(b)
	var parts []string
	for i := 0; i < len(raw); i += 4 {
		parts = append(parts, raw[i:i+4])
	}
	return keyPrefix + strings.ToLower(strings.Join(parts, "-")), nil
}

// NormalizeKey makes typed keys comparable: case, spaces and dashes do not
// matter. A key without the ubk prefix is not a read key.
func NormalizeKey(key string) (string, bool) {
	k := strings.ToUpper(strings.TrimSpace(key))
	k = strings.NewReplacer(" ", "", "-", "", "\t", "").Replace(k)
	if !strings.HasPrefix(k, "UBK") || len(k) != 3+32 {
		return "", false
	}
	return k, true
}

// HashKey is the stored form of a key (hex SHA-256 of the normalized key).
func HashKey(key string) (string, bool) {
	k, ok := NormalizeKey(key)
	if !ok {
		return "", false
	}
	sum := sha256.Sum256([]byte(k))
	return hex.EncodeToString(sum[:]), true
}

// KeyCheck checks request keys against a stored hash.
type KeyCheck struct{ hash [sha256.Size]byte }

// NewKeyCheck reads a stored hex hash.
func NewKeyCheck(hexHash string) (*KeyCheck, error) {
	b, err := hex.DecodeString(strings.TrimSpace(hexHash))
	if err != nil || len(b) != sha256.Size {
		return nil, errors.New("blackbox: the read key file is not a valid hash")
	}
	k := &KeyCheck{}
	copy(k.hash[:], b)
	return k, nil
}

// Check says whether key is the read key. The comparison takes the same time
// whatever the key.
func (k *KeyCheck) Check(key string) bool {
	h, ok := HashKey(key)
	if !ok {
		// Still do one comparison so a malformed key is not faster to refuse.
		subtle.ConstantTimeCompare(k.hash[:], make([]byte, sha256.Size))
		return false
	}
	got, _ := hex.DecodeString(h)
	return subtle.ConstantTimeCompare(k.hash[:], got) == 1
}

// SaveKeyHash stores the hash of a key at path (not in the recordings folder).
func SaveKeyHash(path, key string) error {
	h, ok := HashKey(key)
	if !ok {
		return errors.New("blackbox: not a read key")
	}
	return writeFileAtomic(path, []byte(h+"\n"), 0o600)
}

// LoadKeyCheck reads the stored hash at path.
func LoadKeyCheck(path string) (*KeyCheck, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return NewKeyCheck(string(b))
}
