package main

import (
	"context"
	"errors"
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
