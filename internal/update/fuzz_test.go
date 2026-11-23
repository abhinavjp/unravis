package update

import (
	"crypto/ed25519"
	"encoding/json"
	"reflect"
	"testing"
)

// BL-450: Go fuzz targets for the three parsers that read a downloaded file. A plain `go test` replays the seeds and anything under
// testdata/fuzz as ordinary tests; the nightly workflow runs the real fuzzing (`.github/workflows/nightly-fuzz.yml`).
//
// What must hold for any input: no panic, and a result that is accepted is one the parser itself would accept again.

// fuzzTrust is a fixed key pair so a run is repeatable. The seed is test-only and signs nothing outside these tests.
func fuzzTrust() (Trust, ed25519.PrivateKey) {
	priv := ed25519.NewKeyFromSeed([]byte("unravis-fuzz-seed-0123456789abcd"))
	pub := priv.Public().(ed25519.PublicKey)
	return Trust{Keys: []Key{{Role: ChannelRC, Pub: pub}}, Channel: ChannelRC, Want: ChannelRC}, priv
}

// manifestSeeds are the starting inputs: two good manifests and the shapes the strict reader exists to refuse.
func manifestSeeds() [][]byte {
	var seeds [][]byte
	for _, channel := range []string{ChannelRC, ChannelStable} {
		b, _ := json.Marshal(goodManifest("0.1.0-rc.48", channel))
		seeds = append(seeds, b)
	}
	return append(seeds, []byte(`{}`), []byte(`{"schema":1,"schema":2}`), []byte(`{"Schema":1,"schema":1}`), []byte(`{"schema":1} trailing`), []byte(``), []byte("\x00\xff{"))
}

func FuzzParseManifest(f *testing.F) {
	for _, seed := range manifestSeeds() {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		m, err := ParseManifest(data)
		if err != nil {
			return
		}
		if m == nil {
			t.Fatal("ParseManifest returned no manifest and no error")
		}
		// The strict reader accepts only one spelling of each key; what it accepted must have no duplicate key.
		if err := rejectDuplicateKeys(data); err != nil {
			t.Fatalf("accepted a manifest with a duplicate key: %v", err)
		}
		// Round trip: what was accepted, written out again, is accepted again and reads the same.
		again, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("an accepted manifest cannot be written out: %v", err)
		}
		m2, err := ParseManifest(again)
		if err != nil {
			t.Fatalf("a manifest written out again is refused: %v", err)
		}
		if reflect.DeepEqual(m, m2) == false {
			t.Fatalf("a manifest changed on a round trip: %+v then %+v", m, m2)
		}
	})
}

// FuzzVerify signs the fuzzed bytes with the trusted key, so the fuzzer reaches the parse and field checks and is not
// stopped at the signature every time. It also feeds the raw bytes as a signature file.
func FuzzVerify(f *testing.F) {
	trust, priv := fuzzTrust()
	// Seed signature files of the right shape and key id, signed over other bytes, so the fuzzer starts at ed25519.Verify and not at the format check.
	other := []byte(FormatSignature(trust.Keys[0].Pub, ed25519.Sign(priv, []byte("some other bytes"))))
	for _, seed := range manifestSeeds() {
		f.Add(seed, other)
		f.Add(seed, []byte("unravis-sig-v1 x y"))
	}
	f.Fuzz(func(t *testing.T, data []byte, sigFile []byte) {
		// Raw signature file: it must never be accepted for bytes it does not sign.
		if m, err := Verify(data, sigFile, trust, now); err == nil {
			t.Fatalf("accepted a manifest with a signature file it cannot have produced: %+v", m)
		}
		signed := []byte(FormatSignature(trust.Keys[0].Pub, ed25519.Sign(priv, data)))
		m, err := Verify(data, signed, trust, now)
		if err != nil {
			return
		}
		if m.Channel != trust.Keys[0].Role {
			t.Fatalf("a %s key signed a %s manifest and it was accepted", trust.Keys[0].Role, m.Channel)
		}
		if err := m.Validate(now); err != nil {
			t.Fatalf("Verify accepted a manifest that fails Validate: %v", err)
		}
		if _, err := ParseManifest(data); err != nil {
			t.Fatalf("Verify accepted bytes that ParseManifest refuses: %v", err)
		}
	})
}

func FuzzVerifyControl(f *testing.F) {
	trust, priv := fuzzTrust()
	if b, err := json.Marshal(goodControl(ChannelRC)); err == nil {
		f.Add(b)
	}
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"schema":1,"schema":1}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		signed := []byte(FormatSignature(trust.Keys[0].Pub, ed25519.Sign(priv, data)))
		c, err := VerifyControl(data, signed, trust, now)
		if err != nil {
			return
		}
		if c == nil {
			t.Fatal("VerifyControl returned no control file and no error")
		}
		if c.Channel != trust.Keys[0].Role {
			t.Fatalf("a %s key signed a %s control file and it was accepted", trust.Keys[0].Role, c.Channel)
		}
	})
}
