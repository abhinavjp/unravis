package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"api-audit-explorer/internal/blackbox"
)

// BL-140 PR 7. The verified reader is a fake here, so nothing in this file proves the real reader on a PC (probe P1 is still owed).

const leakyCause = `CreateFile C:\secret\blackbox.json: Access is denied`

// refusing is a reader that refuses with a fixed word and a cause full of text that must never be logged.
func refusing(word string) func(string) ([]byte, error) {
	return func(string) ([]byte, error) { return nil, blackbox.NewSettingsRefusal(word, errors.New(leakyCause)) }
}

type startResult struct {
	err     error
	logged  []string
	started bool
	proofs  int
	cfg     blackbox.Config
}

func startWith(t *testing.T, path string, read func(string) ([]byte, error), proof func(string) error) *startResult {
	t.Helper()
	r := &startResult{}
	d := deps{readSettings: read, sqlRun: func(_ context.Context, o sqlRunOptions) error { r.started = true; r.cfg = o.Config; return nil }}
	if proof != nil {
		d.verifyConfig = func(p string) error { r.proofs++; return proof(p) }
	}
	r.err = runSQLService(context.Background(), blackbox.Paths{ConfigPath: path}, d, func(f string, a ...any) { r.logged = append(r.logged, fmt.Sprintf(f, a...)) })
	return r
}

func noLeak(t *testing.T, r *startResult) {
	t.Helper()
	all := strings.Join(r.logged, "\n")
	if r.err != nil {
		all += "\n" + r.err.Error()
	}
	if strings.Contains(all, "secret") || strings.Contains(all, "denied") || strings.Contains(all, "CreateFile") {
		t.Fatalf("the cause text reached a log line or the answer: %q", all)
	}
}

func pass(string) error { return nil }

func TestRunSQLServiceStartsFromTheVerifiedReadersBytes(t *testing.T) {
	var reads []string
	read := func(p string) ([]byte, error) { reads = append(reads, p); return settingsJSON(t, "verified"), nil }
	path := `C:\nowhere\settings.json` // never opened by path: a second open would fail
	r := startWith(t, path, read, func(string) error { t.Fatal("the by-path proof ran although the verified read worked"); return nil })
	if r.err != nil || !r.started || r.cfg.Name != "verified" || len(reads) != 1 || reads[0] != path || len(r.logged) != 0 {
		t.Fatalf("err=%v started=%v cfg=%+v reads=%v log=%v, want a start from one verified read and no log", r.err, r.started, r.cfg, reads, r.logged)
	}
}

// The three words that mean "this account or a share mode cannot do the read" fall back, once, to the by-path proof and read.
func TestRunSQLServiceFallsBackOnlyOnTheThreeOperationalWords(t *testing.T) {
	for _, w := range []string{blackbox.WordAccess, blackbox.WordUnreadable, blackbox.WordSharing} {
		t.Run(w, func(t *testing.T) {
			r := startWith(t, fallbackFile(t), refusing(w), pass)
			if r.err != nil || !r.started || r.proofs != 1 || r.cfg.Name != "fallback" {
				t.Fatalf("err=%v started=%v proofs=%d cfg=%+v, want a start from the by-path read after one proof", r.err, r.started, r.proofs, r.cfg)
			}
			if len(r.logged) != 1 || !strings.Contains(r.logged[0], "("+w+")") {
				t.Fatalf("log = %q, want exactly one line with the fixed word %q", r.logged, w)
			}
			noLeak(t, r)
		})
	}
}

// Every other word is a verdict on the file: the start stops with the fixed sentence, the by-path proof is never tried and nothing is read
// by path.
func TestRunSQLServiceStopsOnEveryOtherWord(t *testing.T) {
	for _, w := range []string{blackbox.WordPath, blackbox.WordChain, blackbox.WordNotFound, blackbox.WordNotDisk, blackbox.WordFolder, blackbox.WordLink,
		blackbox.WordNames, blackbox.WordTooLarge, blackbox.WordMoved, blackbox.WordOwner, blackbox.WordList, blackbox.WordNoReader} {
		t.Run(w, func(t *testing.T) {
			r := startWith(t, fallbackFile(t), refusing(w), func(string) error { t.Error("the weaker by-path proof was tried"); return nil })
			if !errors.Is(r.err, errSQLSettingsNotLocked) || r.started || r.proofs != 0 {
				t.Fatalf("err=%v started=%v proofs=%d, want the fixed sentence and no start", r.err, r.started, r.proofs)
			}
			if len(r.logged) != 1 || r.logged[0] != "sql: settings-not-locked ("+w+")" {
				t.Fatalf("log = %q, want one line with the fixed word %q", r.logged, w)
			}
			noLeak(t, r)
		})
	}
}

// A nil reader refuses (fail closed); it does not skip the proof.
func TestRunSQLServiceNilReaderRefuses(t *testing.T) {
	r := startWith(t, fallbackFile(t), nil, pass)
	if !errors.Is(r.err, errSQLSettingsNotLocked) || r.started || r.proofs != 0 {
		t.Fatalf("err=%v started=%v proofs=%d, want a refusal and no start", r.err, r.started, r.proofs)
	}
	if len(r.logged) != 1 || r.logged[0] != "sql: settings-not-locked (no-reader)" {
		t.Fatalf("log = %q", r.logged)
	}
}

// A reader error with no word, or with a word that is not in the fixed list, is "unreadable" and may fall back; its text is never logged.
func TestRunSQLServiceUntaggedAndUnknownWordsAreUnreadable(t *testing.T) {
	for name, read := range map[string]func(string) ([]byte, error){
		"untagged": func(string) ([]byte, error) { return nil, errors.New(leakyCause) },
		"unknown":  refusing(`C:\secret free text`),
	} {
		t.Run(name, func(t *testing.T) {
			r := startWith(t, fallbackFile(t), read, pass)
			if r.err != nil || !r.started || len(r.logged) != 1 || !strings.Contains(r.logged[0], "("+blackbox.WordUnreadable+")") {
				t.Fatalf("err=%v started=%v log=%q, want a fallback logged as unreadable", r.err, r.started, r.logged)
			}
			noLeak(t, r)
		})
	}
}

// The fallback is not a way round the proof: a refusing or missing by-path proof still stops the start.
func TestRunSQLServiceFallbackStillNeedsThePathProof(t *testing.T) {
	for name, proof := range map[string]func(string) error{"proof refuses": func(string) error { return errors.New("not locked") }, "no proof": nil} {
		t.Run(name, func(t *testing.T) {
			r := startWith(t, fallbackFile(t), refusing(blackbox.WordAccess), proof)
			if !errors.Is(r.err, errSQLSettingsNotLocked) || r.started {
				t.Fatalf("err=%v started=%v, want the fixed refusal and no start", r.err, r.started)
			}
			if len(r.logged) != 2 || r.logged[1] != "sql: settings-not-locked (by-path)" {
				t.Fatalf("log = %q, want the fallback line then the not-locked line", r.logged)
			}
			noLeak(t, r)
		})
	}
}

func TestRunSQLServiceBadVerifiedSettingsAreNotReportedAsUnlocked(t *testing.T) {
	r := startWith(t, `C:\nowhere\settings.json`, func(string) ([]byte, error) { return []byte("{"), nil }, func(string) error { t.Error("proof ran for proven bytes"); return nil })
	if r.err == nil || errors.Is(r.err, errSQLSettingsNotLocked) || r.started || len(r.logged) != 0 {
		t.Fatalf("err=%v started=%v log=%q, want a settings error that is not the unlocked sentence", r.err, r.started, r.logged)
	}
}

// settingsJSON is valid settings text for the OS the test runs on (an absolute folder; no default Windows log paths).
func settingsJSON(t *testing.T, name string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{"name": name, "folder": filepath.Join(t.TempDir(), "rec"), "logPaths": []string{}})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func fallbackFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "blackbox.json")
	if err := os.WriteFile(p, settingsJSON(t, "fallback"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}
