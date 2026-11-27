package releasedash

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"api-audit-explorer/internal/activitylog"
	"api-audit-explorer/internal/localci"
	"api-audit-explorer/internal/releasehold"
)

var hsPaths = []string{"/api/queue", "/api/policy", "/api/holds", "/api/activity"}

func hsQueueOf(t *testing.T, f *ciFixture) *localci.Queue {
	t.Helper()
	q, err := localci.OpenQueue(localci.QueueDir(f.dir))
	must(t, err)
	return q
}

func TestHSRoutesAreReadOnlyLoopbackAndCSP(t *testing.T) {
	f := newCIFixture(t)
	for _, p := range hsPaths {
		for _, m := range []string{"POST", "PUT", "DELETE", "PATCH"} {
			if c := f.do(t, m, "127.0.0.1:80", p, "tok").Code; c != 405 {
				t.Errorf("%s %s: %d", m, p, c)
			}
		}
		if c := f.do(t, "GET", "evil.example", p, "tok").Code; c != 404 {
			t.Errorf("rebind %s: %d", p, c)
		}
		if c := f.do(t, "GET", "127.0.0.1:80", p, "").Code; c != 401 {
			t.Errorf("no cookie %s: %d", p, c)
		}
		w := f.get(t, p)
		if w.Code != 200 {
			t.Errorf("GET %s: %d %s", p, w.Code, w.Body.String())
		}
		if !strings.Contains(w.Header().Get("Content-Security-Policy"), "default-src 'none'") || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Content-Type") != "application/json" {
			t.Errorf("headers of %s: %v", p, w.Header())
		}
		if c := f.get(t, p+"?project=nope").Code; c != 404 {
			t.Errorf("unknown project %s: %d", p, c)
		}
	}
}

func TestHSQueueShowsWaitingReasonPriorityAndSuperseded(t *testing.T) {
	f := newCIFixture(t)
	q := hsQueueOf(t, f)
	// A second push, moved to the top, and a pull request that a newer head replaces.
	a, _, err := q.Enqueue(localci.Request{Kind: localci.KindPush, Repo: "o/r", Ref: "develop", SHA: "eeeeeee5555555555555555555555555555555ee", Workflow: "fast-gate.yml"})
	must(t, err)
	top, err := q.MoveToTop(a.ID)
	must(t, err)
	if top.Priority >= localci.PriorityPush {
		t.Fatalf("not moved: %+v", top)
	}
	// Nobody may run o/r now: every queued request waits for a reason the answer must say.
	writeFile(t, filepath.Join(f.dir, "policy.json"), `{"v":1,"projects":{"o/r":{"mode":"only","machines":["zeta"]}}}`)

	out := decode[HSQueue](t, f.get(t, "/api/queue"))
	if !out.CIFound || out.Error != "" || out.Paused {
		t.Fatalf("%+v", out)
	}
	if out.Counts.Running != 1 || out.Counts.Queued != len(out.Queued) || out.Counts.Queued < 2 || out.Counts.Waiting != out.Counts.Queued {
		t.Fatalf("counts %+v", out.Counts)
	}
	if len(out.Running) != 1 || out.Running[0].ID != f.liveReq || out.Running[0].State != "running" || out.Running[0].ClaimedBy != "a1" {
		t.Fatalf("running %+v", out.Running)
	}
	first := out.Queued[0]
	if first.ID != a.ID || first.PriorityLabel != "First" {
		t.Fatalf("the moved request must lead and say First: %+v", out.Queued)
	}
	for _, r := range out.Queued[1:] {
		if r.PriorityLabel != "Normal" {
			t.Errorf("%s says %s", r.ID, r.PriorityLabel)
		}
	}
	for _, r := range out.Queued {
		if r.Why == nil || r.Why.Code != localci.WaitDenied || !r.Waiting || !strings.Contains(r.Why.Text, "only these: zeta") {
			t.Errorf("why of %s: %+v", r.ID, r.Why)
		}
	}
	if out.Running[0].Why != nil || out.Running[0].Waiting {
		t.Errorf("a running request does not wait: %+v", out.Running[0])
	}

	// Without the rule the first one in line is ready (a1 has a free slot) and is not counted as waiting.
	must(t, os.Remove(filepath.Join(f.dir, "policy.json")))
	out = decode[HSQueue](t, f.get(t, "/api/queue"))
	if out.Queued[0].Why == nil || out.Queued[0].Why.Code != localci.WaitReady || out.Queued[0].Waiting || out.Counts.Waiting != 0 {
		t.Fatalf("%+v %+v", out.Queued[0], out.Counts)
	}

	// A paused folder says so, and every queued request waits for it.
	must(t, localci.SetPaused(f.dir, true))
	out = decode[HSQueue](t, f.get(t, "/api/queue"))
	if !out.Paused || out.PausedReason == "" || out.Queued[0].Why.Code != localci.WaitPaused || out.Counts.Waiting != out.Counts.Queued {
		t.Fatalf("%+v", out)
	}
	must(t, localci.SetPaused(f.dir, false))

	// A superseded request shows what replaced it, in the finished list.
	old, _, err := q.Enqueue(localci.Request{Kind: localci.KindPR, Repo: "o/r", Ref: "feat3", Base: "develop", Number: 14, SHA: "1111111aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Workflow: "fast-gate.yml"})
	must(t, err)
	_, err = q.Supersede(old.ID, "20990101-000000-xyz")
	must(t, err)
	out = decode[HSQueue](t, f.get(t, "/api/queue"))
	found := false
	for _, r := range out.Done {
		if r.ID == old.ID {
			found = true
			if r.State != "superseded" || r.SupersededBy != "20990101-000000-xyz" || !strings.Contains(r.Display, "Replaced by") || !strings.Contains(r.Display, "20990101-000000-xyz") {
				t.Errorf("superseded: %+v", r)
			}
		}
	}
	if !found {
		t.Fatalf("the superseded request is not in the finished list: %+v", out.Done)
	}
}

func TestHSQueueExposesRunIDAndNeededLabelsForTheButtons(t *testing.T) {
	f := newCIFixture(t)
	q := hsQueueOf(t, f)
	// What a machine must have shows in the "Needs" column.
	a, _, err := q.Enqueue(localci.Request{Kind: localci.KindPush, Repo: "o/r", Ref: "develop", SHA: "abcdef1111111111111111111111111111111111", Workflow: "fast-gate.yml", Labels: []string{"windows", "gpu"}})
	must(t, err)
	// A finished request carries the run it made, so Retry knows what to run again.
	_, err = q.Complete(f.liveReq, "20260101-000000-abc", "success", "")
	must(t, err)
	out := decode[HSQueue](t, f.get(t, "/api/queue"))
	var gotLabels, gotRun bool
	for _, r := range out.Queued {
		if r.ID == a.ID && len(r.Labels) == 2 && r.Labels[0] == "windows" && r.Labels[1] == "gpu" {
			gotLabels = true
		}
	}
	for _, r := range out.Done {
		if r.ID == f.liveReq && r.RunID == "20260101-000000-abc" && r.Conclusion == "success" {
			gotRun = true
		}
	}
	if !gotLabels || !gotRun {
		t.Fatalf("labels %v run %v: %+v", gotLabels, gotRun, out)
	}
}

func TestHSQueueLimitAndBadValues(t *testing.T) {
	f := newCIFixture(t)
	q := hsQueueOf(t, f)
	for i := 0; i < 4; i++ {
		r, _, err := q.Enqueue(localci.Request{Kind: localci.KindPR, Repo: "o/r", Ref: "f", Base: "develop", Number: 100 + i, SHA: "2222222bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Workflow: "fast-gate.yml"})
		must(t, err)
		_, err = q.Cancel(r.ID, "test")
		must(t, err)
	}
	out := decode[HSQueue](t, f.get(t, "/api/queue?limit=2"))
	if len(out.Done) != 2 || out.Counts.Done < 4 {
		t.Fatalf("done %d, counts %+v", len(out.Done), out.Counts)
	}
	for _, bad := range []string{"limit=0", "limit=-1", "limit=x", "limit=999999"} {
		if c := f.get(t, "/api/queue?"+bad).Code; c != 400 {
			t.Errorf("%s: %d", bad, c)
		}
	}
}

func TestHSPolicyShowsRulesMachinesAndImpact(t *testing.T) {
	f := newCIFixture(t)
	writeFile(t, filepath.Join(f.dir, "policy.json"), `{"v":1,"updated":"2026-11-01T10:00:00Z","projects":{"o/r":{"mode":"only","machines":["zeta"]},"x/y":{"mode":"except","machines":["a1"]}}}`)
	must(t, localci.RequestDrain(f.dir, "a1"))

	out := decode[HSPolicy](t, f.get(t, "/api/policy"))
	if !out.CIFound || out.Error != "" || out.Updated == "" {
		t.Fatalf("%+v", out)
	}
	by := map[string]HSProjectPolicy{}
	for _, p := range out.Projects {
		by[p.Project] = p
	}
	or, xy := by["o/r"], by["x/y"]
	if or.Mode != "only" || !or.Explicit || len(or.Machines) != 1 || or.Machines[0] != "zeta" || !strings.Contains(or.Text, "only these: zeta") {
		t.Fatalf("o/r %+v", or)
	}
	// Both queued requests of o/r have a fitting machine that the rule does not allow.
	if or.Impact.Queued != 1 || or.Impact.NoMachine != 1 || !strings.Contains(or.ImpactLine, "no machine") {
		t.Fatalf("o/r impact %+v %q", or.Impact, or.ImpactLine)
	}
	if xy.Mode != "except" || xy.Impact.Queued != 0 || xy.ImpactLine != "No run of this project is waiting." {
		t.Fatalf("x/y %+v", xy)
	}
	m := map[string]HSMachine{}
	for _, x := range out.Machines {
		m[x.Name] = x
	}
	if len(m) != 2 || m["a1"].State != "ready" || m["a1"].Running != 1 || m["a1"].Slots != 2 || !m["a1"].DrainAsked || m["old"].State != "gone" || m["old"].SeenAgo == "" {
		t.Fatalf("machines %+v", out.Machines)
	}
	if strings.Join(m["a1"].Denied, ",") != "o/r,x/y" || strings.Join(m["old"].Denied, ",") != "o/r" {
		t.Fatalf("denied a1 %v old %v", m["a1"].Denied, m["old"].Denied)
	}
}

func TestHSPolicyWithoutAFileShowsTheDefault(t *testing.T) {
	f := newCIFixture(t)
	out := decode[HSPolicy](t, f.get(t, "/api/policy"))
	if out.Error != "" || len(out.Projects) != 1 {
		t.Fatalf("%+v", out)
	}
	p := out.Projects[0]
	if p.Project != "o/r" || p.Mode != "any" || p.Explicit || p.Impact.NoMachine != 0 || p.Impact.Queued != 1 {
		t.Fatalf("%+v", p)
	}
	if _, err := os.Stat(filepath.Join(f.dir, "policy.json")); err == nil {
		t.Fatal("reading wrote the policy file")
	}
}

func TestHSHoldsListByState(t *testing.T) {
	f := newCIFixture(t)
	s := releasehold.Store{Dir: f.dir}
	now := time.Now()
	act, err := s.Add(releasehold.NewHold{Project: "unravis", Reason: "freeze for the demo", Owner: "nachi", Start: now.Add(-time.Hour), End: now.Add(time.Hour)}, now)
	must(t, err)
	up, err := s.Add(releasehold.NewHold{Project: "all", Reason: "later", Owner: "nachi", Start: now.Add(2 * time.Hour), End: now.Add(3 * time.Hour)}, now)
	must(t, err)
	lf, err := s.Add(releasehold.NewHold{Project: "unravis", Reason: "was needed", Owner: "nachi", Start: now.Add(-time.Hour), End: now.Add(5 * time.Hour)}, now)
	must(t, err)
	_, err = s.Lift(lf.ID, "nachi", "all clear "+fxTokenSecret, now)
	must(t, err)
	en, err := s.Add(releasehold.NewHold{Project: "unravis", Reason: "past", Owner: "nachi", Start: now.Add(-3 * time.Hour), End: now.Add(-2 * time.Hour)}, now.Add(-4*time.Hour))
	must(t, err)

	out := decode[HSHolds](t, f.get(t, "/api/holds"))
	if out.Error != "" || len(out.Active) != 1 || len(out.Upcoming) != 1 || len(out.Lifted) != 1 || len(out.Ended) != 1 {
		t.Fatalf("%+v", out)
	}
	if out.Active[0].ID != act.ID || out.Active[0].State != "active" || out.Active[0].Reason != "freeze for the demo" || out.Active[0].Owner != "nachi" {
		t.Fatalf("active %+v", out.Active[0])
	}
	if out.Upcoming[0].ID != up.ID || out.Upcoming[0].Project != "all" {
		t.Fatalf("upcoming %+v", out.Upcoming[0])
	}
	l := out.Lifted[0]
	if l.ID != lf.ID || l.State != "lifted" || l.LiftedBy != "nachi" || l.LiftedAt == "" || strings.Contains(l.LiftReason, fxTokenSecret) {
		t.Fatalf("lifted %+v", l)
	}
	if out.Ended[0].ID != en.ID || out.Ended[0].State != "ended" {
		t.Fatalf("ended %+v", out.Ended[0])
	}
}

func TestHSActivityFiltersByActorAndLimit(t *testing.T) {
	f := newCIFixture(t)
	l := activitylog.Log{Dir: f.dir}
	base := time.Now().Add(-time.Hour)
	for i, e := range []activitylog.Entry{
		{Actor: "cli", Action: "hold.add", Target: "h1", Reason: "one"},
		{Actor: "ui", Action: "queue.move-to-top", Target: "r1"},
		{Actor: "cli", Action: "hold.lift", Target: "h1", Reason: "two " + fxTokenSecret},
		{Actor: "claude", Action: "policy.set", Target: "o/r"},
	} {
		e.Time = base.Add(time.Duration(i) * time.Minute)
		must(t, l.Append(e))
	}
	out := decode[HSActivity](t, f.get(t, "/api/activity"))
	if out.Error != "" || len(out.Entries) != 4 || out.Order != "oldest-first" || out.Entries[0].Action != "hold.add" || out.Entries[3].Actor != "claude" {
		t.Fatalf("%+v", out)
	}
	out = decode[HSActivity](t, f.get(t, "/api/activity?actor=cli"))
	if len(out.Entries) != 2 || out.Entries[0].Actor != "cli" || out.Entries[1].Actor != "cli" {
		t.Fatalf("%+v", out.Entries)
	}
	if strings.Contains(out.Entries[1].Reason, fxTokenSecret) {
		t.Fatalf("a secret reached the page: %+v", out.Entries[1])
	}
	out = decode[HSActivity](t, f.get(t, "/api/activity?limit=2"))
	if len(out.Entries) != 2 || out.Entries[0].Action != "hold.lift" || out.Entries[1].Action != "policy.set" {
		t.Fatalf("the newest two, oldest first: %+v", out.Entries)
	}
	for _, bad := range []string{"actor=root", "limit=0", "limit=abc", "limit=100000"} {
		if c := f.get(t, "/api/activity?"+bad).Code; c != 400 {
			t.Errorf("%s: %d", bad, c)
		}
	}
}

func TestHSMissingStoreIsEmptyNotAnError(t *testing.T) {
	d := New(t.TempDir(), "", nil)
	d.Projects = []Project{{ID: "unravis", Name: "Unravis", Repo: "o/r", CIStore: filepath.Join(t.TempDir(), "not-there")}}
	must(t, d.Projects[0].Normalize())
	f := &ciFixture{d: d, h: Handler(d, "tok", fstest.MapFS{"index.html": {Data: []byte("x")}})}
	for _, p := range hsPaths {
		w := f.get(t, p)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"ciFound":false`) || strings.Contains(w.Body.String(), `"error"`) {
			t.Errorf("%s: %d %s", p, w.Code, w.Body.String())
		}
	}
	q := decode[HSQueue](t, f.get(t, "/api/queue"))
	if q.CIFound || q.Queued == nil || q.Running == nil || q.Done == nil {
		t.Fatalf("lists must be empty arrays, not null: %+v", q)
	}
	if _, err := os.Stat(d.Projects[0].CIStore); err == nil {
		t.Fatal("reading created the store folder")
	}
	// A project with no store folder set must not read the working folder.
	d2 := New(t.TempDir(), "", nil)
	f2 := &ciFixture{d: d2, h: Handler(d2, "tok", fstest.MapFS{"index.html": {Data: []byte("x")}})}
	for _, p := range hsPaths {
		if w := f2.get(t, p); w.Code != 200 || !strings.Contains(w.Body.String(), `"ciFound":false`) {
			t.Errorf("%s without a store: %d %s", p, w.Code, w.Body.String())
		}
	}
}

func TestHSCorruptFilesGiveAnErrorFieldNotACrash(t *testing.T) {
	f := newCIFixture(t)
	writeFile(t, filepath.Join(f.dir, "policy.json"), "{not json")
	writeFile(t, filepath.Join(f.dir, "holds.json"), "][")
	writeFile(t, filepath.Join(f.dir, "activity.jsonl"), "garbage line\n{\"time\":\"2026-11-01T10:00:00Z\",\"actor\":\"cli\",\"action\":\"hold.add\"}\n{torn")

	pol := decode[HSPolicy](t, f.get(t, "/api/policy"))
	if !strings.Contains(pol.Error, "machine policy could not be read") || len(pol.Machines) != 2 {
		t.Fatalf("policy %+v", pol)
	}
	h := decode[HSHolds](t, f.get(t, "/api/holds"))
	if !strings.Contains(h.Error, "holds could not be read") || h.Active == nil || len(h.Active) != 0 {
		t.Fatalf("holds %+v", h)
	}
	a := decode[HSActivity](t, f.get(t, "/api/activity"))
	if a.Error != "" || a.Skipped != 2 || len(a.Entries) != 1 {
		t.Fatalf("a torn or bad line is skipped and counted: %+v", a)
	}
	q := decode[HSQueue](t, f.get(t, "/api/queue"))
	if !strings.Contains(q.Error, "Why a request waits could not be worked out") || len(q.Queued) < 1 || q.Queued[0].Why != nil {
		t.Fatalf("queue %+v", q)
	}

	// A hold store from a newer version is refused the same way.
	writeFile(t, filepath.Join(f.dir, "holds.json"), `{"version":99,"holds":[]}`)
	if h := decode[HSHolds](t, f.get(t, "/api/holds")); !strings.Contains(h.Error, "format version 99") {
		t.Fatalf("%+v", h)
	}
	// A request file that is not JSON is reported and the others are still listed.
	writeFile(t, filepath.Join(localci.QueueDir(f.dir), "queued", "broken.json"), "{")
	if q := decode[HSQueue](t, f.get(t, "/api/queue")); q.Error == "" {
		t.Fatalf("a broken request file must be reported: %+v", q)
	}
}

func TestHSReadingNeverWritesTheStore(t *testing.T) {
	f := newCIFixture(t)
	now := time.Now()
	_, err := releasehold.Store{Dir: f.dir}.Add(releasehold.NewHold{Project: "all", Reason: "r", Owner: "o", Start: now.Add(-time.Minute), End: now.Add(time.Hour)}, now)
	must(t, err)
	must(t, activitylog.Log{Dir: f.dir}.Append(activitylog.Entry{Actor: "cli", Action: "hold.add"}))
	writeFile(t, filepath.Join(f.dir, "policy.json"), `{"v":1,"projects":{"o/r":{"mode":"any"}}}`)
	snap := func() map[string]string {
		m := map[string]string{}
		_ = filepath.WalkDir(f.dir, func(p string, e os.DirEntry, err error) error {
			if err != nil {
				return nil //nolint:nilerr // a file that vanished is just not in the snapshot
			}
			if e.IsDir() {
				m[p] = "dir"
				return nil
			}
			fi, _ := e.Info()
			m[p] = fi.ModTime().String() + "|" + strconv.FormatInt(fi.Size(), 10)
			return nil
		})
		return m
	}
	before := snap()
	for _, p := range append(hsPaths, "/api/queue?limit=5", "/api/activity?actor=cli&limit=3") {
		if w := f.get(t, p); w.Code != 200 {
			t.Fatalf("%s: %d", p, w.Code)
		}
	}
	after := snap()
	if len(before) != len(after) {
		t.Fatalf("the store changed: %d entries before, %d after", len(before), len(after))
	}
	for k, v := range before {
		if after[k] != v {
			t.Fatalf("%s changed: %q -> %q", k, v, after[k])
		}
	}
}

func TestHSBareStoreFolderCreatesNothing(t *testing.T) {
	// A store folder that exists but holds nothing yet: the answers are empty and no queue folder, lock or file appears.
	dir := t.TempDir()
	d := New(t.TempDir(), "", nil)
	d.Projects = []Project{{ID: "unravis", Name: "Unravis", Repo: "o/r", CIStore: dir}}
	must(t, d.Projects[0].Normalize())
	f := &ciFixture{d: d, h: Handler(d, "tok", fstest.MapFS{"index.html": {Data: []byte("x")}})}
	for _, p := range hsPaths {
		if w := f.get(t, p); w.Code != 200 || strings.Contains(w.Body.String(), `"error"`) {
			t.Errorf("%s: %d %s", p, w.Code, w.Body.String())
		}
	}
	q := decode[HSQueue](t, f.get(t, "/api/queue"))
	if !q.CIFound || q.Counts != (HSQueueCounts{}) {
		t.Fatalf("%+v", q)
	}
	left, err := os.ReadDir(dir)
	must(t, err)
	if len(left) != 0 {
		t.Fatalf("reading created %d entries in the store", len(left))
	}
}

// The dashboard hides the machine and user names it finds in the audit log from log text. A hold's owner and the person in the
// activity log are data, not log text: they must show even when the same name is on the hide list (the fixture's audit log names
// the user "zed"), while a secret in the same field is still hidden.
func TestHSPeopleShowEvenWhenTheirNameIsOnTheHideList(t *testing.T) {
	f := newCIFixture(t)
	now := time.Now()
	s := releasehold.Store{Dir: f.dir}
	h, err := s.Add(releasehold.NewHold{Project: "unravis", Reason: "freeze, ask zed", Owner: "zed", Start: now.Add(-time.Hour), End: now.Add(time.Hour)}, now)
	must(t, err)
	_, err = s.Lift(h.ID, "zed", "done", now)
	must(t, err)
	must(t, activitylog.Log{Dir: f.dir}.Append(activitylog.Entry{Time: now, Actor: "cli", Person: "zed", Action: "hold.lift", Target: h.ID}))
	must(t, activitylog.Log{Dir: f.dir}.Append(activitylog.Entry{Time: now, Actor: "cli", Person: "zed " + fxTokenSecret, Action: "x"}))

	holds := decode[HSHolds](t, f.get(t, "/api/holds"))
	if len(holds.Lifted) != 1 || holds.Lifted[0].Owner != "zed" || holds.Lifted[0].LiftedBy != "zed" {
		t.Fatalf("the owner was hidden: %+v", holds)
	}
	if strings.Contains(holds.Lifted[0].Reason, "zed") {
		t.Fatalf("the hide list no longer applies to free text, so this test proves nothing: %q", holds.Lifted[0].Reason)
	}
	act := decode[HSActivity](t, f.get(t, "/api/activity"))
	if len(act.Entries) < 2 || act.Entries[0].Person != "zed" {
		t.Fatalf("the person was hidden: %+v", act.Entries)
	}
	if strings.Contains(act.Entries[1].Person, fxTokenSecret) {
		t.Fatalf("a secret reached the page in the person field: %+v", act.Entries[1])
	}
}
