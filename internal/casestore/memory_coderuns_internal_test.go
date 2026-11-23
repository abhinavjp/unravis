package casestore

import (
	"context"
	"testing"
	"time"
)

// The memory store runs the same verifier; these cases need rows the sqlite
// schema refuses (a second end record), so they are made by hand here.

func memRunCase(t *testing.T) (*memCase, CodeRunStore) {
	t.Helper()
	st := NewMemoryWith(DefaultCaps, time.Now)
	info, err := st.Create(context.Background(), "Mem audit")
	if err != nil {
		t.Fatal(err)
	}
	c, err := st.Open(context.Background(), info.ID)
	if err != nil {
		t.Fatal(err)
	}
	s := c.(CodeRunStore)
	for i := 0; i < 2; i++ {
		r, err := s.BeginRun(context.Background(), RunStart{Surface: "cli", Source: "return 1", KeepResults: true})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.RecordCall(context.Background(), r.RunID, CallRecord{Seq: 1, DoneSeq: 1, Tool: "t", Status: "ok", Result: []byte(`{}`)}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.EndRun(context.Background(), r.RunID, RunEnd{Status: RunOK}); err != nil {
			t.Fatal(err)
		}
	}
	return c.(*memCase), s
}

func checks(rep ChainReport) map[string]bool {
	m := map[string]bool{}
	for _, p := range rep.Problems {
		m[p.Check] = true
	}
	return m
}

func TestMemReplayedEndRecordIsFound(t *testing.T) {
	c, s := memRunCase(t)
	m := c.d.code
	// Paste the first run's old end record at the tail, correctly linked and hashed.
	old := m.chain[2] // the first run: begin, call, end
	tailN, tailHash := m.tail()
	m.append(NewChainRow(tailN+1, old.RunID, old.Phase, 0, old.RecordJSON, tailHash))
	rep, err := s.VerifyCodeChain(context.Background(), nil)
	if err != nil || rep.OK {
		t.Fatalf("verify = %+v %v", rep, err)
	}
	got := checks(rep)
	if !got[CheckDuplicateEnd] || !got[CheckEndMismatch] {
		t.Errorf("checks = %v", got)
	}
}

func TestMemEditedRunRowIsFound(t *testing.T) {
	c, s := memRunCase(t)
	for _, id := range c.d.code.order[:1] {
		c.d.code.runs[id].Reason = "edited"
	}
	rep, _ := s.VerifyCodeChain(context.Background(), nil)
	if rep.OK || !checks(rep)[CheckBeginMismatch] || rep.Problems[0].N != 1 {
		t.Fatalf("verify = %+v", rep)
	}
}

func TestMemDeletedMiddleRowIsFound(t *testing.T) {
	c, s := memRunCase(t)
	m := c.d.code
	m.chain = append(m.chain[:1:1], m.chain[2:]...) // the first run's call record
	rep, _ := s.VerifyCodeChain(context.Background(), nil)
	if rep.OK || !checks(rep)[CheckChainGap] {
		t.Fatalf("verify = %+v", rep)
	}
}

func TestNewRunIDIsUUIDv7(t *testing.T) {
	a, err := NewRunID(time.UnixMilli(1700000000000))
	if err != nil || !ValidRunID(a) || a[14] != '7' {
		t.Fatalf("%q %v", a, err)
	}
	b, _ := NewRunID(time.UnixMilli(1700000000001))
	if a[:13] >= b[:13] {
		t.Errorf("ids do not sort by time: %s %s", a, b)
	}
}

func TestReplaceAddresses(t *testing.T) {
	for in, want := range map[string]string{
		"from 10.1.2.3 to 8.8.8.8":                "from [ip] to [ip]",
		"peer 2001:db8::1 ok":                     "peer [ip] ok",
		"[fe80::1]:8080":                          "[[ip]]:8080",
		"::1 loopback":                            "[ip] loopback",
		"v1.2.3 at 12:30:45":                      "v1.2.3 at 12:30:45",
		"std::bad Foo::Bar":                       "std::bad Foo::Bar",
		"aa:bb:cc:dd:ee:ff":                       "aa:bb:cc:dd:ee:ff",
		"999.1.1.1 and 1.2.3.4.5":                 "999.1.1.1 and [ip].5",
		"2001:0db8:0000:0000:0000:0000:0000:0001": "[ip]",
	} {
		if got, _ := ReplaceAddresses(in); got != want {
			t.Errorf("ReplaceAddresses(%q) = %q, want %q", in, got, want)
		}
	}
}
