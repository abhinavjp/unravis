package privacy

import (
	"strings"
	"testing"
)

// Vectors made with an independent Python implementation of the steps in the Rand comment. The worker must give the same.
func TestRandVectors(t *testing.T) {
	cases := []struct {
		seed string
		want []uint64
	}{
		{"000102030405060708090a0b0c0d0e0f", []uint64{0x1193145395979718, 0x0d8f0a49f04d99eb, 0x2dd0d9ec73d2b019, 0x5749941e660e1e2e}},
		{"00000000000000000000000000000000", []uint64{0x3c6ef35871de8e5f, 0x7af6fe195757c550, 0x6ab123f7ea410725, 0xfa6da9b971861fb8}},
		{"ffffffffffffffffffffffffffffffff", []uint64{0xffffffc0007fffbf, 0xffffffc0007fefc0, 0xe0003000005ff000, 0xc000603ffff00fff}},
	}
	for _, c := range cases {
		r, err := NewRand(c.seed)
		if err != nil {
			t.Fatal(err)
		}
		for i, w := range c.want {
			if g := r.Uint64(); g != w {
				t.Fatalf("seed %s draw %d: got %#x want %#x", c.seed, i, g, w)
			}
		}
	}
}

func TestRandSameSeedSameSequenceAndRange(t *testing.T) {
	a, _ := NewRand("0123456789abcdef0123456789abcdef")
	b, _ := NewRand("0123456789ABCDEF0123456789abcdef") // hex case does not matter
	seen := map[float64]bool{}
	for i := 0; i < 10000; i++ {
		x, y := a.Float64(), b.Float64()
		if x != y {
			t.Fatalf("draw %d differs", i)
		}
		if x < 0 || x >= 1 {
			t.Fatalf("out of range: %v", x)
		}
		seen[x] = true
	}
	if len(seen) < 9990 {
		t.Fatalf("too many repeats: %d distinct", len(seen))
	}
}

func TestRandBadSeed(t *testing.T) {
	for _, s := range []string{"", "00", strings.Repeat("g", 32), strings.Repeat("0", 31), strings.Repeat("0", 33)} {
		if _, err := NewRand(s); err == nil {
			t.Fatalf("seed %q accepted", s)
		}
	}
}

func TestNewSeedShapeAndUnique(t *testing.T) {
	a, err := NewSeed(nil)
	if err != nil || len(a) != SeedLen {
		t.Fatalf("seed %q err %v", a, err)
	}
	b, _ := NewSeed(nil)
	if a == b {
		t.Fatal("two seeds equal")
	}
	if _, err := NewRand(a); err != nil {
		t.Fatal(err)
	}
	if _, err := NewSeed(strings.NewReader("short")); err == nil {
		t.Fatal("short reader accepted")
	}
}
