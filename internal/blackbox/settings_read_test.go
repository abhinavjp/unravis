package blackbox

import (
	"errors"
	"strings"
	"testing"
)

func TestParseVerifiedSettingsNilReaderIsRefused(t *testing.T) {
	if _, err := ParseVerifiedSettings(nil, `C:\nowhere\settings.json`); !errors.Is(err, ErrSettingsNotProven) {
		t.Fatalf("a missing reader gave %v, want ErrSettingsNotProven", err)
	}
}

func TestParseVerifiedSettingsReaderErrorIsRefusedWithoutItsText(t *testing.T) {
	read := func(string) ([]byte, error) {
		return nil, errors.New(`CreateFile C:\secret\blackbox.json: access denied`)
	}
	_, err := ParseVerifiedSettings(read, `C:\secret\blackbox.json`)
	if !errors.Is(err, ErrSettingsNotProven) {
		t.Fatalf("got %v, want ErrSettingsNotProven", err)
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "denied") {
		t.Fatalf("the refusal leaks the cause: %q", err.Error())
	}
}

func TestParseVerifiedSettingsBadJSONIsNotReportedAsUnlocked(t *testing.T) {
	read := func(string) ([]byte, error) { return []byte("{"), nil }
	_, err := ParseVerifiedSettings(read, `C:\nowhere\settings.json`)
	if err == nil || errors.Is(err, ErrSettingsNotProven) {
		t.Fatalf("broken JSON gave %v; want a settings error that is not ErrSettingsNotProven", err)
	}
}

func TestParseVerifiedSettingsInvalidValuesAreNotReportedAsUnlocked(t *testing.T) {
	read := func(string) ([]byte, error) { return []byte(`{"name":"x","intervalSeconds":7}`), nil }
	_, err := ParseVerifiedSettings(read, `C:\nowhere\settings.json`)
	if err == nil || errors.Is(err, ErrSettingsNotProven) {
		t.Fatalf("invalid values gave %v; want a validation error that is not ErrSettingsNotProven", err)
	}
}

func TestParseVerifiedSettingsReadsOnceAndNeverOpensThePath(t *testing.T) {
	const path = `C:\nowhere\settings.json`
	var calls []string
	read := func(p string) ([]byte, error) { calls = append(calls, p); return []byte(`{"name":"x"}`), nil }
	_, _ = ParseVerifiedSettings(read, path)
	if len(calls) != 1 || calls[0] != path {
		t.Fatalf("the reader was called %v, want once with exactly %q", calls, path)
	}
	// The path does not exist on any machine: a helper that opened it by itself would have failed with a not-exist error, not a parse error.
	if _, err := ParseVerifiedSettings(read, path); err != nil && errors.Is(err, ErrSettingsNotProven) {
		t.Fatalf("the helper read the path itself: %v", err)
	}
}

func TestParseVerifiedSettingsOversizeBytesAreRefusedByParse(t *testing.T) {
	read := func(string) ([]byte, error) { return make([]byte, maxConfigBytes+1), nil }
	_, err := ParseVerifiedSettings(read, `C:\nowhere\settings.json`)
	if err == nil || errors.Is(err, ErrSettingsNotProven) || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("oversize bytes gave %v; want a \"too large\" parse error", err)
	}
}

func TestSettingsHandleProblem(t *testing.T) {
	const dir, reparse = 0x10, 0x400
	for _, c := range []struct {
		name  string
		disk  bool
		attrs uint32
		links uint32
		size  uint64
		bad   bool
	}{
		{"a plain file", true, 0x20, 1, 100, false},
		{"an empty file", true, 0, 1, 0, false},
		{"exactly the limit", true, 0x20, 1, maxConfigBytes, false},
		{"not a disk file", false, 0x20, 1, 100, true},
		{"a folder", true, dir, 1, 100, true},
		{"a reparse point", true, reparse, 1, 100, true},
		{"a folder link", true, dir | reparse, 1, 100, true},
		{"two names", true, 0x20, 2, 100, true},
		{"no names", true, 0x20, 0, 100, true},
		{"over the limit", true, 0x20, 1, maxConfigBytes + 1, true},
	} {
		err := settingsHandleProblem(c.disk, c.attrs, c.links, c.size)
		if (err != nil) != c.bad {
			t.Errorf("%s: got %v, want refusal=%v", c.name, err, c.bad)
		}
	}
}
