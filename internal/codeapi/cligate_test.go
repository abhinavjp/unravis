package codeapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"api-audit-explorer/internal/clisrv"
	"api-audit-explorer/internal/codemode"
	"api-audit-explorer/internal/codemode/settingsfile"
)

// BL-371: the command line may run scripts only while the person in the app has opened a window for it. These tests drive the real broker,
// the real routes and a real sealed case store; the clock of the window is a fake one.

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

const cliAgent = "unravis-cli/1.0 (windows)"

func (r *rig) setCLI(body string) *httptest.ResponseRecorder {
	r.t.Helper()
	return r.call(http.MethodPut, "/api/code/settings", body)
}

func (r *rig) settings() settingsDTO {
	r.t.Helper()
	res := r.call(http.MethodGet, "/api/code/settings", "")
	var d settingsDTO
	if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &d) != nil {
		r.t.Fatalf("settings = %d %s", res.Code, res.Body)
	}
	return d
}

// A plain client of the owner pipe: the same call the program "unravis code run" makes, with nothing but the code. It is refused
// until the app opens the window, and the refusal is recorded nowhere.
func TestCLIIsRefusedWhileTheWindowIsClosed(t *testing.T) {
	r := newRig(t, rigOpts{spawner: &spawn{}, cliShut: true})
	id := r.newCase("From the console")
	r.enable()
	c := cliOf(t, r)

	_, err := c.Run(context.Background(), clisrv.CodeRunInput{Code: "1+1", Reason: "x"})
	if got := refusalOf(t, err); got != codemode.CodeCLIClosed {
		t.Fatalf("closed window: %q", got)
	}
	if n := len(r.runsOf(id)); n != 0 {
		t.Fatalf("a refused run was recorded: %d", n)
	}
	// Whatever the caller asks for, it is the same answer: naming a case or asking for identifiers does not get past the gate or leak more.
	for _, in := range []clisrv.CodeRunInput{{Code: "1", CaseID: id}, {Code: "1", AllowIdentifiers: true}, {Code: "1", TimeoutMS: 1}} {
		if _, err := c.Run(context.Background(), in); refusalOf(t, err) != codemode.CodeCLIClosed {
			t.Fatalf("%+v: %v", in, err)
		}
	}
	if len(r.runsOf(id)) != 0 || r.spawnerStarts() != 0 {
		t.Fatalf("a refused run started something: runs=%d starts=%d", len(r.runsOf(id)), r.spawnerStarts())
	}
	// The same program over the route (its User-Agent says it is the command line) is held by the same gate.
	res := r.call(http.MethodPost, "/api/code/run", runBodyOf("1+1"), "User-Agent", cliAgent)
	wantProblem(t, res, http.StatusForbidden, codemode.CodeCLIClosed)
	if len(r.runsOf(id)) != 0 {
		t.Fatal("the route recorded a refused run")
	}
}

func (r *rig) spawnerStarts() int32 {
	if s, ok := r.spawnerUsed.(*spawn); ok {
		return s.starts.Load()
	}
	return 0
}

// Code mode being off is the first answer; the window does not change it, and an open window does not switch code mode on.
func TestTheWindowDoesNotSwitchCodeModeOn(t *testing.T) {
	r := newRig(t, rigOpts{spawner: &spawn{}, cliShut: true})
	r.newCase("A")
	if res := r.setCLI(`{"cliMinutes":5}`); res.Code != http.StatusOK {
		t.Fatalf("open = %d %s", res.Code, res.Body)
	}
	_, err := cliOf(t, r).Run(context.Background(), clisrv.CodeRunInput{Code: "1"})
	if got := refusalOf(t, err); got != codemode.CodeModeOff {
		t.Fatalf("window open, switch off: %q", got)
	}
}

func TestOpeningTheWindowFromTheSettingsLetsTheCLIRunUntilItEnds(t *testing.T) {
	r := newRig(t, rigOpts{spawner: &spawn{}, cliShut: true})
	id := r.newCase("A")
	r.enable()
	if s := r.settings(); s.CLIOpenUntil != "" {
		t.Fatalf("a new app reports an open window: %q", s.CLIOpenUntil)
	}
	res := r.setCLI(`{"cliMinutes":15}`)
	if res.Code != http.StatusOK {
		t.Fatalf("open = %d %s", res.Code, res.Body)
	}
	want := r.clock.now().Add(15 * time.Minute).UTC().Format(time.RFC3339)
	if got := r.settings().CLIOpenUntil; got != want {
		t.Fatalf("cliOpenUntil = %q, want %q", got, want)
	}
	c := cliOf(t, r)
	// Open: the run is accepted (it ends as "no safe runner" in this build, which is not the gate) and is recorded as the command line's.
	if _, err := c.Run(context.Background(), clisrv.CodeRunInput{Code: "1+1", Reason: "x"}); err != nil {
		t.Fatalf("open window: %v", err)
	}
	if runs := r.runsOf(id); len(runs) != 1 || runs[0].Surface != "cli" {
		t.Fatalf("runs: %+v", runs)
	}
	// The end of the window shuts it again, with nothing done by anyone.
	r.clock.add(15 * time.Minute)
	if _, err := c.Run(context.Background(), clisrv.CodeRunInput{Code: "1"}); refusalOf(t, err) != codemode.CodeCLIClosed {
		t.Fatalf("after the end: %v", err)
	}
	if s := r.settings(); s.CLIOpenUntil != "" {
		t.Fatalf("an ended window is still reported: %q", s.CLIOpenUntil)
	}
	if len(r.runsOf(id)) != 1 {
		t.Fatal("the refused run was recorded")
	}
}

func TestClosingTheWindowAndSwitchingCodeModeOff(t *testing.T) {
	r := newRig(t, rigOpts{spawner: &spawn{}, cliShut: true})
	r.newCase("A")
	r.enable()
	c := cliOf(t, r)

	r.setCLI(`{"cliMinutes":30}`)
	if r.settings().CLIOpenUntil == "" {
		t.Fatal("not open")
	}
	// 0 closes it at once.
	if res := r.setCLI(`{"cliMinutes":0}`); res.Code != http.StatusOK || r.settings().CLIOpenUntil != "" {
		t.Fatalf("close = %d %s", res.Code, res.Body)
	}
	if _, err := c.Run(context.Background(), clisrv.CodeRunInput{Code: "1"}); refusalOf(t, err) != codemode.CodeCLIClosed {
		t.Fatalf("after close: %v", err)
	}
	// Switching code mode off closes an open window, so the next time it is switched on the command line starts shut.
	r.setCLI(`{"cliMinutes":30}`)
	r.setCLI(`{"enabled":false}`)
	r.setCLI(`{"enabled":true}`)
	if r.settings().CLIOpenUntil != "" {
		t.Fatal("the window survived switching code mode off")
	}
	// Opening twice replaces the end, it does not add to it.
	r.setCLI(`{"cliMinutes":10}`)
	r.clock.add(4 * time.Minute)
	r.setCLI(`{"cliMinutes":10}`)
	if got, want := r.settings().CLIOpenUntil, r.clock.now().Add(10*time.Minute).UTC().Format(time.RFC3339); got != want {
		t.Fatalf("second opening: %q, want %q", got, want)
	}
	// Another setting changed with no cliMinutes leaves the window as it is.
	r.setCLI(`{"keepResults":true}`)
	if r.settings().CLIOpenUntil == "" {
		t.Fatal("an unrelated setting closed the window")
	}
}

func TestTheWindowLengthIsValidated(t *testing.T) {
	r := newRig(t, rigOpts{spawner: &spawn{}, cliShut: true})
	r.enable()
	for _, body := range []string{`{"cliMinutes":-1}`, `{"cliMinutes":61}`, `{"cliMinutes":100000}`, `{"cliMinutes":1.5}`, `{"cliMinutes":"10"}`} {
		res := r.setCLI(body)
		if res.Code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", body, res.Code)
		}
	}
	if r.settings().CLIOpenUntil != "" {
		t.Fatal("a refused length opened the window")
	}
	for _, min := range []string{"1", "60"} {
		if res := r.setCLI(`{"cliMinutes":` + min + `}`); res.Code != http.StatusOK {
			t.Errorf("%s = %d %s", min, res.Code, res.Body)
		}
	}
	// A refused length does not change the other settings in the same request.
	before := r.settings()
	if res := r.setCLI(`{"keepResults":true,"cliMinutes":99}`); res.Code != http.StatusBadRequest {
		t.Fatalf("mixed = %d", res.Code)
	}
	if after := r.settings(); after.KeepResults != before.KeepResults {
		t.Fatal("a refused request changed another setting")
	}
}

// The window is memory only: nothing about it is in code-mode.json, so no file edit can open it and an app start finds it shut.
func TestTheWindowIsNeverWrittenToDisk(t *testing.T) {
	dir := t.TempDir()
	r := newRig(t, rigOpts{spawner: &spawn{}, cliShut: true, settingsIn: dir})
	r.enable()
	r.setCLI(`{"cliMinutes":30}`)
	b, err := os.ReadFile(filepath.Join(dir, settingsfile.FileName)) //nolint:gosec // a test file in the test's own temp folder
	if err != nil {
		t.Fatal(err)
	}
	low := strings.ToLower(string(b))
	if strings.Contains(low, "cli") || strings.Contains(low, "minutes") || strings.Contains(low, "until") {
		t.Fatalf("the window is in the settings file: %s", b)
	}
	// An app that starts from the same folder starts with the window shut, though code mode is still on.
	r2 := newRig(t, rigOpts{spawner: &spawn{}, cliShut: true, settingsIn: dir})
	if r2.settings().CLIOpenUntil != "" {
		t.Fatal("a new app starts with the window open")
	}
	// Writing a window into the file by hand does nothing: unknown keys are not read as one.
	if err := os.WriteFile(filepath.Join(dir, settingsfile.FileName), []byte(`{"enabled":true,"cliMinutes":30,"cliOpenUntil":"2999-01-01T00:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	r3 := newRig(t, rigOpts{spawner: &spawn{}, cliShut: true, settingsIn: dir})
	r3.newCase("A")
	if _, err := cliOf(t, r3).Run(context.Background(), clisrv.CodeRunInput{Code: "1"}); refusalOf(t, err) == "" {
		t.Fatal("a hand-edited file opened the window")
	}
	if r3.settings().CLIOpenUntil != "" {
		t.Fatal("a hand-edited file opened the window")
	}
}

// The app's own page is not held by the gate: the same route without the command line's User-Agent runs while the window is closed.
func TestTheAppsOwnPageIsNotHeldByTheWindow(t *testing.T) {
	r := newRig(t, rigOpts{spawner: &spawn{}, cliShut: true})
	id := r.newCase("A")
	r.enable()
	res := r.call(http.MethodPost, "/api/code/run", runBodyOf("1+1"))
	if res.Code == http.StatusForbidden && strings.Contains(res.Body.String(), codemode.CodeCLIClosed) {
		t.Fatalf("the page was held by the command-line gate: %s", res.Body)
	}
	runs := r.runsOf(id)
	if len(runs) != 1 || runs[0].Surface != "ui" {
		t.Fatalf("runs: %+v", runs)
	}
}

// Built without a gate (the window cannot be opened), the settings refuse cliMinutes and everything else still works.
func TestSettingsRefuseTheWindowWithoutAGate(t *testing.T) {
	r := newRig(t, rigOpts{spawner: &spawn{}, noGate: true})
	r.enable()
	if res := r.setCLI(`{"cliMinutes":5}`); res.Code != http.StatusBadRequest {
		t.Fatalf("no gate = %d", res.Code)
	}
	if res := r.setCLI(`{"keepResults":true}`); res.Code != http.StatusOK {
		t.Fatalf("other settings = %d", res.Code)
	}
	if r.settings().CLIOpenUntil != "" {
		t.Fatal("reported a window with no gate")
	}
}
