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
	for _, l := range logged {
		if l != "sql: settings-not-locked" {
			t.Fatalf("log line %q is not a fixed code", l)
		}
	}
}
