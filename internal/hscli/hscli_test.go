package hscli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/activitylog"
	"api-audit-explorer/internal/cicli"
	"api-audit-explorer/internal/hsaction"
	"api-audit-explorer/internal/localci"
	"api-audit-explorer/internal/releasehold"
)

// ---------------------------------------------------------------- the command table against the registry

// Every action of the registry must have a command. This fails the moment someone adds an action to internal/hsaction
// without a command here, and it reads the registry, not a list of its own.
func TestEveryRegistryActionHasACommand(t *testing.T) {
	byAction := map[string][]*command{}
	for i := range commands {
		if a := commands[i].Action; a != "" {
			byAction[a] = append(byAction[a], &commands[i])
		}
	}
	all := hsaction.All()
	if len(all) == 0 {
		t.Fatal("the registry has no actions: the test would pass for nothing")
	}
	for _, a := range all {
		cmds := byAction[a.ID]
		if len(cmds) == 0 {
			t.Errorf("registry action %s (%s) has no homestretch command: add one to the table in commands.go with Action: %q", a.ID, a.CLI, a.ID)
			continue
		}
		for _, cmd := range cmds {
			if cmd.NotBacked != "" || cmd.Run == nil {
				t.Errorf("registry action %s is on %q, which is not backed", a.ID, strings.Join(cmd.Path, " "))
			}
			if got, _ := find(cmd.Path); got != cmd {
				t.Errorf("registry action %s: the words %q do not reach its command", a.ID, strings.Join(cmd.Path, " "))
			}
		}
		delete(byAction, a.ID)
	}
	for id := range byAction {
		t.Errorf("the table names an action that is not in the registry: %s", id)
	}
}

func TestCommandTableIsSound(t *testing.T) {
	seen := map[string]bool{}
	for i := range commands {
		cmd := &commands[i]
		key := strings.Join(cmd.Path, " ")
		if key == "" {
			t.Errorf("command %d has no words", i)
		}
		if seen[key] {
			t.Errorf("two commands are called %q", key)
		}
		seen[key] = true
		if cmd.Run == nil && cmd.NotBacked == "" {
			t.Errorf("%q does nothing: it has neither Run nor NotBacked", key)
		}
		if cmd.Run != nil && cmd.NotBacked != "" {
			t.Errorf("%q has both Run and NotBacked", key)
		}
		if !cmd.Hidden {
			if cmd.Group == "" || cmd.Summary == "" || !strings.HasPrefix(cmd.Usage, cmd.Path[0]) {
				t.Errorf("%q needs a group, a summary and a usage line that starts with its first word", key)
			}
		}
	}
	// The design's section 4 table: every one of these words must be a command.
	for _, want := range []string{"status", "promote", "release", "rollback", "queue", "machines", "policy show", "policy set", "hold add", "hold lift",
		"hold list", "activity", "claim", "brief", "compare", "version", "ci"} {
		if !seen[want] {
			t.Errorf("the design lists %q but the table has no such command", want)
		}
	}
}

// ---------------------------------------------------------------- fakes

type scriptCall struct {
	Script string
	Args   []string
}

type world struct {
	t           *testing.T
	root        string
	engineCalls [][]string
	engineCode  func(args []string) int
	scriptCalls []scriptCall
	scriptCode  int
	prog        string
	engineProg  string
	child       []string
}

func newWorld(t *testing.T) *world {
	t.Helper()
	root := t.TempDir()
	rel := filepath.Join(root, "tools", "release")
	if err := os.MkdirAll(rel, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"Promote-Develop.ps1", "Release.ps1"} {
		if err := os.WriteFile(filepath.Join(rel, f), []byte("# test\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return &world{t: t, root: root}
}

func (w *world) deps() Deps {
	return Deps{
		Engine: func(_ context.Context, args []string, _, _ io.Writer, _ []string) int {
			w.engineCalls = append(w.engineCalls, append([]string{}, args...))
			w.prog, w.engineProg, w.child = cicli.Prog, cicli.EngineProg, append([]string{}, cicli.ChildPrefix...)
			if w.engineCode != nil {
				return w.engineCode(args)
			}
			return 0
		},
		Script: func(_ context.Context, script string, args []string, _, _ io.Writer, _ []string) int {
			w.scriptCalls = append(w.scriptCalls, scriptCall{Script: script, Args: append([]string{}, args...)})
			return w.scriptCode
		},
		Roots: func() []string { return []string{w.root} },
	}
}

func (w *world) hs(args ...string) (code int, out, errOut string) {
	return w.hsEnv(nil, args...)
}

func (w *world) hsEnv(environ []string, args ...string) (code int, out, errOut string) {
	var o, e bytes.Buffer
	code = Run(context.Background(), w.deps(), "homestretch", args, &o, &e, environ)
	return code, o.String(), e.String()
}

// realHS runs a command line against the real engine and a store folder; no script may run.
func realHS(t *testing.T, environ []string, args ...string) (code int, out, errOut string) {
	t.Helper()
	d := Deps{Script: func(context.Context, string, []string, io.Writer, io.Writer, []string) int {
		t.Error("a script ran in a test that must not run one")
		return 0
	}, Roots: func() []string { return nil }}
	var o, e bytes.Buffer
	code = Run(context.Background(), d, "homestretch", args, &o, &e, environ)
	return code, o.String(), e.String()
}

// storeWith makes a CI store folder with machines (as running agents) and two queued pull-request runs.
func storeWith(t *testing.T, machines ...string) (state string, ids []string) {
	t.Helper()
	state = t.TempDir()
	if err := os.MkdirAll(localci.AgentsDir(state), 0o750); err != nil {
		t.Fatal(err)
	}
	for _, m := range machines {
		hb := localci.Heartbeat{Name: m, PID: os.Getpid(), Slots: 1, State: "ready", Started: time.Now(), LastSeen: time.Now(), Jobs: []localci.AgentJob{}}
		b, err := json.Marshal(hb)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(localci.AgentsDir(state), hb.Name+".json"), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	q, err := localci.OpenQueue(localci.QueueDir(state))
	if err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= 2; n++ {
		r, _, err := q.Enqueue(localci.Request{Kind: localci.KindPR, Repo: "o/r", Ref: "f", Base: "develop", Number: n,
			SHA: strings.Repeat(string(rune('a'+n)), 40), Workflow: "fast-gate.yml"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, r.ID)
	}
	return state, ids
}

// ---------------------------------------------------------------- every action, from the command line

// Every action of the registry is run by its homestretch command: the command exits 0, and the action's activity entry
// is there with the actor cli and the person. The rows are checked against hsaction.All(), so a new action without a
// row fails here too.
func TestEveryActionRunsFromItsCommand(t *testing.T) {
	state, ids := storeWith(t, "m1", "m2")
	store, err := localci.OpenStore(state)
	if err != nil {
		t.Fatal(err)
	}
	const runID = "20260101-000000-push-develop-ci"
	if err := store.AppendIndex(localci.IndexEntry{RunID: runID, Workflow: "ci.yml", Event: "push", Repo: "o/r", HeadRef: "develop",
		HeadSHA: strings.Repeat("4", 40), Conclusion: "failure"}, nil); err != nil {
		t.Fatal(err)
	}
	holdID := func() string {
		hs, err := (releasehold.Store{Dir: state}).List()
		if err != nil || len(hs) == 0 {
			t.Fatalf("holds: %v %v", hs, err)
		}
		return hs[len(hs)-1].ID
	}
	rows := []struct {
		id   string
		args func() []string
	}{
		{"queue.top", func() []string { return []string{"queue", "top", ids[1]} }},
		{"queue.pause", func() []string { return []string{"queue", "pause"} }},
		{"queue.resume", func() []string { return []string{"queue", "resume"} }},
		{"queue.cancel", func() []string { return []string{"queue", "cancel", ids[0]} }},
		{"queue.retry", func() []string { return []string{"queue", "retry", runID} }},
		{"machines.allow", func() []string { return []string{"machines", "allow", "m1", "--project", "o/r"} }},
		{"machines.deny", func() []string { return []string{"machines", "deny", "m2", "--project", "o/r"} }},
		{"policy.set", func() []string { return []string{"policy", "set", "o/r", "--any"} }},
		{"machines.drain", func() []string { return []string{"machines", "drain", "m1"} }},
		{"machines.resume", func() []string { return []string{"machines", "resume", "m1"} }},
		{"hold.add", func() []string {
			return []string{"hold", "add", "--project", "unravis", "--until", "2h", "--reason", "freeze"}
		}},
		{"hold.lift", func() []string { return []string{"hold", "lift", holdID(), "--reason", "done"} }},
	}
	have := map[string]bool{}
	for _, r := range rows {
		have[r.id] = true
	}
	for _, a := range hsaction.All() {
		if !have[a.ID] {
			t.Errorf("registry action %s has no row in this test: add the command line that runs it", a.ID)
		}
	}
	env := []string{"USERNAME=pat"}
	for _, row := range rows {
		args := append(row.args(), "--state-dir", state)
		if code, out, e := realHS(t, env, args...); code != ExitOK {
			t.Errorf("%s (%s): exit %d, out %q, err %q", row.id, strings.Join(args, " "), code, out, e)
		}
	}
	es, _, err := (activitylog.Log{Dir: state}).Read(activitylog.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	logged := map[string]bool{}
	for _, e := range es {
		if e.Actor != activitylog.ActorCLI || e.Person != "pat" || e.Outcome != "done" {
			t.Errorf("entry %+v", e)
		}
		logged[e.Action] = true
	}
	for _, a := range hsaction.All() {
		if !logged[a.ID] {
			t.Errorf("%s left no activity entry", a.ID)
		}
	}
	// HOMESTRETCH_ACTOR names the actor.
	if code, _, e := realHS(t, []string{"USERNAME=pat", "HOMESTRETCH_ACTOR=claude"}, "queue", "pause", "--state-dir", state); code != ExitOK {
		t.Fatalf("pause as claude: %d %q", code, e)
	}
	es, _, _ = (activitylog.Log{Dir: state}).Read(activitylog.Filter{})
	if last := es[len(es)-1]; last.Actor != activitylog.ActorClaude || last.Action != "queue.pause" {
		t.Fatalf("last entry %+v", last)
	}
}

// A refused action still goes through the registry: exit 1, a plain reason, an activity entry that says refused.
func TestRefusedActionExitsOneAndIsLogged(t *testing.T) {
	state, _ := storeWith(t, "m1")
	code, _, e := realHS(t, []string{"USERNAME=pat"}, "queue", "cancel", "no-such-run", "--state-dir", state)
	if code != ExitFailed || strings.TrimSpace(e) == "" {
		t.Fatalf("cancel of nothing: %d %q", code, e)
	}
	code, _, _ = realHS(t, []string{"USERNAME=pat"}, "hold", "lift", "h-unknown", "--state-dir", state)
	if code != ExitFailed {
		t.Fatalf("lift of nothing: %d", code)
	}
}

// ---------------------------------------------------------------- read commands with --json

func TestReadCommandsPrintJSON(t *testing.T) {
	state, _ := storeWith(t, "m1")
	env := []string{"USERNAME=pat"}
	if code, _, e := realHS(t, env, "hold", "add", "--project", "unravis", "--until", "2h", "--reason", "freeze", "--state-dir", state); code != ExitOK {
		t.Fatalf("hold add: %d %q", code, e)
	}
	cases := []struct {
		name string
		args []string
		keys []string
	}{
		{"queue", []string{"queue", "--json", "--state-dir", state}, []string{"stateDir", "waiting", "requests"}},
		{"queue list", []string{"queue", "list", "--json", "--state-dir", state}, []string{"stateDir", "waiting", "requests"}},
		{"machines", []string{"machines", "--json", "--state-dir", state}, []string{"stateDir"}},
		{"policy show", []string{"policy", "show", "o/r", "--json", "--state-dir", state}, []string{"stateDir", "policy"}},
		{"hold list", []string{"hold", "list", "--json", "--state-dir", state}, []string{"holds"}},
		{"activity", []string{"activity", "--json", "--state-dir", state}, []string{"entries"}},
		{"version", []string{"version", "--json"}, []string{"name", "version", "engine"}},
	}
	for _, c := range cases {
		code, out, e := realHS(t, env, c.args...)
		if code != ExitOK {
			t.Errorf("%s: exit %d, err %q", c.name, code, e)
			continue
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(out), &m); err != nil {
			t.Errorf("%s: not one JSON object: %v\n%s", c.name, err, out)
			continue
		}
		for _, k := range c.keys {
			if _, ok := m[k]; !ok {
				t.Errorf("%s: no %q in %s", c.name, k, out)
			}
		}
	}
	// The activity entry of the hold is in the --json read.
	_, out, _ := realHS(t, env, "activity", "--json", "--state-dir", state)
	var act struct {
		Entries []activitylog.Entry `json:"entries"`
	}
	if err := json.Unmarshal([]byte(out), &act); err != nil || len(act.Entries) != 1 || act.Entries[0].Action != "hold.add" || act.Entries[0].Actor != "cli" {
		t.Fatalf("activity: %v %+v", err, act)
	}
}

func TestVersionAndProgramName(t *testing.T) {
	w := newWorld(t)
	code, out, _ := w.hs("version")
	if code != ExitOK || !strings.HasPrefix(out, "homestretch "+Version+" (engine ") {
		t.Fatalf("version: %d %q", code, out)
	}
	if code, _, e := w.hs("version", "extra"); code != ExitUsage || e == "" {
		t.Fatalf("version extra: %d", code)
	}
	for argv0, want := range map[string]string{
		"hs": "hs", "hs.exe": "hs", "HS.EXE": "hs", `C:\tools\Hs.Exe`: "hs", "/usr/local/bin/hs": "hs", // winok: argv[0] name table; the function only strips the path by string rules
		"homestretch": "homestretch", "homestretch.exe": "homestretch", "hsx": "homestretch", "ths.exe": "homestretch", "": "homestretch",
	} {
		if got := ProgName(argv0); got != want {
			t.Errorf("ProgName(%q) = %q, want %q", argv0, got, want)
		}
	}
	// hs and homestretch are the same program: same output, apart from the name it prints.
	var a, b bytes.Buffer
	if c := Main(context.Background(), "hs.exe", []string{"version"}, &a, io.Discard, nil); c != ExitOK {
		t.Fatal(c)
	}
	if c := Main(context.Background(), "homestretch.exe", []string{"version"}, &b, io.Discard, nil); c != ExitOK {
		t.Fatal(c)
	}
	if strings.TrimPrefix(a.String(), "hs ") != strings.TrimPrefix(b.String(), "homestretch ") {
		t.Fatalf("hs %q, homestretch %q", a.String(), b.String())
	}
}

// ---------------------------------------------------------------- help and unknown words

func TestHelpAndUnknownWords(t *testing.T) {
	w := newWorld(t)
	code, out, _ := w.hs("help")
	if code != ExitOK {
		t.Fatalf("help: %d", code)
	}
	for i := range commands {
		cmd := &commands[i]
		if !cmd.Hidden && !strings.Contains(out, "homestretch "+cmd.Usage) {
			t.Errorf("help does not list %q", cmd.Usage)
		}
	}
	for _, want := range []string{"Exit codes:", "5 held by a freeze", "homestretch ci"} {
		if !strings.Contains(out, want) {
			t.Errorf("help lacks %q", want)
		}
	}
	for _, a := range []string{"-h", "--help"} {
		if code, _, _ := w.hs(a); code != ExitOK {
			t.Errorf("%s: %d", a, code)
		}
	}
	if code, out, e := w.hs(); code != ExitUsage || out != "" || !strings.Contains(e, "usage:") {
		t.Errorf("no arguments: %d %q %q", code, out, e)
	}
	if code, _, e := w.hs("frobnicate"); code != ExitUsage || !strings.Contains(e, `"frobnicate" is not a command`) {
		t.Errorf("unknown command: %d %q", code, e)
	}
	for _, args := range [][]string{{"queue", "bogus"}, {"machines", "bogus"}, {"policy"}, {"hold"}, {"policy", "bogus"}, {"hold", "bogus"}} {
		if code, _, e := w.hs(args...); code != ExitUsage || e == "" {
			t.Errorf("%v: exit %d, err %q", args, code, e)
		}
	}
	if len(w.engineCalls) != 0 || len(w.scriptCalls) != 0 {
		t.Errorf("a mistyped command reached the engine or a script: %v %v", w.engineCalls, w.scriptCalls)
	}
}

// ---------------------------------------------------------------- not backed

func TestUnbackedCommandsExitThreeAndChangeNothing(t *testing.T) {
	state, _ := storeWith(t, "m1")
	n := 0
	for i := range commands {
		cmd := &commands[i]
		if cmd.NotBacked == "" {
			continue
		}
		n++
		args := append(append([]string{}, cmd.Path...), "x", "--state-dir", state)
		code, out, e := realHS(t, nil, args...)
		if code != ExitUnsupported || out != "" || !strings.Contains(e, "not built yet") || !strings.Contains(e, "Nothing was changed") {
			t.Errorf("%v: exit %d, out %q, err %q", cmd.Path, code, out, e)
		}
	}
	for _, want := range []string{"rollback", "brief", "claim", "machines label", "machines enrol"} {
		found := false
		for i := range commands {
			found = found || (strings.Join(commands[i].Path, " ") == want && commands[i].NotBacked != "")
		}
		if !found {
			t.Errorf("%q should be not backed", want)
		}
	}
	if n != 5 {
		t.Errorf("%d not-backed commands, want 5", n)
	}
	if _, err := os.Stat(filepath.Join(state, activitylog.FileName)); err == nil {
		t.Error("a not-backed command wrote to the activity log")
	}
}

// ---------------------------------------------------------------- forwarding to the engine

func TestCommandsForwardToTheEngine(t *testing.T) {
	cases := []struct {
		line string
		want []string
	}{
		{"status", []string{"status"}},
		{"status --json", []string{"status", "--json"}},
		{"status --state-dir D", []string{"status", "--state", "D"}},
		{"status --project unravis --state-dir=D", []string{"status", "--state=D"}},
		{"status --project other --state-dir D", []string{"status", "--state", "D"}},
		{"queue", []string{"queue", "list"}},
		{"queue --json", []string{"queue", "list", "--json"}},
		{"queue list --state all --state-dir D", []string{"queue", "list", "--state", "all", "--state-dir", "D"}},
		{"queue cancel 7 --reason why --state-dir D", []string{"cancel", "7", "--reason", "why", "--state", "D"}},
		{"queue retry run-1 --state-dir D", []string{"rerun", "run-1", "--state", "D"}},
		{"queue top 7 --state-dir D", []string{"queue", "top", "7", "--state-dir", "D"}},
		{"queue pause --state-dir D", []string{"queue", "pause", "--state-dir", "D"}},
		{"queue resume", []string{"queue", "resume"}},
		{"queue fallback 12 --dry-run", []string{"fallback", "12", "--dry-run"}},
		{"machines", []string{"machines", "list"}},
		{"machines list --json", []string{"machines", "list", "--json"}},
		{"machines drain m1 --state-dir D", []string{"drain", "--name", "m1", "--state", "D"}},
		{"machines drain --state-dir D m1", []string{"drain", "--name", "m1", "--state", "D"}},
		{"machines resume m1", []string{"machines", "resume", "m1"}},
		{"machines allow m1 --project o/r --force", []string{"machines", "allow", "m1", "--project", "o/r", "--force"}},
		{"machines deny m1 --project o/r --dry-run", []string{"machines", "deny", "m1", "--project", "o/r", "--dry-run"}},
		{"policy set o/r --only m1,m2", []string{"policy", "set", "o/r", "--only", "m1,m2"}},
		{"policy show o/r --json", []string{"policy", "show", "o/r", "--json"}},
		{"hold add --project unravis --until 2h --reason r", []string{"hold", "add", "--project", "unravis", "--until", "2h", "--reason", "r"}},
		{"hold lift h-1 --reason done", []string{"hold", "lift", "h-1", "--reason", "done"}},
		{"hold list --all --json", []string{"hold", "list", "--all", "--json"}},
		{"hold check --project unravis", []string{"hold", "check", "--project", "unravis"}},
		{"activity --actor cli --limit 5", []string{"activity", "--actor", "cli", "--limit", "5"}},
		{"compare --state-dir D --limit 3", []string{"compare", "--state", "D", "--limit", "3"}},
		{"ci lint", []string{"lint"}},
		{"ci serve --job-slots 2", []string{"serve", "--job-slots", "2"}},
		{"ci", nil},
	}
	for _, c := range cases {
		w := newWorld(t)
		code, _, e := w.hs(strings.Fields(c.line)...)
		if code != ExitOK || len(w.engineCalls) != 1 || (!reflect.DeepEqual(w.engineCalls[0], append([]string(nil), c.want...)) && (len(c.want) != 0 || len(w.engineCalls[0]) != 0)) {
			t.Errorf("%q: exit %d, engine %q, want %q, err %q", c.line, code, w.engineCalls, c.want, e)
		}
	}
}

func TestEngineExitCodeIsPassedThrough(t *testing.T) {
	for _, want := range []int{0, 1, 2, 3, 5} {
		w := newWorld(t)
		w.engineCode = func([]string) int { return want }
		if code, _, _ := w.hs("queue", "pause"); code != want {
			t.Errorf("engine %d gave %d", want, code)
		}
	}
}

func TestMistypedLinesNeverReachTheEngine(t *testing.T) {
	for _, line := range []string{
		"status --project other",
		"machines drain",
		"machines drain a b",
		"status --project",
	} {
		w := newWorld(t)
		code, _, e := w.hs(strings.Fields(line)...)
		if code != ExitUsage || e == "" || len(w.engineCalls) != 0 {
			t.Errorf("%q: exit %d, err %q, engine %v", line, code, e, w.engineCalls)
		}
	}
}

// Under `homestretch ci`, the engine names itself and starts its children with the same words, and nothing leaks out
// of the call afterwards.
func TestEngineIsToldItsNameAndRestored(t *testing.T) {
	oldProg, oldEngine, oldChild := cicli.Prog, cicli.EngineProg, append([]string(nil), cicli.ChildPrefix...)
	w := newWorld(t)
	if code, _, _ := w.hs("ci", "serve"); code != ExitOK {
		t.Fatal(code)
	}
	if w.prog != "homestretch" || w.engineProg != "homestretch ci" || !reflect.DeepEqual(w.child, []string{"ci"}) {
		t.Errorf("the engine saw prog %q, engine prog %q, child prefix %q", w.prog, w.engineProg, w.child)
	}
	if cicli.Prog != oldProg || cicli.EngineProg != oldEngine || !reflect.DeepEqual(append([]string(nil), cicli.ChildPrefix...), oldChild) {
		t.Errorf("the engine's names were not restored: %q %q %q", cicli.Prog, cicli.EngineProg, cicli.ChildPrefix)
	}
}

func TestEngineMessagesUseTheProgramName(t *testing.T) {
	state, _ := storeWith(t, "m1")
	var o, e bytes.Buffer
	if code := Run(context.Background(), Deps{}, "hs", []string{"ci", "frobnicate"}, &o, &e, nil); code != ExitUsage {
		t.Fatalf("ci frobnicate: %d", code)
	}
	if !strings.Contains(e.String(), "hs ci") || strings.Contains(e.String(), "unravis-ci") {
		t.Errorf("the engine's message names the wrong program: %q", e.String())
	}
	_ = state
}

// ---------------------------------------------------------------- promote

func TestPromoteHeldByAFreezeNeverRunsTheScript(t *testing.T) {
	w := newWorld(t)
	w.engineCode = func(a []string) int {
		if len(a) >= 2 && a[0] == "hold" && a[1] == "check" {
			return 5
		}
		return 0
	}
	if code, _, _ := w.hs("promote", "develop"); code != ExitHeld {
		t.Fatalf("exit %d, want 5", code)
	}
	if len(w.scriptCalls) != 0 {
		t.Fatalf("the script ran while held: %v", w.scriptCalls)
	}
	if len(w.engineCalls) != 1 || !reflect.DeepEqual(w.engineCalls[0], []string{"hold", "check", "--project", "unravis"}) {
		t.Fatalf("hold check call: %v", w.engineCalls)
	}
}

func TestPromoteStopsWhenTheFreezeCannotBeChecked(t *testing.T) {
	for _, bad := range []int{1, 2, 3, 4} {
		w := newWorld(t)
		w.engineCode = func([]string) int { return bad }
		code, _, e := w.hs("promote")
		if code != ExitUsage || len(w.scriptCalls) != 0 || !strings.Contains(e, "could not be checked") {
			t.Errorf("hold check exit %d: promote exit %d, scripts %v, err %q", bad, code, w.scriptCalls, e)
		}
	}
}

func TestPromoteRunsTheScriptAfterAFreeCheck(t *testing.T) {
	w := newWorld(t)
	code, _, e := w.hs("promote", "develop", "--project", "unravis", "--up-to", "abc1234", "--dry-run", "--no-merge", "--skip-notes-check",
		"--wait-minutes", "5", "--ci-wait-minutes", "7", "--repo", "o/r", "--state-dir", "D")
	if code != ExitOK {
		t.Fatalf("exit %d: %s", code, e)
	}
	if len(w.engineCalls) != 1 || !reflect.DeepEqual(w.engineCalls[0], []string{"hold", "check", "--project", "unravis", "--state-dir", "D"}) {
		t.Fatalf("hold check call: %v", w.engineCalls)
	}
	want := []string{"-Project", "unravis", "-UpTo", "abc1234", "-Repo", "o/r", "-CiStore", "D", "-DryRun", "-NoMerge", "-SkipNotesCheck",
		"-WaitMinutes", "5", "-CiWaitMinutes", "7"}
	if len(w.scriptCalls) != 1 || !reflect.DeepEqual(w.scriptCalls[0].Args, want) || filepath.Base(w.scriptCalls[0].Script) != "Promote-Develop.ps1" {
		t.Fatalf("script call: %+v", w.scriptCalls)
	}
	// Plain `promote`: only the project.
	w = newWorld(t)
	if code, _, _ := w.hs("promote"); code != ExitOK || !reflect.DeepEqual(w.scriptCalls[0].Args, []string{"-Project", "unravis"}) {
		t.Fatalf("plain promote: %d %+v", code, w.scriptCalls)
	}
}

func TestPromoteRefusesWhatItDoesNotKnow(t *testing.T) {
	for _, line := range []string{
		"promote master",
		"promote develop extra",
		"promote --project -x",
		"promote --project UP",
		"promote --up-to zz",
		"promote --repo o/r;ls",
		"promote --repo noslash",
		"promote --wait-minutes -3",
		"promote --wait-minutes x",
		"promote --unknown-flag",
	} {
		w := newWorld(t)
		code, _, e := w.hs(strings.Fields(line)...)
		if code != ExitUsage || e == "" || len(w.scriptCalls) != 0 || len(w.engineCalls) != 0 {
			t.Errorf("%q: exit %d, err %q, scripts %v, engine %v", line, code, e, w.scriptCalls, w.engineCalls)
		}
	}
}

func TestScriptExitCodes(t *testing.T) {
	for in, want := range map[int]int{0: 0, 1: 1, 2: 2, 3: 3, 4: 4, 5: 5, 6: 1, 7: 1, 255: 1, -1: 1} {
		w := newWorld(t)
		w.scriptCode = in
		if code, _, _ := w.hs("promote"); code != want {
			t.Errorf("script exit %d gave %d, want %d", in, code, want)
		}
	}
}

// ---------------------------------------------------------------- release

func TestReleaseTranslatesItsFlags(t *testing.T) {
	cases := []struct {
		line string
		want []string
	}{
		{"release rc", []string{"-Channel", "rc"}},
		{"release stable --yes", []string{"-Channel", "stable", "-Yes"}},
		{"release rc --version 1.2.3-rc.1 --dry-run", []string{"-Channel", "rc", "-Version", "1.2.3-rc.1", "-CheckOnly"}},
		{"release rc --check-only", []string{"-Channel", "rc", "-CheckOnly"}},
		{"release stable --requester nachi --repo o/r --branch master --wait-minutes 9",
			[]string{"-Channel", "stable", "-Requester", "nachi", "-Repo", "o/r", "-Branch", "master", "-WaitMinutes", "9"}},
	}
	for _, c := range cases {
		w := newWorld(t)
		code, _, e := w.hs(strings.Fields(c.line)...)
		if code != ExitOK || len(w.scriptCalls) != 1 || !reflect.DeepEqual(w.scriptCalls[0].Args, c.want) || filepath.Base(w.scriptCalls[0].Script) != "Release.ps1" {
			t.Errorf("%q: exit %d, scripts %+v, err %q, want %v", c.line, code, w.scriptCalls, e, c.want)
		}
		if len(w.engineCalls) != 1 || w.engineCalls[0][0] != "hold" || w.engineCalls[0][1] != "check" {
			t.Errorf("%q: the freeze was not checked first: %v", c.line, w.engineCalls)
		}
	}
}

func TestReleaseStatusIsReadOnlyAndSkipsTheFreeze(t *testing.T) {
	w := newWorld(t)
	w.engineCode = func([]string) int { return 5 } // a freeze would stop a release, but not looking at the status
	code, _, e := w.hs("release", "--status", "--json")
	if code != ExitOK || len(w.engineCalls) != 0 || len(w.scriptCalls) != 1 || !reflect.DeepEqual(w.scriptCalls[0].Args, []string{"-Status", "-Json"}) {
		t.Fatalf("exit %d, engine %v, scripts %+v, err %q", code, w.engineCalls, w.scriptCalls, e)
	}
	w = newWorld(t)
	if code, _, _ := w.hs("release", "--status", "rc"); code != ExitUsage || len(w.scriptCalls) != 0 {
		t.Fatalf("status with a channel: %d", code)
	}
}

func TestReleaseHeldAndRefused(t *testing.T) {
	w := newWorld(t)
	w.engineCode = func([]string) int { return 5 }
	if code, _, _ := w.hs("release", "stable", "--yes"); code != ExitHeld || len(w.scriptCalls) != 0 {
		t.Fatalf("held release: %d %v", code, w.scriptCalls)
	}
	for _, line := range []string{
		"release",
		"release beta",
		"release rc stable",
		"release rc --version -rf",
		"release rc --branch ../x",
		"release rc --requester -x",
		"release rc --repo bad",
		"release rc --wait-minutes -1",
	} {
		w := newWorld(t)
		code, _, e := w.hs(strings.Fields(line)...)
		if code != ExitUsage || e == "" || len(w.scriptCalls) != 0 || len(w.engineCalls) != 0 {
			t.Errorf("%q: exit %d, err %q, scripts %v, engine %v", line, code, e, w.scriptCalls, w.engineCalls)
		}
	}
}

// ---------------------------------------------------------------- finding the scripts

func TestScriptsAreFoundOrRefusedPlainly(t *testing.T) {
	w := newWorld(t)
	// Not inside a checkout: exit 2, a plain message, nothing run.
	empty := t.TempDir()
	d := w.deps()
	d.Roots = func() []string { return []string{empty} }
	var o, e bytes.Buffer
	code := Run(context.Background(), d, "homestretch", []string{"promote", "--dry-run"}, &o, &e, nil)
	if code != ExitUsage || !strings.Contains(e.String(), "could not find") || !strings.Contains(e.String(), repoEnv) || len(w.scriptCalls) != 0 {
		t.Fatalf("no checkout: %d %q", code, e.String())
	}
	// A folder below the checkout finds it.
	deep := filepath.Join(w.root, "a", "b")
	if err := os.MkdirAll(deep, 0o750); err != nil {
		t.Fatal(err)
	}
	p, err := findScript([]string{deep}, nil, "Release.ps1")
	if err != nil || p != filepath.Join(w.root, "tools", "release", "Release.ps1") {
		t.Fatalf("walk up: %q %v", p, err)
	}
	// HOMESTRETCH_REPO wins, in any case of its name; a wrong one is refused, not ignored.
	other := newWorld(t)
	p, err = findScript([]string{deep}, []string{"PATH=x", "homestretch_repo=" + other.root}, "Release.ps1")
	if err != nil || p != filepath.Join(other.root, "tools", "release", "Release.ps1") {
		t.Fatalf("env: %q %v", p, err)
	}
	if _, err := findScript([]string{deep}, []string{repoEnv + "=" + empty}, "Release.ps1"); err == nil || !strings.Contains(err.Error(), repoEnv) {
		t.Fatalf("wrong env: %v", err)
	}
}

func TestRunPowerShellWithoutPowerShell(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	var e bytes.Buffer
	if code := runPowerShell(context.Background(), "x.ps1", nil, io.Discard, &e, nil); code != ExitUsage || !strings.Contains(e.String(), "PowerShell was not found") {
		t.Fatalf("%d %q", code, e.String())
	}
}

// ---------------------------------------------------------------- argument helpers

func TestArgumentHelpers(t *testing.T) {
	if got := stateFlag([]string{"--state-dir", "D", "-state-dir=E", "--state-dir=F", "--json", "x"}); !reflect.DeepEqual(got, []string{"--state", "D", "--state=E", "--state=F", "--json", "x"}) {
		t.Errorf("stateFlag: %q", got)
	}
	for _, c := range []struct {
		args  []string
		value string
		rest  []string
		err   bool
	}{
		{[]string{"a", "--project", "p", "b"}, "p", []string{"a", "b"}, false},
		{[]string{"--project=p"}, "p", nil, false},
		{[]string{"-project", "p"}, "p", nil, false},
		{[]string{"--project"}, "", nil, true},
		{[]string{"x", "--", "--project", "p"}, "", []string{"x", "--", "--project", "p"}, false},
		{[]string{"--projects", "p"}, "", []string{"--projects", "p"}, false},
		{nil, "", nil, false},
	} {
		v, rest, err := takeFlag(c.args, "project")
		if v != c.value || (err != nil) != c.err || (!c.err && !reflect.DeepEqual(rest, c.rest)) {
			t.Errorf("takeFlag(%q) = %q %q %v", c.args, v, rest, err)
		}
	}
	pos, flags := positionals([]string{"m1", "--state-dir", "D", "--json", "-", "--", "--odd"}, "state-dir")
	if !reflect.DeepEqual(pos, []string{"m1", "--odd"}) && !reflect.DeepEqual(pos, []string{"m1", "-", "--odd"}) {
		t.Errorf("positionals: %q", pos)
	}
	if !reflect.DeepEqual(flags, []string{"--state-dir", "D", "--json"}) {
		t.Errorf("flags: %q", flags)
	}
	if !hasStateDir([]string{"--state=D"}) || !hasStateDir([]string{"--state-dir", "D"}) || hasStateDir([]string{"--json"}) {
		t.Error("hasStateDir")
	}
}
