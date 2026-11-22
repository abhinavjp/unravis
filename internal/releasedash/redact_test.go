package releasedash

import (
	"strings"
	"testing"
)

func TestRedactorHidesSecrets(t *testing.T) {
	r := NewRedactor("DESKTOP-ABC", "nachi")
	cases := map[string]string{
		"push with ghp_abcdefghijklmnopqrstuvwxyz0123456789":                "<token hidden>",
		"Authorization: Bearer abcdefghijklmnop1234":                        "<hidden>",
		"passphrase=hunter2hunter2":                                         "<hidden>",
		`loaded C:\Users\nachi\keys\release.key now`:                        "<key file>",
		"running on DESKTOP-ABC as someone":                                 "<name>",
		"\x1b[36;1mcolour\x1b[0m":                                           "colour",
		"AKIAABCDEFGHIJKLMNOP leaked":                                       "<token hidden>",
		"home /home/runner/work and /Users/bob/x":                           "<user>",
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.abcdef0123456789": "<token hidden>",
	}
	for in, want := range cases {
		got := r.Line(in)
		if !strings.Contains(got, want) {
			t.Errorf("%q -> %q, want to contain %q", in, got, want)
		}
		for _, bad := range []string{"ghp_abc", "hunter2", "release.key", "DESKTOP-ABC", "nachi", "AKIAABC", "eyJhbGci", "abcdefghijklmnop1234"} {
			if strings.Contains(got, bad) {
				t.Errorf("%q -> %q still has %q", in, got, bad)
			}
		}
	}
}

func TestRedactorHidesAPrivateKeyBlock(t *testing.T) {
	r := NewRedactor()
	lines := []string{"before", "-----BEGIN PRIVATE KEY-----", "MIIEvQIBADANBgkqhkiG9w0B", "-----END PRIVATE KEY-----", "after"}
	var out []string
	for _, l := range lines {
		out = append(out, r.Line(l))
	}
	j := strings.Join(out, "|")
	if strings.Contains(j, "MIIEvQ") || !strings.HasPrefix(j, "before|") || !strings.HasSuffix(j, "|after") {
		t.Fatalf("%s", j)
	}
}

func TestRedactorKeepsOrdinaryLines(t *testing.T) {
	r := NewRedactor("ab")
	for _, l := range []string{"ok  \tpkg\t1.2s", "[3/7] Waiting for the build", "Release v0.1.0-rc.58 is done."} {
		if got := r.Line(l); strings.Contains(got, "<") && !strings.Contains(l, "<") {
			t.Errorf("%q changed to %q", l, got)
		}
	}
}

func TestRedactorReviewCases(t *testing.T) {
	r := NewRedactor("Abhinav")
	for in, bad := range map[string]string{
		"GH_TOKEN=abc123secret":                 "abc123secret",
		"SIGNING_PASSPHRASE=hunter2":            "hunter2",
		"keyPassword=hunter2":                   "hunter2",
		"client_secret: s3cr3t-value":           "s3cr3t-value",
		"passphrase: correct horse battery":     "battery",
		"-----BEGIN PGP PRIVATE KEY BLOCK-----": "BEGIN PGP",
	} {
		if got := NewRedactor("Abhinav").Line(in); strings.Contains(got, bad) {
			t.Errorf("%q -> %q still has %q", in, got, bad)
		}
	}
	if got := r.Line("https://github.com/abhinavjp/unravis/actions/runs/1 by Abhinav"); !strings.Contains(got, "github.com/abhinavjp/unravis") || strings.Contains(got, "by Abhinav") {
		t.Errorf("name hiding must keep repository names whole: %q", got)
	}
}

func TestRedactorKeepsTestLines(t *testing.T) {
	r := NewRedactor()
	for _, l := range []string{"--- PASS: TestX (0.00s)", "    --- PASS: TestY/sub (0.01s)", "--- SKIP: TestZ (0.00s)"} {
		if got := r.Line(l); got != l {
			t.Errorf("%q changed to %q", l, got)
		}
	}
}

func TestRedactorUnclosedKeyBlockEnds(t *testing.T) {
	r := NewRedactor()
	got := []string{}
	for _, l := range []string{
		"-----BEGIN PGP PRIVATE KEY BLOCK-----", // a header with no END line, as the release self-test prints
		"ok   log redaction: nothing secret left",
		"--- PASS: TestX (0.00s)",
	} {
		got = append(got, r.Line(l))
	}
	if got[0] != "<private key hidden>" || got[1] != "ok   log redaction: nothing secret left" || got[2] != "--- PASS: TestX (0.00s)" {
		t.Fatalf("%q", got)
	}
}

func TestRedactorKeyBlockWithCIPrefix(t *testing.T) {
	r := NewRedactor()
	pfx := "job\tstep\t2026-11-22T11:32:17.6419460Z "
	in := []string{pfx + "-----BEGIN PRIVATE KEY-----", pfx + "MIIEvQIBADANBgkqhkiG9w0BAQEFAASC", pfx + "Proc-Type: 4,ENCRYPTED", pfx + "aGVsbG8=", pfx + "-----END PRIVATE KEY-----", pfx + "ok   after the key"}
	for i, l := range in {
		got := r.Line(l)
		if i < 5 && got != "<private key hidden>" {
			t.Errorf("line %d leaked: %q", i, got)
		}
		if i == 5 && !strings.Contains(got, "ok   after the key") {
			t.Errorf("line after the key was hidden: %q", got)
		}
	}
	// a key whose END line was lost: body lines stay hidden, the first sentence ends the block
	r = NewRedactor()
	for i, l := range []string{pfx + "-----BEGIN PRIVATE KEY-----", pfx + "MIIEvQIBADANBgkqhkiG9w0BAQEFAASC", pfx + "ok   next step"} {
		got := r.Line(l)
		if (i < 2) != (got == "<private key hidden>") {
			t.Errorf("line %d: %q", i, got)
		}
	}
}
