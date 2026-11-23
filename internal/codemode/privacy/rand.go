package privacy

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
)

// Rand is the generator behind the script's Math.random (spec D12): xorshift128+ with a 128-bit seed that is stored in
// code_runs.seed, so a replay draws the same numbers. It is not for secrets. The worker implements the same steps; the vectors in
// the tests are the contract between the two.
//
// The steps for one draw (all arithmetic on uint64, wrapping):
//
//	s1 = s0; s0 = t1; s1 ^= s1 << 23; s1 ^= s1 >> 17; s1 ^= s0; s1 ^= s0 >> 26; t1 = s1; return s0 + s1
//
// where (s0, t1) is the state, seeded from the 32 hex digits as two big-endian 64-bit halves. Float64 is (draw >> 11) / 2^53.
type Rand struct {
	s0, s1 uint64
}

var errBadSeed = errors.New("privacy: seed must be 32 hex digits")

// SeedLen is the length of a seed in hex digits.
const SeedLen = 32

// NewSeed makes a seed from r (crypto/rand when r is nil).
func NewSeed(r io.Reader) (string, error) {
	if r == nil {
		r = rand.Reader
	}
	var b [16]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// NewRand seeds the generator. An all-zero seed (the one state xorshift cannot leave) is replaced by a fixed non-zero state, the
// same one in the worker.
func NewRand(seed string) (*Rand, error) {
	if len(seed) != SeedLen {
		return nil, errBadSeed
	}
	var b [16]byte
	if _, err := hex.Decode(b[:], []byte(seed)); err != nil {
		return nil, errBadSeed
	}
	r := &Rand{}
	for i := 0; i < 8; i++ {
		r.s0 = r.s0<<8 | uint64(b[i])
		r.s1 = r.s1<<8 | uint64(b[8+i])
	}
	if r.s0 == 0 && r.s1 == 0 {
		r.s1 = 0x9E3779B97F4A7C15
	}
	return r, nil
}

// Uint64 returns the next 64 bits.
func (r *Rand) Uint64() uint64 {
	s1 := r.s0
	s0 := r.s1
	r.s0 = s0
	s1 ^= s1 << 23
	s1 ^= s1 >> 17
	s1 ^= s0
	s1 ^= s0 >> 26
	r.s1 = s1
	return r.s0 + r.s1
}

// Float64 returns the next number in [0, 1) with 53 random bits, the form Math.random has.
func (r *Rand) Float64() float64 {
	return float64(r.Uint64()>>11) / (1 << 53)
}
