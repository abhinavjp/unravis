package blackbox

import (
	"context"
	"errors"
	"io/fs"
	"math"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode"
)

// The words the Go side sends to the screen must pass the same copy rules the web
// copy lint applies (web/src/lib/copy/copylint.ts): no em dash, no en dash, and a
// Flesch-Kincaid grade of 8.5 or lower for any text of at least 8 words.

const (
	wordsMaxGrade      = 8.5
	wordsMinForGrade   = 8
	emDash             = "—"
	enDash             = "–"
	wordsSentenceSplit = `[.!?]+`
)

var (
	wordsRe      = regexp.MustCompile(`[A-Za-z0-9][A-Za-z0-9'\x{2019}-]*`)
	syllableTail = regexp.MustCompile(`(?:[^laeiouy]es|[^laeiouy]ed|[^laeiouy]e)$`)
	syllableRun  = regexp.MustCompile(`[aeiouy]{1,2}`)
	sentenceEnd  = regexp.MustCompile(wordsSentenceSplit)
)

// wordsSyllablesInPart mirrors syllablesInPart in copylint.ts.
func wordsSyllablesInPart(part string) int {
	var b strings.Builder
	for _, r := range strings.ToLower(part) {
		if r >= 'a' && r <= 'z' {
			b.WriteRune(r)
		}
	}
	w := b.String()
	if w == "" {
		return 1 // digits
	}
	if len(w) <= 3 {
		return 1
	}
	stripped := strings.TrimPrefix(syllableTail.ReplaceAllString(w, ""), "y")
	groups := len(syllableRun.FindAllString(stripped, -1))
	return max(1, groups)
}

// wordsSyllables mirrors countSyllables in copylint.ts.
func wordsSyllables(word string) int {
	sum := 0
	for _, part := range strings.Split(word, "-") {
		sum += wordsSyllablesInPart(part)
	}
	return sum
}

// wordsSentences mirrors text.split(/[.!?]+(?=\s|$)/) filtered to pieces with a letter or digit.
func wordsSentences(text string) int {
	var pieces []string
	start := 0
	for _, loc := range sentenceEnd.FindAllStringIndex(text, -1) {
		end := loc[1]
		if end == len(text) || unicode.IsSpace(rune(text[end])) {
			pieces = append(pieces, text[start:loc[0]])
			start = end
		}
	}
	pieces = append(pieces, text[start:])
	count := 0
	for _, p := range pieces {
		for _, r := range p {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				count++
				break
			}
		}
	}
	return count
}

// wordsFleschKincaid mirrors fleschKincaidGrade in copylint.ts.
func wordsFleschKincaid(text string) float64 {
	words := wordsRe.FindAllString(text, -1)
	if len(words) == 0 {
		return 0
	}
	sentences := max(1, wordsSentences(text))
	syllables := 0
	for _, w := range words {
		syllables += wordsSyllables(w)
	}
	return 0.39*(float64(len(words))/float64(sentences)) + 11.8*(float64(syllables)/float64(len(words))) - 15.59
}

type wordsCase struct {
	id   string
	text string
}

// wordsFromFunctions collects strings the package builds in code paths that a test can
// reach without a live source: the state sentences, the failure words and the sentinel errors.
func wordsFromFunctions(t *testing.T) []wordsCase {
	t.Helper()
	var out []wordsCase
	add := func(id, text string) {
		if text != "" {
			out = append(out, wordsCase{id, text})
		}
	}

	// Failure words for every reason the wizard and status screen can show.
	reasons := []string{
		string(ReasonNoAdmin), string(ReasonExists), string(ReasonPortBusy), string(ReasonFolder),
		string(ReasonAntivirus), ReasonDeclined, ReasonNoProgram, ReasonNotInstall, ReasonConfirm,
		ReasonInvalid, string(ReasonSaveFailed), string(ReasonRestartFailed), string(ReasonUpdateFailed),
		string(ReasonFolderUnsafe), ReasonHelperChanged, string(ReasonRemoveFailed),
		string(SnapDriveFull), string(SnapFileOpen), "other",
	}
	for _, r := range reasons {
		what, next := FailureWords(r)
		add("FailureWords("+r+").what", what)
		add("FailureWords("+r+").next", next)
	}
	add("GenericFailure", GenericFailure)

	// Sentinel errors that are shown to the person as they are.
	for name, e := range map[string]error{
		"ErrNotAdmin": ErrNotAdmin, "ErrNotInstalled": ErrNotInstalled, "ErrConfirmName": ErrConfirmName,
		"ErrNoProgram": ErrNoProgram, "ErrDeclined": ErrDeclined,
	} {
		add(name, e.Error())
	}

	// Words for a program that Windows could not start.
	for name, e := range map[string]error{
		"startFailureWords(no answer)":  ErrNoAnswer,
		"startFailureWords(not found)":  fs.ErrNotExist,
		"startFailureWords(permission)": fs.ErrPermission,
		"startFailureWords(deadline)":   context.DeadlineExceeded,
		"startFailureWords(other)":      errors.New("boom"),
	} {
		add(name, startFailureWords(e))
	}

	// State sentences from DeriveState.
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	base := func(mut func(*Manifest), rmut func(*Reading)) StateInfo {
		last := now.Add(-5 * time.Second)
		m := Manifest{Format: FormatVersion, Name: "web-b", IntervalSeconds: 15, State: ManifestRunning, LastSample: &last, Created: now.Add(-time.Hour)}
		if mut != nil {
			mut(&m)
		}
		r := &Reading{Manifest: m, ReadAt: now}
		if rmut != nil {
			rmut(r)
		}
		return DeriveState(StateInput{Placed: true, Reading: r})
	}
	states := map[string]StateInfo{
		"not-placed": DeriveState(StateInput{Placed: false}),
		"not-read":   DeriveState(StateInput{Placed: true}),
		"incomplete": DeriveState(StateInput{Placed: true, Reading: &Reading{Manifest: Manifest{Format: FormatVersion}}, Report: &ReadReport{Dropped: []string{"a", "b"}}}),
		"stopped":    base(func(m *Manifest) { m.State = ManifestStopped }, nil),
		"disk-paused": base(func(m *Manifest) {
			m.Events = []Event{{T: now, Kind: EventPausedDiskLow}}
			m.PausedDiskLow = true
		}, nil),
		"late": base(func(m *Manifest) {
			old := now.Add(-10 * time.Minute)
			m.LastSample = &old
		}, nil),
		"disk-low": base(nil, func(r *Reading) {
			free := 4.0
			r.Status = &StatusDoc{FreePercent: &free}
		}),
		"clock-differs": base(nil, func(r *Reading) {
			r.ServerTime = now.Add(3 * time.Second)
			r.Status = &StatusDoc{}
		}),
		"running": base(nil, nil),
	}
	for name, si := range states {
		add("DeriveState("+name+")", si.Detail)
	}

	// Read errors from the manifest parser and the transport.
	_, rerr := parseManifest([]byte("{"))
	if rerr != nil {
		add("parseManifest(half written)", rerr.Message)
	}
	_, rerr = parseManifest([]byte(`{"format":99}`))
	if rerr != nil {
		add("parseManifest(newer)", rerr.Message)
	}
	_, rerr = parseManifest([]byte(`{"format":0}`))
	if rerr != nil {
		add("parseManifest(not manifest)", rerr.Message)
	}
	src := &HTTPSource{}
	add("netErr(refused)", src.netErr(errors.New("refused")).Message)
	add("netErr(timeout)", src.netErr(context.DeadlineExceeded).Message)
	return out
}

// wordsFromSource lists strings that live in function bodies. They are copied here as
// fixed text because they sit inside code paths that need a real folder, share, HTTP
// server or Windows service to reach. Templates use a short sample value.
func wordsFromSource() []wordsCase {
	return []wordsCase{
		// state.go
		{"state.incomplete", "2 recording files could not be read to the end. What was read is used; the rest counts as a gap, never as zero."},
		{"state.late", "No sample for 5 minutes, which is more than 3 intervals. The recorder may be stuck or the PC was busy."},
		{"state.disk-low", "The recordings drive has 4% free, under 10%. The Blackbox will pause when it runs out of room."},
		{"state.clock", "The Blackbox clock differs from this PC by 3.0 seconds. Times from it are shifted by that much until the clocks agree."},
		{"state.host-slow", HostSlowSentence},
		{"state.running-note", "The clock was not compared: a folder or share has no second clock."},
		{"state.unreadable", "The Blackbox could not be read."},
		// reader.go
		{"reader.not-there", "No Blackbox is in that folder: manifest.json is not there."},
		{"reader.no-read-access", "This account may not read that folder. Ask for read access to it."},
		{"reader.no-answer-folder", "The folder did not answer. Check the connection and try again."},
		{"reader.cancelled", "The read was cancelled."},
		{"reader.bad-name", "That is not a recording file name."},
		{"reader.manifest-half", "manifest.json could not be read. It may be half written; try again in a moment."},
		{"reader.newer", "This Blackbox was written by a newer version than this Unravis understands. Update Unravis to read it."},
		{"reader.not-manifest", "That folder has a manifest.json that is not a Blackbox manifest."},
		{"reader.bad-address", "That address cannot be used."},
		{"reader.key-refused", "The read key was refused. Check the key you typed against the one printed when the Blackbox was installed."},
		{"reader.throttled", "Too many wrong keys were sent from this PC. Wait a minute, then try again."},
		{"reader.busy", "The Blackbox is serving as many readers as it allows. Try again in a few seconds."},
		{"reader.no-blackbox-there", "That address answered, but no Blackbox is there (or the file is gone)."},
		{"reader.other-status", "That address answered with 418, which a Blackbox does not send."},
		{"reader.dropped", "The connection dropped while reading. Try again."},
		{"reader.too-much", "The address answered with more data than a Blackbox sends."},
		{"reader.timeout", "The Blackbox did not answer in time. Check that the server is on, that Listens on is the server address, and that the port is open to this PC."},
		{"reader.nothing-answered", "Nothing answered at that address. Check the name and port, that the Blackbox is running, that Listens on lets other PCs in, and that the port is open to this PC."},
		// thispc.go
		{"thispc.no-answer", "The Blackbox program gave no readable answer, so Unravis cannot tell whether the change happened. Check the Blackbox status and try again."},
		{"thispc.not-here", "Changing the Blackbox on this PC is not available in this build."},
		{"thispc.temp-folder", "Unravis could not make a temporary folder for the settings."},
		{"thispc.write-settings", "Unravis could not write the settings for the helper."},
		{"thispc.helper-changed", "The Blackbox program is not the one that came with Unravis, so no permission prompt was shown and nothing was changed. Reinstall Unravis to restore it, then try again."},
		// service_update.go
		{"update.no-service-answer", "Could not ask Windows about the service, so nothing was changed:"},
		{"update.no-location", "Windows did not say where the recorder is installed, so nothing was changed."},
		{"update.new-missing", "Unravis could not find the new recorder program, so nothing was changed."},
		{"update.new-unreadable", "Unravis could not read the new recorder program, so nothing was changed. Antivirus may block it: allow it, then retry."},
		{"update.old-unreadable", "Unravis could not read the installed recorder, so nothing was changed."},
		{"update.not-stopped", "The recorder did not stop, so nothing was changed. It keeps running the previous program. Try again."},
		{"update.not-written", "The new recorder program could not be written, so nothing was changed. Antivirus may block it: allow unravis-blackbox.exe, then retry."},
		{"update.copy-mismatch", "The new recorder copy did not match, so nothing was changed. Antivirus may have changed it: allow unravis-blackbox.exe, then retry."},
		{"update.no-old-copy", "Unravis could not keep a copy of the previous program, so nothing was changed."},
		{"update.not-in-place", "The new recorder program could not be put in place."},
		{"update.rollback-no-copy", " The previous program could not be put back because there was no copy of it. Reinstall the Blackbox from Unravis."},
		{"update.rollback-failed", " The previous program could not be put back. Remove the Blackbox and place it again."},
		{"update.rollback-running", " The previous program was put back and the recorder is running with it again."},
		{"update.rollback-stopped", " The previous program was put back, but the recorder is stopped. Press Start on the Blackbox, or check Services in Windows."},
		{"update.rollback-idle", " The previous program was put back. The recorder was stopped and stays stopped."},
		// server/blackbox.go (copied here: the server package is not importable from this one)
		{"server.stopped-here", "The Blackbox service on this PC is stopped. Its recordings are kept; nothing new is recorded. Start it to record again."},
		{"server.busy-here", "Another change to the Blackbox on this PC is still running."},
		{"server.busy-here-next", "Wait for it to finish."},
	}
}

func TestSharedWordsPassCopyLint(t *testing.T) {
	cases := append(wordsFromFunctions(t), wordsFromSource()...)
	if len(cases) < 40 {
		t.Fatalf("collected only %d strings, expected the full set", len(cases))
	}
	for _, c := range cases {
		if strings.Contains(c.text, emDash) {
			t.Errorf("%s has an em dash: %q", c.id, c.text)
		}
		if strings.Contains(c.text, enDash) {
			t.Errorf("%s has an en dash: %q", c.id, c.text)
		}
		if len(wordsRe.FindAllString(c.text, -1)) < wordsMinForGrade {
			continue
		}
		if g := wordsFleschKincaid(c.text); g > wordsMaxGrade {
			t.Errorf("%s is grade %.1f (limit %.1f): %q", c.id, g, wordsMaxGrade, c.text)
		}
	}
}

// TestWordsFleschKincaidMatchesWebRules pins the Go grade to the values the web lint's
// own tests rely on, so a change to either side shows up here.
func TestWordsFleschKincaidMatchesWebRules(t *testing.T) {
	if g := wordsFleschKincaid("The cat sat on the mat. The dog ran to the park."); g >= 3 {
		t.Errorf("simple text grade %.2f, want below 3", g)
	}
	if g := wordsFleschKincaid(""); g != 0 {
		t.Errorf("empty text grade %.2f, want 0", g)
	}
	if g := wordsFleschKincaid("P95 was 12.5 ms."); math.IsNaN(g) || math.IsInf(g, 0) {
		t.Errorf("P95 sentence grade is not finite: %v", g)
	}
	if g := wordsFleschKincaid("Recording every 15 seconds for the host."); g > wordsMaxGrade {
		t.Errorf("short plain sentence grade %.2f, want at most %.1f", g, wordsMaxGrade)
	}
}
