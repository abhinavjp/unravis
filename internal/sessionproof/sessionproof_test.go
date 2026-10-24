package sessionproof

import (
	"strings"
	"testing"
)

func TestProofBindsTokenAddressAndNonce(t *testing.T) {
	token := "proof-token-0123456789abcdef0123456789"
	nonce, err := NewNonce()
	if err != nil || !ValidNonce(nonce) {
		t.Fatalf("nonce %q, %v", nonce, err)
	}
	proof := Compute(token, "127.0.0.1:4000", nonce)
	if strings.Contains(proof, token) || len(proof) != 64 {
		t.Fatalf("proof %q", proof)
	}
	if !Verify(token, "127.0.0.1:4000", nonce, proof) {
		t.Fatal("valid proof refused")
	}
	other, _ := NewNonce()
	for name, ok := range map[string]bool{
		"other token":   Verify(token+"x", "127.0.0.1:4000", nonce, proof),
		"other address": Verify(token, "127.0.0.1:4001", nonce, proof),
		"other nonce":   Verify(token, "127.0.0.1:4000", other, proof),
		"empty token":   Verify("", "127.0.0.1:4000", nonce, Compute("", "127.0.0.1:4000", nonce)),
		"not hex":       Verify(token, "127.0.0.1:4000", nonce, strings.Repeat("z", 64)),
		"short":         Verify(token, "127.0.0.1:4000", nonce, proof[:62]),
		"upper nonce":   Verify(token, "127.0.0.1:4000", strings.ToUpper(nonce), Compute(token, "127.0.0.1:4000", strings.ToUpper(nonce))),
	} {
		if ok {
			t.Errorf("%s: proof accepted", name)
		}
	}
	if ValidNonce(strings.Repeat("a", 63)) || ValidNonce(strings.Repeat("a", 65)) || ValidNonce("") {
		t.Error("nonce of the wrong length accepted")
	}
}
