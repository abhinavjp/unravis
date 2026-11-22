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

// BL-140: the SQL service proves its settings before it reads them, at every start.
func TestRunSQLServiceRefusesUnlockedSettings(t *testing.T) {
	ran := false
	d := deps{
		verifyConfig: func(string) error { return errors.New("driver text C:\\secret must never reach the answer") },
		sqlRun:       func(context.Context, sqlRunOptions) error { ran = true; return nil },
	}
	var logged []string
	err := runSQLService(context.Background(), blackbox.Paths{ConfigPath: `C:\nowhere\settings.json`}, d, func(f string, a ...any) { logged = append(logged, f) })
	if !errors.Is(err, errSQLSettingsNotLocked) {
		t.Fatalf("err = %v, want the fixed sentence", err)
	}
	if ran {
		t.Fatal("the runner started although the proof refused")
	}
	if len(logged) != 1 || logged[0] != "sql: settings-not-locked" {
		t.Fatalf("log = %v, want one fixed code line", logged)
	}
}

// A proof that passes (or no proof, as in tests) does not stop the start: the error, if any, is not the refusal.
func TestRunSQLServiceProofPassDoesNotRefuse(t *testing.T) {
	for name, verify := range map[string]func(string) error{"passing proof": func(string) error { return nil }, "no proof": nil} {
		t.Run(name, func(t *testing.T) {
			d := deps{verifyConfig: verify, sqlRun: func(context.Context, sqlRunOptions) error { return nil }}
			err := runSQLService(context.Background(), blackbox.Paths{ConfigPath: `C:\nowhere\settings.json`}, d, nil)
			if errors.Is(err, errSQLSettingsNotLocked) {
				t.Fatalf("the proof passed but the start was refused: %v", err)
			}
		})
	}
}

// BL-140 PR 7: the verified reader is tried first. These use a real temp file for the by-path fallback; the verified reader is a fake, so
// nothing here proves the real reader on a real PC (probe P1 is still owed).
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

func TestRunSQLServiceStartsFromTheVerifiedReadersBytes(t *testing.T) {
	var reads []string
	var got blackbox.Config
	d := deps{
		readSettings: func(p string) ([]byte, error) { reads = append(reads, p); return settingsJSON(t, "verified"), nil },
		verifyConfig: func(string) error { t.Fatal("the by-path proof ran although the verified read worked"); return nil },
		sqlRun:       func(_ context.Context, o sqlRunOptions) error { got = o.Config; return nil },
	}
	var logged []string
	p := blackbox.Paths{ConfigPath: `C:\nowhere\settings.json`} // never opened by path: a second open would fail
	_ = runSQLService(context.Background(), p, d, func(f string, a ...any) { logged = append(logged, fmt.Sprintf(f, a...)) })
	if len(reads) != 1 || reads[0] != p.ConfigPath {
		t.Fatalf("reads = %v, want one read of the config path", reads)
	}
	if got.Name != "verified" {
		t.Fatalf("the service ran with %+v, want the verified bytes", got)
	}
	if len(logged) != 0 {
		t.Fatalf("a good read logged %v", logged)
	}
}

// Every refusal word, a plain error and no reader all fall back to today's path (proof, then read by path), log one line, never crash.
func TestRunSQLServiceFallsBackOnEveryRefusal(t *testing.T) {
	words := []string{blackbox.WordPath, blackbox.WordChain, blackbox.WordNotFound, blackbox.WordSharing, blackbox.WordAccess, blackbox.WordNotDisk,
		blackbox.WordFolder, blackbox.WordLink, blackbox.WordNames, blackbox.WordTooLarge, blackbox.WordMoved, blackbox.WordOwner, blackbox.WordList, blackbox.WordUnreadable}
	for _, w := range words {
		t.Run(w, func(t *testing.T) {
			path := fallbackFile(t)
			proofs, runs := 0, 0
			var got blackbox.Config
			d := deps{
				readSettings: func(string) ([]byte, error) {
					// The refusal's own text is never read (only its Word), so it is left empty.
					return nil, &blackbox.SettingsRefusal{Word: w}
				},
				verifyConfig: func(string) error { proofs++; return nil },
				sqlRun:       func(_ context.Context, o sqlRunOptions) error { runs++; got = o.Config; return nil },
			}
			var logged []string
			err := runSQLService(context.Background(), blackbox.Paths{ConfigPath: path}, d, func(f string, a ...any) { logged = append(logged, fmt.Sprintf(f, a...)) })
			if err != nil || runs != 1 || proofs != 1 || got.Name != "fallback" {
				t.Fatalf("err=%v runs=%d proofs=%d cfg=%+v, want a start from the by-path read after one proof", err, runs, proofs, got)
			}
			if len(logged) != 1 || !strings.Contains(logged[0], "("+w+")") || strings.Contains(logged[0], "secret") || strings.Contains(logged[0], "denied") {
				t.Fatalf("log = %q, want one line with the fixed word %q and no path or system text", logged, w)
			}
		})
	}
}

func TestRunSQLServiceFallbackStillRefusesAnUnlockedFile(t *testing.T) {
	ran := false
	d := deps{
		readSettings: func(string) ([]byte, error) { return nil, errors.New("refused") },
		verifyConfig: func(string) error { return errors.New("not locked") },
		sqlRun:       func(context.Context, sqlRunOptions) error { ran = true; return nil },
	}
	var logged []string
	err := runSQLService(context.Background(), blackbox.Paths{ConfigPath: fallbackFile(t)}, d, func(f string, a ...any) { logged = append(logged, fmt.Sprintf(f, a...)) })
	if !errors.Is(err, errSQLSettingsNotLocked) || ran {
		t.Fatalf("err=%v ran=%v, want the fixed refusal and no start (the fallback must not widen what is read)", err, ran)
	}
	if len(logged) != 2 || logged[1] != "sql: settings-not-locked" {
		t.Fatalf("log = %q, want the fallback line then the not-locked line", logged)
	}
}

func TestRunSQLServiceBadVerifiedSettingsAreNotReportedAsUnlocked(t *testing.T) {
	d := deps{
		readSettings: func(string) ([]byte, error) { return []byte("{"), nil },
		verifyConfig: func(string) error { t.Fatal("proof ran for bytes that were already proven"); return nil },
		sqlRun:       func(context.Context, sqlRunOptions) error { t.Fatal("started with bad settings"); return nil },
	}
	var logged []string
	err := runSQLService(context.Background(), blackbox.Paths{ConfigPath: `C:\nowhere\settings.json`}, d, func(f string, a ...any) { logged = append(logged, fmt.Sprintf(f, a...)) })
	if err == nil || errors.Is(err, errSQLSettingsNotLocked) {
		t.Fatalf("err = %v, want a settings error that is not the unlocked sentence", err)
	}
	if len(logged) != 0 {
		t.Fatalf("log = %q, want none", logged)
	}
}

func TestRunSQLServiceNoReaderFallsBackQuietly(t *testing.T) {
	d := deps{sqlRun: func(context.Context, sqlRunOptions) error { return nil }}
	if err := runSQLService(context.Background(), blackbox.Paths{ConfigPath: fallbackFile(t)}, d, nil); err != nil {
		t.Fatalf("err = %v", err)
	}
}
