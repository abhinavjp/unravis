// Package sessionproof lets a client check that a loopback server holds the
// session token before the client sends it. The client sends a fresh random
// nonce; the server answers HMAC-SHA256(token, label, address, nonce), where
// address is the server's own listening address. A process that does not hold
// the token cannot answer, and a process that relays the nonce to the real
// dashboard on another port gets an answer bound to the wrong address.
package sessionproof

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
)

// Path is the unauthenticated proof endpoint. It takes the nonce as ?n=.
const Path = "/api/session-proof"

// nonceBytes is the nonce length; it travels as lowercase hex.
const nonceBytes = 32

const label = "unravis-session-proof-v1"

// NewNonce returns a fresh random nonce in lowercase hex.
func NewNonce() (string, error) {
	b := make([]byte, nonceBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// ValidNonce reports whether n has the shape NewNonce produces.
func ValidNonce(n string) bool {
	if len(n) != 2*nonceBytes {
		return false
	}
	for i := 0; i < len(n); i++ {
		c := n[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// Compute is the proof for token, the server's listening address (host:port as
// net.Addr.String prints it) and nonce, in lowercase hex.
func Compute(token, address, nonce string) string {
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write([]byte(label))
	mac.Write([]byte{0})
	mac.Write([]byte(address))
	mac.Write([]byte{0})
	mac.Write([]byte(nonce))
	return hex.EncodeToString(mac.Sum(nil))
}

// Verify reports, in constant time, whether proof is Compute(token, address, nonce).
func Verify(token, address, nonce, proof string) bool {
	got, err := hex.DecodeString(proof)
	if err != nil || len(got) != sha256.Size || !ValidNonce(nonce) || token == "" {
		return false
	}
	want, _ := hex.DecodeString(Compute(token, address, nonce))
	return hmac.Equal(got, want)
}
