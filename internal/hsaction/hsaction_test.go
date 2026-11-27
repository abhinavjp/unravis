package hsaction

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/activitylog"
	"api-audit-explorer/internal/localci"
)

// store makes a CI store with live machines and two queued pull request runs of o/r.
func store(t *testing.T, machines ...string) (string, []string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(localci.AgentsDir(dir), 0o750); err != nil {
		t.Fatal(err)
	}
	for _, m := range machines {
		hb := localci.Heartbeat{Name: m, PID: os.Getpid(), Slots: 1, State: "ready", Started: time.Now(), LastSeen: time.Now(), Jobs: []localci.AgentJob{}}
		b, _ := json.Marshal(hb)
		if err := os.WriteFile(filepath.Join(localci.AgentsDir(dir), m+".json"), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	q, err := localci.OpenQueue(localci.QueueDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for n := 1; n <= 2; n++ {
		r, _, err := q.Enqueue(localci.Request{Kind: localci.KindPR, Repo: "o/r", Ref: "f", Base: "develop", Number: n,
			SHA: strings.Repeat(string(rune('a'+n)), 40), Workflow: "fast-gate.yml"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, r.ID)
	}
	return dir, ids
}

func entries(t *testing.T, dir string) []activitylog.Entry {
	t.Helper()
	es, _, err := (activitylog.Log{Dir: dir}).Read(activitylog.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	return es
}

func TestRegistryHasTheActionsAndNoPromoteOrRelease(t *testing.T) {
	want := []string{"hold.add", "hold.lift", "machines.allow", "machines.deny", "machines.drain", "machines.resume",
		"policy.set", "queue.cancel", "queue.pause", "queue.resume", "queue.retry", "queue.top"}
	var got []string
	for _, a := range All() {
		got = append(got, a.ID)
		if a.Summary == "" || a.CLI == "" {
			t.Errorf("%s: no summary or CLI text", a.ID)
		}
		if strings.Contains(a.ID, "promote") || strings.Contains(a.ID, "release") {
			t.Errorf("%s: no promote or release action belongs here", a.ID)
		}
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("actions %v, want %v", got, want)
	}
}

func TestDecodeIsStrict(t *testing.T) {
	if _, err := Decode("queue.top", []byte(`{"id":"x","extra":1}`)); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field: %v", err)
	}
	if _, err := Decode("queue.top", []byte(`{"id":"x"} {"id":"y"}`)); err == nil {
		t.Fatal("two values were accepted")
	}
	if _, err := Decode("queue.top", []byte(`{"pr":"seven"}`)); err == nil {
		t.Fatal("a string pr was accepted")
	}
	if _, err := Decode("nope", nil); err == nil {
		t.Fatal("an unknown id was accepted")
	}
	// A field the CLI alone may set cannot come in as JSON.
	if _, err := Decode("hold.lift", []byte(`{"id":"h-1","By":"mallory"}`)); err == nil {
		t.Fatal("hold.lift took By from JSON")
	}
	if _, err := Decode("queue.retry", []byte(`{"run":"1","Resolved":{}}`)); err == nil {
		t.Fatal("queue.retry took Resolved from JSON")
	}
	v, err := Decode("queue.pause", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := v.(*NoArgs); !ok {
		t.Fatalf("empty args gave %T", v)
	}
}

func TestStrandingPolicyEditIsRefusedWithoutConfirm(t *testing.T) {
	dir, ids := store(t, "m1", "m2")
	env := Env{Dir: dir, Actor: "ui", Person: "pat"}
	// A dry run measures and writes nothing, not even a log line.
	res := Run(env, "policy.set", &PolicySetArgs{Project: "o/r", Mode: "only", Machines: []string{"m9"}, DryRun: true})
	if res.Impact == nil || len(res.Impact.Stranded) != 2 {
		t.Fatalf("dry run impact: %+v", res)
	}
	if n := len(entries(t, dir)); n != 0 {
		t.Fatalf("a dry run wrote %d entries", n)
	}
	res = Run(env, "policy.set", &PolicySetArgs{Project: "o/r", Mode: "only", Machines: []string{"m9"}})
	if res.OK || res.Status != Refused || res.Status.HTTPStatus() != 409 || !strings.Contains(res.Message, "Confirm") {
		t.Fatalf("not refused: %+v", res)
	}
	for _, id := range ids {
		if !strings.Contains(strings.Join(res.Impact.Stranded, " "), id) {
			t.Errorf("stranded list misses %s: %v", id, res.Impact.Stranded)
		}
	}
	if p, err := localci.LoadPolicy(dir); err != nil || p.Projects["o/r"].Mode == localci.PolicyOnly {
		t.Fatal("the refused edit was written")
	}
	res = Run(env, "policy.set", &PolicySetArgs{Project: "o/r", Mode: "only", Machines: []string{"m9"}, Confirm: true})
	if !res.OK || !res.Changed {
		t.Fatalf("confirm did not apply: %+v", res)
	}
	es := entries(t, dir)
	if len(es) != 2 || es[0].Outcome != "refused" || es[1].Outcome != "done" || !strings.HasPrefix(es[1].Reason, "confirmed although") {
		t.Fatalf("entries: %+v", es)
	}
}

func TestEveryAttemptIsLoggedWithSecretsMasked(t *testing.T) {
	dir, _ := store(t, "m1")
	env := Env{Dir: dir, Actor: "cli", Person: "pat"}
	secret := "ghp_" + strings.Repeat("A", 36)
	res := Run(env, "queue.cancel", CancelArgs{Target: Target{PR: 77}, Reason: "token=" + secret})
	if res.OK || res.Status != Refused {
		t.Fatalf("cancel of a missing PR: %+v", res)
	}
	res = Run(env, "machines.drain", &MachineArgs{Name: "bad name"})
	if res.Status != Invalid || res.Field != "name" || res.Status.ExitCode() != 2 {
		t.Fatalf("bad name: %+v", res)
	}
	es := entries(t, dir)
	if len(es) != 2 {
		t.Fatalf("entries: %+v", es)
	}
	if es[0].Outcome != "refused" || strings.Contains(es[0].Reason, secret) || es[0].Actor != "cli" || es[0].Person != "pat" {
		t.Fatalf("refused cancel entry: %+v", es[0])
	}
	if es[1].Outcome != "invalid" || es[1].Detail == "" {
		t.Fatalf("invalid drain entry: %+v", es[1])
	}
}

func TestRunRefusesABadActorAndAMissingStore(t *testing.T) {
	if res := Run(Env{Dir: t.TempDir(), Actor: "robot"}, "queue.pause", NoArgs{}); res.OK || res.Status != Invalid {
		t.Fatalf("bad actor: %+v", res)
	}
	if res := Run(Env{Actor: "ui"}, "queue.pause", NoArgs{}); res.OK {
		t.Fatalf("no dir: %+v", res)
	}
	if res := Run(Env{Dir: t.TempDir(), Actor: "ui"}, "nope", nil); res.OK || res.Status != Invalid {
		t.Fatalf("unknown id: %+v", res)
	}
}

func TestPageMessagesHideThePath(t *testing.T) {
	dir, _ := store(t)
	res := Run(Env{Dir: dir, Actor: "ui"}, "machines.resume", &MachineArgs{Name: "m7"})
	if res.OK || strings.Contains(res.Message, dir) || !strings.Contains(res.Message, "the CI store") {
		t.Fatalf("page message: %+v", res)
	}
	res = Run(Env{Dir: dir, Actor: "cli", ShowPaths: true}, "machines.resume", &MachineArgs{Name: "m7"})
	if !strings.Contains(res.Message, dir) {
		t.Fatalf("cli message: %+v", res)
	}
}

func TestCancelDefaultReasonNamesTheActorAndLongReasonIsRefused(t *testing.T) {
	dir, ids := store(t, "m1")
	res := Run(Env{Dir: dir, Actor: "ui"}, "queue.cancel", CancelArgs{Target: Target{ID: ids[0]}})
	if !res.OK {
		t.Fatalf("cancel: %+v", res)
	}
	if es := entries(t, dir); es[0].Reason != "cancelled from the Homestretch page" {
		t.Fatalf("reason %q", es[0].Reason)
	}
	res = Run(Env{Dir: dir, Actor: "ui"}, "queue.cancel", CancelArgs{Target: Target{ID: ids[1]}, Reason: strings.Repeat("x", 301)})
	if res.Status != Invalid || res.Field != "reason" {
		t.Fatalf("long reason: %+v", res)
	}
}

func TestConfirmIsTiedToItsDryRun(t *testing.T) {
	dir, ids := store(t, "m1", "m2")
	env := Env{Dir: dir, Actor: "ui", Person: "pat", ConfirmNeedsBase: true}
	only9 := func(cb ConfirmBase, confirm, dry bool) Result {
		return Run(env, "policy.set", &PolicySetArgs{Project: "o/r", Mode: "only", Machines: []string{"m9"}, Confirm: confirm, DryRun: dry, ConfirmBase: cb})
	}
	dry := only9(ConfirmBase{}, false, true)
	if dry.Impact == nil || dry.Impact.Base == "" || dry.Impact.StrandedCount != 2 || len(dry.Impact.Stranded) != 2 {
		t.Fatalf("dry run: %+v", dry.Impact)
	}
	// The page must send the base back.
	if res := only9(ConfirmBase{}, true, false); res.Status != Invalid || res.Field != "base" {
		t.Fatalf("confirm without base: %+v", res)
	}
	// Another run is queued after the dry run: the impact the person saw is stale.
	q, err := localci.OpenQueue(localci.QueueDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := q.Enqueue(localci.Request{Kind: localci.KindPR, Repo: "o/r", Ref: "f", Base: "develop", Number: 9,
		SHA: strings.Repeat("e", 40), Workflow: "fast-gate.yml"}); err != nil {
		t.Fatal(err)
	}
	seen := ConfirmBase{Base: dry.Impact.Base, Stranded: dry.Impact.Stranded}
	res := only9(seen, true, false)
	if res.Status != Refused || res.Status.HTTPStatus() != 409 || res.Changed || res.Impact == nil || res.Impact.StrandedCount != 3 {
		t.Fatalf("stale impact: %+v", res)
	}
	if p, err := localci.LoadPolicy(dir); err != nil || p.Projects["o/r"].Mode == localci.PolicyOnly {
		t.Fatal("a stale confirm was written")
	}
	// The policy itself changed after a fresh dry run (someone else edited it): refused too.
	dry = only9(ConfirmBase{}, false, true)
	if r := Run(env, "policy.set", &PolicySetArgs{Project: "o/other", Mode: "only", Machines: []string{"m1"}}); !r.OK || !r.Changed {
		t.Fatalf("other edit: %+v", r)
	}
	if res := only9(ConfirmBase{Base: dry.Impact.Base, Stranded: dry.Impact.Stranded}, true, false); res.Status != Refused || res.Changed {
		t.Fatalf("stale base: %+v", res)
	}
	// A confirm with what the latest dry run showed applies.
	dry = only9(ConfirmBase{}, false, true)
	if res := only9(ConfirmBase{Base: dry.Impact.Base, Stranded: dry.Impact.Stranded}, true, false); !res.OK || !res.Changed {
		t.Fatalf("fresh confirm: %+v", res)
	}
	_ = ids
	// The CLI's --force needs no base.
	cli := Env{Dir: dir, Actor: "cli"}
	if res := Run(cli, "policy.set", &PolicySetArgs{Project: "o/r", Mode: "only", Machines: []string{"m8"}, Confirm: true}); !res.OK {
		t.Fatalf("cli force: %+v", res)
	}
}

func TestCancelReasonIsOneLine(t *testing.T) {
	dir, ids := store(t, "m1")
	for _, bad := range []string{"ok\nforged line", "tab\there", "cr\rhere", "nul\x00"} {
		res := Run(Env{Dir: dir, Actor: "ui"}, "queue.cancel", CancelArgs{Target: Target{ID: ids[0]}, Reason: bad})
		if res.Status != Invalid || res.Field != "reason" || res.Changed {
			t.Fatalf("%q: %+v", bad, res)
		}
	}
	for _, e := range entries(t, dir) {
		if strings.ContainsAny(e.Reason, "\n\r\t\x00") {
			t.Fatalf("a control character reached the log: %q", e.Reason)
		}
	}
	if res := Run(Env{Dir: dir, Actor: "ui"}, "queue.cancel", CancelArgs{Target: Target{ID: ids[0]}, Reason: "  plain  "}); !res.OK {
		t.Fatalf("plain reason: %+v", res)
	}
}

func TestCorruptStoreFilesAreBroken(t *testing.T) {
	for name, body := range map[string]string{"syntax": "{", "types": `{"v":"x","projects":[]}`, "version": `{"v":99,"projects":{}}`} {
		dir, _ := store(t, "m1")
		if err := os.WriteFile(localci.PolicyPath(dir), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		res := Run(Env{Dir: dir, Actor: "ui"}, "policy.set", &PolicySetArgs{Project: "o/r", Mode: "any"})
		if res.Status != Broken || res.Status.HTTPStatus() != 500 || res.Status.Outcome() != "error" {
			t.Errorf("policy %s: %v %+v", name, res.Status, res)
		}
		if res := Run(Env{Dir: dir, Actor: "ui"}, "machines.deny", &MachineRuleArgs{Name: "m1", Project: "o/r"}); res.Status != Broken {
			t.Errorf("deny with policy %s: %v", name, res.Status)
		}
	}
	for name, body := range map[string]string{"syntax": "[", "types": `{"version":"1","holds":{}}`, "version": `{"version":99,"holds":[]}`,
		"no id": `{"version":1,"holds":[{"project":"unravis"}]}`} {
		dir, _ := store(t, "m1")
		if err := os.WriteFile(filepath.Join(dir, "holds.json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		res := Run(Env{Dir: dir, Actor: "ui", Person: "pat"}, "hold.add", &HoldAddArgs{Project: "unravis", Until: "2h", Reason: "r"})
		if res.Status != Broken {
			t.Errorf("hold.add with holds %s: %v %+v", name, res.Status, res)
		}
		if res := Run(Env{Dir: dir, Actor: "ui", Person: "pat"}, "hold.lift", &HoldLiftArgs{ID: "h1", Reason: "r"}); res.Status != Broken {
			t.Errorf("hold.lift with holds %s: %v %+v", name, res.Status, res)
		}
	}
}

func TestHoldDaysAreCapped(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	if got, err := ParseWhen("3650d", now, true); err != nil || !got.Equal(now.AddDate(0, 0, 3650)) {
		t.Fatalf("3650d: %v %v", got, err)
	}
	for _, s := range []string{"3651d", "106752d", "9223372036854775807d", "87601h", "2562047h"} {
		if _, err := ParseWhen(s, now, true); err == nil {
			t.Errorf("%s was accepted", s)
		}
	}
	dir, _ := store(t, "m1")
	env := Env{Dir: dir, Actor: "ui", Person: "pat", Now: func() time.Time { return now }}
	if res := Run(env, "hold.add", &HoldAddArgs{Project: "unravis", Until: "9999-12-31", Reason: "r"}); res.Status != Invalid || res.Field != "until" {
		t.Fatalf("far date: %+v", res)
	}
	if res := Run(env, "hold.add", &HoldAddArgs{Project: "unravis", Until: "3650d", Reason: "r"}); !res.OK {
		t.Fatalf("3650d hold: %+v", res)
	}
}

func TestOverLongRunIDIsRefusedLikeAnUnknownRun(t *testing.T) {
	dir, _ := store(t, "m1")
	res := Run(Env{Dir: dir, Actor: "cli"}, "queue.retry", RetryArgs{Run: strings.Repeat("r", maxRunID+1)})
	if res.Status != Refused || res.Status.ExitCode() != 1 || res.Changed {
		t.Fatalf("over-long id: %+v", res)
	}
	for _, e := range entries(t, dir) {
		if len(e.Target) > maxRunID {
			t.Fatalf("the whole id reached the log: %d characters", len(e.Target))
		}
	}
}
