package codeapi

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/clisrv"
	"api-audit-explorer/internal/codemode"
	"api-audit-explorer/internal/codemode/settingsfile"
)

// "No time limit" for the command line (prototype v2, owner approved). Same rig as cligate_test.go: the real broker, the real routes, a
// real sealed case store, the gate's clock fake.

func (r *rig) rawSettings() map[string]json.RawMessage {
	r.t.Helper()
	res := r.call(http.MethodGet, "/api/code/settings", "")
	var m map[string]json.RawMessage
	if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &m) != nil {
		r.t.Fatalf("settings = %d %s", res.Code, res.Body)
	}
	return m
}

// captureLog sends the standard logger's lines to a buffer for the test (the app's log is where window changes are recorded).
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(old) })
	return &buf
}

func TestUnlimitedLetsTheCommandLineRunAndShowsNoEndTime(t *testing.T) {
	r := newRig(t, rigOpts{spawner: &spawn{}, cliShut: true})
	id := r.newCase("A")
	r.enable()
	// The field is always there, false when it is not on.
	if raw, ok := r.rawSettings()["cliUnlimited"]; !ok || string(raw) != "false" {
		t.Fatalf("cliUnlimited on a new app: %s (present %v)", raw, ok)
	}
	c := cliOf(t, r)
	if _, err := c.Run(context.Background(), clisrv.CodeRunInput{Code: "1"}); refusalOf(t, err) != codemode.CodeCLIClosed {
		t.Fatalf("before: %v", err)
	}
	if res := r.setCLI(`{"cliUnlimited":true}`); res.Code != http.StatusOK {
		t.Fatalf("unlimited = %d %s", res.Code, res.Body)
	}
	if s := r.settings(); !s.CLIUnlimited || s.CLIOpenUntil != "" {
		t.Fatalf("settings while unlimited: %+v", s)
	}
	// Long after any timed window would have ended, the command line still runs (the run itself ends as "no safe runner" in this build,
	// or runs on the fake worker, neither is the gate); the run is recorded as the command line's.
	r.clock.add(1000 * time.Hour)
	if _, err := c.Run(context.Background(), clisrv.CodeRunInput{Code: "1", Reason: "x"}); err != nil && refusalOf(t, err) == codemode.CodeCLIClosed {
		t.Fatalf("unlimited: still refused: %v", err)
	}
	runs := r.runsOf(id)
	if len(runs) != 1 || runs[0].Surface != "cli" {
		t.Fatalf("runs: %+v", runs)
	}
	// cliUnlimited:false closes the command line entirely.
	if res := r.setCLI(`{"cliUnlimited":false}`); res.Code != http.StatusOK {
		t.Fatalf("close = %d %s", res.Code, res.Body)
	}
	if s := r.settings(); s.CLIUnlimited || s.CLIOpenUntil != "" {
		t.Fatalf("settings after close: %+v", s)
	}
	if _, err := c.Run(context.Background(), clisrv.CodeRunInput{Code: "1"}); refusalOf(t, err) != codemode.CodeCLIClosed {
		t.Fatalf("after close: %v", err)
	}
}

func TestUnlimitedClosesWhenCodeModeGoesOffAndStaysShutAfterwards(t *testing.T) {
	r := newRig(t, rigOpts{spawner: &spawn{}, cliShut: true})
	r.newCase("A")
	r.enable()
	r.setCLI(`{"cliUnlimited":true}`)
	r.setCLI(`{"enabled":false}`)
	if r.settings().CLIUnlimited {
		t.Fatal("unlimited survived switching code mode off")
	}
	r.setCLI(`{"enabled":true}`)
	if r.settings().CLIUnlimited {
		t.Fatal("unlimited came back when code mode was switched on again")
	}
	if _, err := cliOf(t, r).Run(context.Background(), clisrv.CodeRunInput{Code: "1"}); refusalOf(t, err) != codemode.CodeCLIClosed {
		t.Fatalf("after off and on: %v", err)
	}
	// Asking for unlimited and switching code mode off in one request ends shut.
	r.setCLI(`{"enabled":false,"cliUnlimited":true}`)
	if r.settings().CLIUnlimited {
		t.Fatal("unlimited opened with code mode off")
	}
	// Unlimited while code mode has never been switched on is shut too.
	r2 := newRig(t, rigOpts{spawner: &spawn{}, cliShut: true})
	if res := r2.setCLI(`{"cliUnlimited":true}`); res.Code != http.StatusOK || r2.settings().CLIUnlimited {
		t.Fatalf("unlimited with code mode off: %d %s", res.Code, res.Body)
	}
}

func TestATimedWindowClosesUnlimitedAndZeroClosesEverything(t *testing.T) {
	r := newRig(t, rigOpts{spawner: &spawn{}, cliShut: true})
	r.newCase("A")
	r.enable()
	r.setCLI(`{"cliUnlimited":true}`)
	if res := r.setCLI(`{"cliMinutes":10}`); res.Code != http.StatusOK {
		t.Fatalf("timed = %d %s", res.Code, res.Body)
	}
	s := r.settings()
	want := r.clock.now().Add(10 * time.Minute).UTC().Format(time.RFC3339)
	if s.CLIUnlimited || s.CLIOpenUntil != want {
		t.Fatalf("after a timed window: %+v, want end %s", s, want)
	}
	r.clock.add(10 * time.Minute)
	if _, err := cliOf(t, r).Run(context.Background(), clisrv.CodeRunInput{Code: "1"}); refusalOf(t, err) != codemode.CodeCLIClosed {
		t.Fatalf("the timed window that replaced unlimited did not end: %v", err)
	}
	// cliUnlimited:false together with minutes above 0 is a timed window; minutes 0 alone closes unlimited.
	r.setCLI(`{"cliUnlimited":true}`)
	if res := r.setCLI(`{"cliUnlimited":false,"cliMinutes":5}`); res.Code != http.StatusOK || r.settings().CLIUnlimited || r.settings().CLIOpenUntil == "" {
		t.Fatalf("false with minutes: %d %s", res.Code, res.Body)
	}
	r.setCLI(`{"cliUnlimited":true}`)
	if res := r.setCLI(`{"cliMinutes":0}`); res.Code != http.StatusOK || r.settings().CLIUnlimited {
		t.Fatalf("minutes 0: %d %s", res.Code, res.Body)
	}
	// unlimited true with 0 minutes (or none) is fine and opens unlimited.
	for _, body := range []string{`{"cliUnlimited":true,"cliMinutes":0}`, `{"cliUnlimited":true}`} {
		r.setCLI(`{"cliMinutes":0}`)
		if res := r.setCLI(body); res.Code != http.StatusOK || !r.settings().CLIUnlimited {
			t.Fatalf("%s = %d %s", body, res.Code, res.Body)
		}
	}
	// An unrelated setting leaves it as it is.
	r.setCLI(`{"keepResults":true}`)
	if !r.settings().CLIUnlimited {
		t.Fatal("an unrelated setting closed unlimited")
	}
}

func TestUnlimitedAndMinutesTogetherAreRefusedAndChangeNothing(t *testing.T) {
	r := newRig(t, rigOpts{spawner: &spawn{}, cliShut: true})
	r.enable()
	for _, body := range []string{`{"cliUnlimited":true,"cliMinutes":5}`, `{"cliUnlimited":true,"cliMinutes":60,"keepResults":true}`} {
		res := r.setCLI(body)
		wantProblem(t, res, http.StatusBadRequest, codeBadRequest)
		if !strings.Contains(res.Body.String(), "not both") {
			t.Errorf("%s: words: %s", body, res.Body)
		}
	}
	if s := r.settings(); s.CLIUnlimited || s.CLIOpenUntil != "" || s.KeepResults {
		t.Fatalf("a refused request changed something: %+v", s)
	}
	// Also while unlimited is on: the refused request does not close it or change its state.
	r.setCLI(`{"cliUnlimited":true}`)
	if res := r.setCLI(`{"cliUnlimited":true,"cliMinutes":5}`); res.Code != http.StatusBadRequest {
		t.Fatalf("refusal while on = %d", res.Code)
	}
	if !r.settings().CLIUnlimited {
		t.Fatal("a refused request closed unlimited")
	}
}

func TestUnlimitedMustBeABoolean(t *testing.T) {
	r := newRig(t, rigOpts{spawner: &spawn{}, cliShut: true})
	r.enable()
	for _, body := range []string{`{"cliUnlimited":"true"}`, `{"cliUnlimited":1}`, `{"cliUnlimited":"yes"}`, `{"cliUnlimited":[]}`, `{"cliUnlimited":{}}`, `{"cliUnlimited":"no time limit"}`} {
		if res := r.setCLI(body); res.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", body, res.Code)
		}
	}
	if r.settings().CLIUnlimited {
		t.Fatal("a non-boolean opened unlimited")
	}
}

// Without a gate (the build cannot open the command line) a request for unlimited is refused, never ignored with a 200.
func TestSettingsRefuseUnlimitedWithoutAGate(t *testing.T) {
	r := newRig(t, rigOpts{spawner: &spawn{}, noGate: true})
	r.enable()
	if res := r.setCLI(`{"cliUnlimited":true}`); res.Code != http.StatusBadRequest {
		t.Fatalf("no gate = %d", res.Code)
	}
	if raw := r.rawSettings()["cliUnlimited"]; string(raw) != "false" {
		t.Fatalf("no gate reports cliUnlimited %s", raw)
	}
}

// The state is memory only: the settings file never holds the field, a file that holds it is not honoured, and an app that starts from
// the same folder starts with the command line shut.
func TestUnlimitedIsNeverWrittenToDiskAndAFileCannotTurnItOn(t *testing.T) {
	dir := t.TempDir()
	r := newRig(t, rigOpts{spawner: &spawn{}, cliShut: true, settingsIn: dir})
	r.enable()
	r.setCLI(`{"cliUnlimited":true}`)
	// Another save while unlimited is on.
	r.setCLI(`{"keepResults":true}`)
	b, err := os.ReadFile(filepath.Join(dir, settingsfile.FileName)) //nolint:gosec // a test file in the test's own temp folder
	if err != nil {
		t.Fatal(err)
	}
	if low := strings.ToLower(string(b)); strings.Contains(low, "unlimited") || strings.Contains(low, "cli") {
		t.Fatalf("the unlimited state is in the settings file: %s", b)
	}
	if !r.settings().CLIUnlimited {
		t.Fatal("not unlimited")
	}
	// The same folder, a new app: shut, though code mode is still on in the file.
	r2 := newRig(t, rigOpts{spawner: &spawn{}, cliShut: true, settingsIn: dir})
	if r2.settings().CLIUnlimited {
		t.Fatal("a new app starts unlimited")
	}
	// A file that says it is not honoured: the file is read as damaged, code mode is off, and the command line stays shut.
	if err := os.WriteFile(filepath.Join(dir, settingsfile.FileName), []byte(`{"enabled":true,"cliUnlimited":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	r3 := newRig(t, rigOpts{spawner: &spawn{}, cliShut: true, settingsIn: dir})
	r3.newCase("A")
	s := r3.settings()
	if s.CLIUnlimited || s.Enabled || !s.Damaged {
		t.Fatalf("a hand-edited file was honoured: %+v", s)
	}
	if _, err := cliOf(t, r3).Run(context.Background(), clisrv.CodeRunInput{Code: "1"}); err == nil {
		t.Fatal("a hand-edited file let the command line run")
	}
	if _, err := settingsfile.New(dir).Load(); err == nil {
		t.Fatal("the file with cliUnlimited loaded without error")
	}
}

// Each change to the command-line window is one line in the app's log: opened for a time, opened with no time limit, closed (and why
// when code mode going off was the reason). A request that did not touch the window writes none.
func TestWindowChangesAreRecordedInTheLog(t *testing.T) {
	r := newRig(t, rigOpts{spawner: &spawn{}, cliShut: true})
	r.enable()
	lines := func(f func()) string {
		buf := captureLog(t)
		f()
		return buf.String()
	}
	if got := lines(func() { r.setCLI(`{"cliMinutes":15}`) }); !strings.Contains(got, "Command line window: opened for 15 minutes.") {
		t.Fatalf("timed open: %q", got)
	}
	if got := lines(func() { r.setCLI(`{"cliUnlimited":true}`) }); !strings.Contains(got, "Command line window: opened with no time limit.") {
		t.Fatalf("unlimited: %q", got)
	}
	if got := lines(func() { r.setCLI(`{"keepResults":true}`) }); strings.Contains(got, "Command line window") {
		t.Fatalf("an unrelated setting was recorded as a window change: %q", got)
	}
	if got := lines(func() { r.setCLI(`{"cliUnlimited":false}`) }); !strings.Contains(got, "Command line window: closed.") {
		t.Fatalf("close: %q", got)
	}
	if got := lines(func() { r.setCLI(`{"cliMinutes":0}`) }); strings.Contains(got, "Command line window") {
		t.Fatalf("closing a shut window was recorded: %q", got)
	}
	r.setCLI(`{"cliUnlimited":true}`)
	if got := lines(func() { r.setCLI(`{"enabled":false}`) }); !strings.Contains(got, "Command line window: closed because code mode is off.") {
		t.Fatalf("code mode off: %q", got)
	}
	// A refused request is not recorded as a change.
	r.setCLI(`{"enabled":true}`)
	if got := lines(func() { r.setCLI(`{"cliUnlimited":true,"cliMinutes":5}`) }); strings.Contains(got, "Command line window") {
		t.Fatalf("a refused request was recorded: %q", got)
	}
}

// Shutting the command line needs no disk. When the settings cannot be read or saved (here: a folder stands where code-mode.json
// should be, so the save is refused), a request that asks to close still closes, and says so. A request to open does not open.
func TestACloseRequestStillClosesWhenTheSettingsCannotBeSaved(t *testing.T) {
	for _, body := range []string{`{"cliUnlimited":false}`, `{"enabled":false}`, `{"cliMinutes":0}`, `{"cliMinutes":10}`, `{"cliUnlimited":false,"cliMinutes":5}`} {
		dir := t.TempDir()
		r := newRig(t, rigOpts{spawner: &spawn{}, cliShut: true, settingsIn: dir})
		r.enable()
		if res := r.setCLI(`{"cliUnlimited":true}`); res.Code != http.StatusOK || !r.gate.Unlimited() {
			t.Fatalf("%s: not unlimited: %d", body, res.Code)
		}
		path := filepath.Join(dir, settingsfile.FileName)
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		res := r.setCLI(body)
		if res.Code != http.StatusInternalServerError {
			t.Fatalf("%s = %d %s, want 500", body, res.Code, res.Body)
		}
		if !strings.Contains(res.Body.String(), "command line was shut") {
			t.Errorf("%s: the reply does not say the command line was shut: %s", body, res.Body)
		}
		if r.gate.Unlimited() || r.gate.Allows() {
			t.Fatalf("%s: a failed save left the command line open", body)
		}
	}
	// An open request that cannot be saved does not open anything.
	dir := t.TempDir()
	r := newRig(t, rigOpts{spawner: &spawn{}, cliShut: true, settingsIn: dir})
	r.enable()
	path := filepath.Join(dir, settingsfile.FileName)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"cliUnlimited":true}`, `{"cliMinutes":10}`} {
		if res := r.setCLI(body); res.Code != http.StatusInternalServerError || r.gate.Allows() {
			t.Fatalf("%s = %d, open %v", body, res.Code, r.gate.Allows())
		}
	}
}
