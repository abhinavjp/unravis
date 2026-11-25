package mcpsrv

import (
	"crypto/hmac"
	"crypto/sha256"
	"strings"
	"testing"
	"time"
)

// Each part of the cursor binding is checked on its own: the cursor opens only when every one is the same.
func TestCursorCodecChecksEachBinding(t *testing.T) {
	now := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	key := func(k string) func(string, []byte) []byte {
		return func(d string, b []byte) []byte {
			m := hmac.New(sha256.New, []byte(k))
			m.Write([]byte(d))
			m.Write([]byte{0})
			m.Write(b)
			return m.Sum(nil)
		}
	}
	gen := newGen()
	base := func() *cursorCodec {
		return &cursorCodec{sign: key("k1"), now: func() time.Time { return now }, profile: "h1", rev: 4, binding: newBinding([]string{"b", "a"}, TierEvidence, false, gen)}
	}
	c := base()
	cur, err := c.Seal("audit_get_slowest", "fp1", "dashboard-pos")
	if err != nil {
		t.Fatal(err)
	}
	if pos, ok := base().Open("audit_get_slowest", "fp1", cur); !ok || pos != "dashboard-pos" {
		t.Fatalf("the same binding does not open: %q %v", pos, ok)
	}
	mut := map[string]func(*cursorCodec){
		"profile":     func(c *cursorCodec) { c.profile = "h2" },
		"revision":    func(c *cursorCodec) { c.rev = 5 },
		"source set":  func(c *cursorCodec) { c.binding = newBinding([]string{"a", "b", "c"}, TierEvidence, false, gen) },
		"tier":        func(c *cursorCodec) { c.binding = newBinding([]string{"a", "b"}, TierAggregate, false, gen) },
		"identifiers": func(c *cursorCodec) { c.binding = newBinding([]string{"a", "b"}, TierEvidence, true, gen) },
		"generation":  func(c *cursorCodec) { c.binding = newBinding([]string{"a", "b"}, TierEvidence, false, newGen()) },
		"server key":  func(c *cursorCodec) { c.sign = key("k2") },
		"expired":     func(c *cursorCodec) { c.now = func() time.Time { return now.Add(CursorTTL) } },
	}
	for name, f := range mut {
		o := base()
		f(o)
		if _, ok := o.Open("audit_get_slowest", "fp1", cur); ok {
			t.Errorf("%s: the cursor opened", name)
		}
	}
	if _, ok := base().Open("audit_get_requests", "fp1", cur); ok {
		t.Error("another tool")
	}
	if _, ok := base().Open("audit_get_slowest", "fp2", cur); ok {
		t.Error("another query")
	}
	if _, ok := base().Open("audit_get_slowest", "fp1", strings.Repeat("A", 5000)); ok {
		t.Error("oversize")
	}
	// source order does not matter, the set does
	same := base()
	same.binding = newBinding([]string{"a", "b"}, TierEvidence, false, gen)
	if _, ok := same.Open("audit_get_slowest", "fp1", cur); !ok {
		t.Error("the source set in another order is the same scope")
	}
	// no random source for the generation: no cursor can be made
	none := base()
	none.binding = ""
	if _, err := none.Seal("audit_get_slowest", "fp1", "p"); err == nil {
		t.Error("a cursor was made with no binding")
	}
	if _, err := c.Seal("audit_get_slowest", "fp1", ""); err == nil {
		t.Error("an empty position was sealed")
	}
}
