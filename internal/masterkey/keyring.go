package masterkey

import (
	"sync"

	"api-audit-explorer/internal/sealedfile"
)

// Keyring holds the unlocked 32-byte master key for the running session and
// implements sealedfile.Protector. It holds nothing else. Case-file keys (salt
// per file) are not derived here.
type Keyring struct {
	mu  sync.RWMutex
	key []byte
}

var _ sealedfile.Protector = (*Keyring)(nil)

// Protect seals plain under the master key with the default context label.
func (k *Keyring) Protect(plain []byte) ([]byte, error) { return k.Seal(keyringLabel, plain) }

// Unprotect opens a blob made by Protect. Every ciphertext failure is
// ErrCiphertext; a locked Keyring is ErrLocked.
func (k *Keyring) Unprotect(blob []byte) ([]byte, error) { return k.Open(keyringLabel, blob) }

// Seal is Protect with a caller-chosen context label (bound as AAD), so one
// purpose's blobs do not open as another's.
func (k *Keyring) Seal(label string, plain []byte) ([]byte, error) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	if len(k.key) != KeyLen {
		return nil, ErrLocked
	}
	return SealWith(k.key, label, plain)
}

// Open is Unprotect with a caller-chosen context label.
func (k *Keyring) Open(label string, blob []byte) ([]byte, error) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	if len(k.key) != KeyLen {
		return nil, ErrLocked
	}
	return OpenWith(k.key, label, blob)
}

// WithLabel returns a Protector bound to label that shares this Keyring, so
// Lock on the Keyring locks it too.
func (k *Keyring) WithLabel(label string) sealedfile.Protector {
	return labelled{k: k, label: label}
}

type labelled struct {
	k     *Keyring
	label string
}

func (l labelled) Protect(p []byte) ([]byte, error)   { return l.k.Seal(l.label, p) }
func (l labelled) Unprotect(b []byte) ([]byte, error) { return l.k.Open(l.label, b) }

// Locked reports whether the key has been zeroed.
func (k *Keyring) Locked() bool {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return len(k.key) != KeyLen
}

// Lock zeroes the key. Later calls fail with ErrLocked. It is idempotent.
func (k *Keyring) Lock() {
	k.mu.Lock()
	defer k.mu.Unlock()
	clear(k.key)
	k.key = nil
}
