package blackbox

import (
	"errors"
	"strings"
	"testing"
)

func TestParseVerifiedSettingsNilReaderIsRefused(t *testing.T) {
	if _, err := ParseVerifiedSettings(nil, `C:\nowhere\settings.json`, nil); !errors.Is(err, ErrSettingsNotProven) {
		t.Fatalf("a missing reader gave %v, want ErrSettingsNotProven", err)
	}
}

func TestParseVerifiedSettingsReaderErrorIsRefusedWithoutItsText(t *testing.T) {
	read := func(string) ([]byte, error) {
		return nil, errors.New(`CreateFile C:\secret\blackbox.json: access denied`)
	}
	_, err := ParseVerifiedSettings(read, `C:\secret\blackbox.json`, nil)
	if !errors.Is(err, ErrSettingsNotProven) {
		t.Fatalf("got %v, want ErrSettingsNotProven", err)
	}
	if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "denied") {
		t.Fatalf("the refusal leaks the cause: %q", err.Error())
	}
}

func TestParseVerifiedSettingsBadJSONIsNotReportedAsUnlocked(t *testing.T) {
	read := func(string) ([]byte, error) { return []byte("{"), nil }
	_, err := ParseVerifiedSettings(read, `C:\nowhere\settings.json`, nil)
	if err == nil || errors.Is(err, ErrSettingsNotProven) {
		t.Fatalf("broken JSON gave %v; want a settings error that is not ErrSettingsNotProven", err)
	}
}

func TestParseVerifiedSettingsInvalidValuesAreNotReportedAsUnlocked(t *testing.T) {
	read := func(string) ([]byte, error) { return []byte(`{"name":"x","intervalSeconds":7}`), nil }
	_, err := ParseVerifiedSettings(read, `C:\nowhere\settings.json`, nil)
	if err == nil || errors.Is(err, ErrSettingsNotProven) {
		t.Fatalf("invalid values gave %v; want a validation error that is not ErrSettingsNotProven", err)
	}
}

func TestParseVerifiedSettingsReadsOnceAndNeverOpensThePath(t *testing.T) {
	const path = `C:\nowhere\settings.json`
	var calls []string
	read := func(p string) ([]byte, error) { calls = append(calls, p); return []byte(`{"name":"x"}`), nil }
	_, _ = ParseVerifiedSettings(read, path, nil)
	if len(calls) != 1 || calls[0] != path {
		t.Fatalf("the reader was called %v, want once with exactly %q", calls, path)
	}
	// The path does not exist on any machine: a helper that opened it by itself would have failed with a not-exist error, not a parse error.
	if _, err := ParseVerifiedSettings(read, path, nil); err != nil && errors.Is(err, ErrSettingsNotProven) {
		t.Fatalf("the helper read the path itself: %v", err)
	}
}

func TestParseVerifiedSettingsOversizeBytesAreRefusedByParse(t *testing.T) {
	read := func(string) ([]byte, error) { return make([]byte, maxConfigBytes+1), nil }
	_, err := ParseVerifiedSettings(read, `C:\nowhere\settings.json`, nil)
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

// The cause of a refusal is logged as one fixed word, never a path, a Windows message or anything read from the file.
func TestParseVerifiedSettingsLogsOnlyAFixedWord(t *testing.T) {
	var got []string
	note := func(w string) { got = append(got, w) }
	if _, err := ParseVerifiedSettings(nil, `C:\secret\blackbox.json`, note); !errors.Is(err, ErrSettingsNotProven) {
		t.Fatal(err)
	}
	tagged := func(string) ([]byte, error) {
		return nil, refuse(WordOwner, errors.New(`C:\secret\blackbox.json is owned by S-1-5-21-123`))
	}
	if _, err := ParseVerifiedSettings(tagged, `C:\secret\blackbox.json`, note); !errors.Is(err, ErrSettingsNotProven) {
		t.Fatal(err)
	}
	untagged := func(string) ([]byte, error) {
		return nil, errors.New(`CreateFile C:\secret\blackbox.json: access denied`)
	}
	if _, err := ParseVerifiedSettings(untagged, `C:\secret\blackbox.json`, note); !errors.Is(err, ErrSettingsNotProven) {
		t.Fatal(err)
	}
	want := []string{WordNoReader, WordOwner, WordUnreadable}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("logged %v, want %v", got, want)
	}
	for _, w := range got {
		if strings.ContainsAny(w, `\:/ `) || strings.Contains(w, "secret") {
			t.Fatalf("the logged word %q is not a fixed word", w)
		}
	}
	// A successful read logs nothing.
	got = nil
	ok := func(string) ([]byte, error) { return []byte("{"), nil }
	_, _ = ParseVerifiedSettings(ok, `C:\x\blackbox.json`, note)
	if len(got) != 0 {
		t.Fatalf("a proven file logged %v", got)
	}
}

func TestSettingsHandleProblemNamesTheCause(t *testing.T) {
	const dir, reparse = 0x10, 0x400
	for _, c := range []struct {
		disk  bool
		attrs uint32
		links uint32
		size  uint64
		word  string
	}{
		{false, 0, 1, 1, WordNotDisk},
		{true, dir, 1, 1, WordFolder},
		{true, reparse, 1, 1, WordLink},
		{true, 0, 2, 1, WordNames},
		{true, 0, 1, maxConfigBytes + 1, WordTooLarge},
	} {
		if got := RefusalWord(settingsHandleProblem(c.disk, c.attrs, c.links, c.size)); got != c.word {
			t.Errorf("%+v: word %q, want %q", c, got, c.word)
		}
	}
}

// The by-path fallback is allowed for exactly two words (BL-140 PR 7, temporary until probe P1 passes); every other word, including
// sharing and any word not in the list, stops the start. The list is the one allWords, so a new word is covered without editing this test.
func TestFallbackAllowedOnlyForAccessAndUnreadable(t *testing.T) {
	for _, w := range Words() {
		want := w == WordAccess || w == WordUnreadable
		if FallbackAllowed(w) != want {
			t.Errorf("FallbackAllowed(%q) = %v, want %v", w, !want, want)
		}
		if got := FallbackAllowedFor(NewSettingsRefusal(w, errors.New("x"))); got != want {
			t.Errorf("FallbackAllowedFor(%q) = %v, want %v", w, got, want)
		}
	}
	for _, w := range []string{"", "anything else", `C:\secret`} {
		if FallbackAllowed(w) || FallbackAllowedFor(NewSettingsRefusal(w, errors.New("x"))) {
			t.Errorf("an unknown word %q must not allow the fallback", w)
		}
	}
	if FallbackAllowedFor(nil) || FallbackAllowedFor(errors.New("untagged")) || FallbackAllowedFor(&SettingsRefusal{Word: WordAccess}) {
		t.Error("nil, untagged and hand-built refusals must stop the start")
	}
}

// ParseVerifiedSettings keeps the decision taken from the reader's own error: an untagged or unknown error is logged as unreadable but
// must not fall back, while a real access refusal still can.
func TestParseVerifiedSettingsFallbackDecisionIgnoresTheClampedWord(t *testing.T) {
	for name, c := range map[string]struct {
		err  error
		want bool
	}{
		"access":   {NewSettingsRefusal(WordAccess, errors.New("x")), true},
		"sharing":  {NewSettingsRefusal(WordSharing, errors.New("x")), false},
		"untagged": {errors.New("x"), false},
		"unknown":  {NewSettingsRefusal("new-word", errors.New("x")), false},
	} {
		_, err := ParseVerifiedSettings(func(string) ([]byte, error) { return nil, c.err }, "p", nil)
		if FallbackAllowedFor(err) != c.want {
			t.Errorf("%s: FallbackAllowedFor = %v, want %v", name, !c.want, c.want)
		}
	}
	if _, err := ParseVerifiedSettings(nil, "p", nil); FallbackAllowedFor(err) {
		t.Error("a nil reader must not fall back")
	}
}

// The refusal ParseVerifiedSettings returns wraps ErrSettingsNotProven, carries the fixed word, and leaks neither the cause nor free text.
func TestParseVerifiedSettingsRefusalCarriesOnlyTheFixedWord(t *testing.T) {
	cause := `CreateFile C:\secret\blackbox.json: Access is denied`
	for _, c := range []struct {
		err  error
		word string
	}{
		{NewSettingsRefusal(WordOwner, errors.New(cause)), WordOwner},
		{NewSettingsRefusal(`C:\secret free text`, errors.New(cause)), WordUnreadable},
		{errors.New(cause), WordUnreadable},
	} {
		_, err := ParseVerifiedSettings(func(string) ([]byte, error) { return nil, c.err }, `C:\x\blackbox.json`, nil)
		if !errors.Is(err, ErrSettingsNotProven) || RefusalWord(err) != c.word {
			t.Fatalf("err=%v word=%q, want ErrSettingsNotProven and word %q", err, RefusalWord(err), c.word)
		}
		if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "denied") {
			t.Fatalf("the refusal leaks: %q", err.Error())
		}
	}
	if _, err := ParseVerifiedSettings(nil, "p", nil); RefusalWord(err) != WordNoReader {
		t.Fatalf("a nil reader gave word %q, want %q", RefusalWord(err), WordNoReader)
	}
	if got := (&SettingsRefusal{Word: WordList}).Error(); !strings.Contains(got, WordList) {
		t.Fatalf("a refusal with no cause printed %q", got)
	}
}
