package server

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/bbsaved"
	"api-audit-explorer/internal/blackbox"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/casestore/storetest"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/investigate"
)

// Tests of the Blackbox pages: the add flow, the states, the this-PC actions, the
// host reading with the gap rule (A35) and the promise that no read key leaves the
// sealed list. Every place is the in-memory world of blackbox_demo.go, so no share,
// service, port or registry is touched.

const (
	bbKeyA = "ubk-SENTINEL-read-key-aaaa-1111"
	bbKeyB = "ubk-SENTINEL-read-key-bbbb-2222"
)

// bbEnv is a server with the Blackbox pages over a real sealed list and a fake world.
type bbEnv struct {
	t     *testing.T
	e     *caseEnv
	bbs   *bbsaved.Store
	path  string
	world *simWorld
	pc    *simPC

	mu   sync.Mutex
	nowT time.Time
}

func (b *bbEnv) now() time.Time {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.nowT
}

func (b *bbEnv) setNow(t time.Time) {
	b.mu.Lock()
	b.nowT = t
	b.mu.Unlock()
}

func jb(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

// newBBEnv builds the server. mod can change the deps (drop the helper, say).
func newBBEnv(t *testing.T, mod func(*BlackboxDeps)) *bbEnv {
	t.Helper()
	b := &bbEnv{t: t, nowT: invT0.Add(3*time.Hour + 10*time.Second), world: newSimWorld()}
	b.path = filepath.Join(t.TempDir(), "cfg", "blackboxes.dat")
	store, err := bbsaved.New(b.path, storetest.RandomProtector())
	if err != nil {
		t.Fatal(err)
	}
	store.SetClock(b.now)
	b.bbs = store
	b.pc = newSimPC(b.world, b.now())
	deps := BlackboxDeps{
		Store: store, ThisPC: b.pc, Scan: b.pc.Scan, Program: bytesProgram("MZ test recorder program"),
		Open: b.world.Open, Now: b.now,
	}
	if mod != nil {
		mod(&deps)
	}
	repo := &fakeRepository{requestPages: []audit.Page{okPage(invRows())}}
	b.e = tagEnv(t, casestore.NewMemory(), repo, WithBlackbox(deps))
	return b
}

// busyCPU is 95 percent from 10:20 to 11:00 and 25 otherwise.
func busyCPU(t time.Time) float64 {
	if !t.Before(invT0.Add(80*time.Minute)) && t.Before(invT0.Add(120*time.Minute)) {
		return 95
	}
	return 25
}

// bbRec builds a recording from 08:30 to 12:00 on the test day.
func bbRec(name string, mod func(*simRec)) *simBB {
	r := simRec{
		Name: name, Start: invT0.Add(-30 * time.Minute), End: invT0.Add(3 * time.Hour),
		Now: invT0.Add(3*time.Hour + 10*time.Second), CPU: busyCPU, Busy: "w3wp",
	}
	if mod != nil {
		mod(&r)
	}
	return r.build()
}

func (b *bbEnv) post(path, body string) *httptest.ResponseRecorder {
	return b.e.json(http.MethodPost, path, body)
}

func (b *bbEnv) list() bbListAnswer {
	b.t.Helper()
	res := b.e.get("/api/blackbox")
	if res.Code != http.StatusOK {
		b.t.Fatalf("list = %d %s", res.Code, res.Body)
	}
	var out bbListAnswer
	decodeBody(b.t, res, &out)
	return out
}

func (b *bbEnv) add(location, key string) *httptest.ResponseRecorder {
	return b.post("/api/blackbox", jb(map[string]string{"location": location, "key": key}))
}

func (b *bbEnv) mustAdd(location, key string) bbItem {
	b.t.Helper()
	res := b.add(location, key)
	if res.Code != http.StatusCreated {
		b.t.Fatalf("add %s = %d %s", location, res.Code, res.Body)
	}
	var it bbItem
	decodeBody(b.t, res, &it)
	return it
}

func bbFail(t *testing.T, res *httptest.ResponseRecorder, code int, reason string) bbFailure {
	t.Helper()
	var f bbFailure
	decodeBody(t, res, &f)
	if res.Code != code || f.Reason != reason {
		t.Fatalf("got %d reason %q (%s), want %d %q", res.Code, f.Reason, res.Body, code, reason)
	}
	return f
}

const share = `\\web-b\UnravisBlackbox`

func (b *bbEnv) hostReading(id, incStart, incEnd string) *httptest.ResponseRecorder {
	return b.post("/api/blackbox/"+id+"/host-reading", `{"incident":{"start":"`+incStart+`","end":"`+incEnd+`"},"reference":{"start":"2026-10-20T09:00:00Z","end":"2026-10-20T10:00:00Z"}}`)
}

func (b *bbEnv) hostAnswer(id, incStart, incEnd string) bbHostAnswer {
	b.t.Helper()
	res := b.hostReading(id, incStart, incEnd)
	if res.Code != http.StatusOK {
		b.t.Fatalf("host-reading = %d %s", res.Code, res.Body)
	}
	var a bbHostAnswer
	decodeBody(b.t, res, &a)
	return a
}

/* ------------------------------------------------------------ the list */

func TestBlackboxListAddAndCheck(t *testing.T) {
	b := newBBEnv(t, nil)
	if l := b.list(); len(l.Items) != 0 || !l.CanPack || !l.ThisPC.Available || l.ThisPC.Installed {
		t.Fatalf("empty list = %+v", l)
	}
	b.world.put(share, bbRec("web-b", nil))
	it := b.mustAdd(share, "")
	if it.Name != "web-b" || it.Kind != blackbox.LocShare || it.HasKey || !strings.HasPrefix(it.ID, "bb-") {
		t.Fatalf("added = %+v", it.Entry)
	}
	if it.State.State != blackbox.SRunning || it.State.Label != "Running" || it.Facts == nil || it.Facts.Samples == 0 || it.Facts.Host != "WEB-B" {
		t.Fatalf("state = %+v facts = %+v", it.State, it.Facts)
	}
	l := b.list()
	if len(l.Items) != 1 || l.Items[0].ID != it.ID || l.Items[0].State.State != blackbox.SRunning {
		t.Fatalf("list = %+v", l.Items)
	}
	// Check re-reads one entry.
	res := b.post("/api/blackbox/"+it.ID+"/check", "")
	if res.Code != http.StatusOK {
		t.Fatalf("check = %d %s", res.Code, res.Body)
	}
	bbFail(t, b.post("/api/blackbox/bb-000000000000000000000099/check", ""), http.StatusNotFound, "not-listed")
	// Trying without saving.
	res = b.post("/api/blackbox/test", jb(map[string]string{"location": share}))
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"ok":true`) {
		t.Fatalf("test = %d %s", res.Code, res.Body)
	}
	if got := len(b.list().Items); got != 1 {
		t.Fatalf("a test saved an entry: %d", got)
	}
}

func TestBlackboxAddByURLKeepsTheKeySealed(t *testing.T) {
	b := newBBEnv(t, nil)
	b.world.put("http://web-c:7788", bbRec("web-c", func(r *simRec) { r.Key = bbKeyA }))
	res := b.add("web-c:7788", bbKeyA)
	if res.Code != http.StatusCreated {
		t.Fatalf("add = %d %s", res.Code, res.Body)
	}
	var it bbItem
	decodeBody(t, res, &it)
	if !it.HasKey || it.Location != "http://web-c:7788" || it.Kind != blackbox.LocURL {
		t.Fatalf("added = %+v", it.Entry)
	}
	// The answer, the list and the sealed file never carry the key; the store does.
	for what, body := range map[string]string{"add": res.Body.String(), "list": b.e.get("/api/blackbox").Body.String(), "check": b.post("/api/blackbox/"+it.ID+"/check", "").Body.String()} {
		if strings.Contains(body, "SENTINEL") {
			t.Fatalf("%s answer carries the key: %s", what, body)
		}
	}
	raw, err := os.ReadFile(b.path)
	if err != nil || bytes.Contains(raw, []byte(bbKeyA)) || bytes.Contains(raw, []byte("SENTINEL")) {
		t.Fatalf("the sealed file holds the key in the clear (%v)", err)
	}
	if k, err := b.bbs.Key(it.ID); err != nil || k != bbKeyA {
		t.Fatalf("key = %q %v", k, err)
	}
	// An address that is not in the list yet needs a key.
	b.world.put("http://web-d:7788", bbRec("web-d", func(r *simRec) { r.Key = bbKeyA }))
	bbFail(t, b.add("web-d:7788", ""), http.StatusUnprocessableEntity, "key")
}

func TestBlackboxBadLocations(t *testing.T) {
	b := newBBEnv(t, nil)
	for _, loc := range []string{"", "ftp://web-b/x", "   ", "http://user:pw@web-b:7788"} {
		res := b.add(loc, bbKeyA)
		if res.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%q = %d %s", loc, res.Code, res.Body)
		}
	}
	if got := len(b.list().Items); got != 0 {
		t.Fatalf("a bad place was saved: %d", got)
	}
	// A place that is not there: nothing saved, the reason is Not found.
	f := bbFail(t, b.add(`D:\Nowhere`, ""), http.StatusUnprocessableEntity, "not-found")
	if f.Next == "" || f.State == nil || f.State.State != blackbox.SNotFound {
		t.Fatalf("failure = %+v", f)
	}
	// An address that does not answer.
	f = bbFail(t, b.add("web-z:7788", bbKeyA), http.StatusUnprocessableEntity, "timeout")
	if f.Next == "" || f.State == nil || f.State.State != blackbox.SUnreachable {
		t.Fatalf("failure = %+v", f)
	}
	if got := len(b.list().Items); got != 0 {
		t.Fatalf("a failed add saved an entry: %d", got)
	}
}

// Clumsy-user case 1: the same Blackbox added twice, written two ways.
func TestBlackboxTheSameOneAddedTwiceIsOneEntry(t *testing.T) {
	b := newBBEnv(t, nil)
	b.world.put(share, bbRec("web-b", nil))
	first := b.mustAdd(share, "")
	for _, again := range []string{share, `//WEB-B/unravisblackbox/`} {
		f := bbFail(t, b.add(again, ""), http.StatusConflict, "duplicate")
		if f.Existing == nil || f.Existing.ID != first.ID {
			t.Fatalf("existing = %+v", f.Existing)
		}
	}
	// The same recorder reached by its address is the same Blackbox.
	b.world.put("http://web-b:7788", bbRec("web-b", func(r *simRec) { r.Key = bbKeyA }))
	f := bbFail(t, b.add("web-b:7788", bbKeyA), http.StatusConflict, "duplicate")
	if f.Existing == nil || f.Existing.ID != first.ID {
		t.Fatalf("existing = %+v", f.Existing)
	}
	if k, _ := b.bbs.Key(first.ID); k != "" {
		t.Fatal("a refused duplicate left its key behind")
	}
	if got := len(b.list().Items); got != 1 {
		t.Fatalf("%d entries", got)
	}
}

// Clumsy-user case 3: an address with the wrong read key.
func TestBlackboxWrongKeyIsRefusedAndNothingIsSaved(t *testing.T) {
	b := newBBEnv(t, nil)
	b.world.put("http://web-c:7788", bbRec("web-c", func(r *simRec) { r.Key = bbKeyA }))
	f := bbFail(t, b.add("web-c:7788", bbKeyB), http.StatusUnprocessableEntity, "key")
	if !strings.Contains(f.Next, "read key") || strings.Contains(f.Error+f.Next, "SENTINEL") {
		t.Fatalf("failure = %+v", f)
	}
	if got := len(b.list().Items); got != 0 {
		t.Fatalf("%d entries", got)
	}
	// Changing the key of a saved entry: a wrong one never replaces a good one.
	it := b.mustAdd("web-c:7788", bbKeyA)
	b.world.get("http://web-c:7788").key = bbKeyB // the server's key was changed
	if got := b.list().Items[0].State; got.State != blackbox.SUnreachable || got.Reason != "key" {
		t.Fatalf("state = %+v", got)
	}
	bbFail(t, b.e.json(http.MethodPut, "/api/blackbox/"+it.ID+"/key", jb(map[string]string{"key": "ubk-wrong"})), http.StatusUnprocessableEntity, "key")
	if k, _ := b.bbs.Key(it.ID); k != bbKeyA {
		t.Fatalf("a wrong key replaced the old one: %q", k)
	}
	res := b.e.json(http.MethodPut, "/api/blackbox/"+it.ID+"/key", jb(map[string]string{"key": bbKeyB}))
	if res.Code != http.StatusOK || strings.Contains(res.Body.String(), "SENTINEL") {
		t.Fatalf("put key = %d %s", res.Code, res.Body)
	}
	if k, _ := b.bbs.Key(it.ID); k != bbKeyB {
		t.Fatalf("key = %q", k)
	}
	if got := b.list().Items[0].State.State; got != blackbox.SRunning {
		t.Fatalf("state = %s", got)
	}
	// A share has no key to set.
	b.world.put(share, bbRec("web-b", nil))
	sh := b.mustAdd(share, "")
	bbFail(t, b.e.json(http.MethodPut, "/api/blackbox/"+sh.ID+"/key", jb(map[string]string{"key": "x"})), http.StatusUnprocessableEntity, "invalid")
}

func TestBlackboxRemoveFromTheListLeavesTheBlackbox(t *testing.T) {
	b := newBBEnv(t, nil)
	b.world.put(share, bbRec("web-b", nil))
	it := b.mustAdd(share, "")
	res := b.e.json(http.MethodDelete, "/api/blackbox/"+it.ID, "")
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "not touched") {
		t.Fatalf("delete = %d %s", res.Code, res.Body)
	}
	if b.world.get(share) == nil {
		t.Fatal("the Blackbox itself was touched")
	}
	bbFail(t, b.e.json(http.MethodDelete, "/api/blackbox/"+it.ID, ""), http.StatusNotFound, "not-listed")
	if res := b.e.json(http.MethodDelete, "/api/blackbox/not-an-id", ""); res.Code != http.StatusNotFound {
		t.Fatalf("a bad id = %d", res.Code)
	}
}

/* -------------------------------------------------------------- states */

func TestBlackboxEveryStateAppearsOnTheDemoList(t *testing.T) {
	now := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	h := New(&fakeRepository{}, testAssets(), testToken, false, WithBlackboxDemo(now))
	res := request(h, http.MethodGet, "/api/blackbox", "", true)
	if res.Code != http.StatusOK {
		t.Fatalf("list = %d %s", res.Code, res.Body)
	}
	var l bbListAnswer
	decodeBody(t, res, &l)
	got := map[string]string{}
	for _, it := range l.Items {
		got[it.Name] = string(it.State.State)
	}
	want := map[string]string{
		"web-a": "running", "web-b": "running-late", "web-old": "stopped", "web-c": "disk-low", "web-d": "clock-differs",
		"web-e": "unreachable", "web-f": "incomplete", "old-box": "not-found", "web-g": "not-placed",
	}
	for n, s := range want {
		if got[n] != s {
			t.Errorf("%s = %q, want %q (all: %v)", n, got[n], s, got)
		}
	}
	if !l.ThisPC.Installed || !l.ThisPC.Listed {
		t.Fatalf("this PC = %+v", l.ThisPC)
	}
	// Not placed: the this-PC Blackbox is removed from the service but still listed.
	h2 := New(&fakeRepository{}, testAssets(), testToken, false, WithBlackboxDemo(now))
	if r := request(h2, http.MethodPost, "/api/blackbox/this-pc/remove", `{}`, true); r.Code != http.StatusOK {
		t.Fatalf("remove = %d %s", r.Code, r.Body)
	}
	var after bbListAnswer
	decodeBody(t, request(h2, http.MethodGet, "/api/blackbox", "", true), &after)
	if after.ThisPC.Installed {
		t.Fatalf("this PC = %+v", after.ThisPC)
	}
	for _, it := range after.Items {
		if it.Name == "web-a" {
			t.Fatalf("the removed Blackbox is still listed: %+v", it)
		}
	}
}

// Clumsy-user case 2: a share that drops part way through a read.
func TestBlackboxAShareThatDropsMidReadIsIncompleteAndKeepsWhatWasRead(t *testing.T) {
	b := newBBEnv(t, nil)
	b.setNow(invT0.Add(3*time.Hour + 20*time.Minute + 10*time.Second))
	b.world.put(`\\web-f\Blackbox`, bbRec("web-f", func(r *simRec) {
		r.End = invT0.Add(3*time.Hour + 20*time.Minute)
		r.DropBefore = 5 * time.Minute
	}))
	it := b.mustAdd(`\\web-f\Blackbox`, "")
	if it.State.State != blackbox.SIncomplete {
		t.Fatalf("state = %+v", it.State)
	}
	a := b.hostAnswer(it.ID, "2026-10-20T11:30:00Z", "2026-10-20T12:20:00Z")
	if len(a.Read.Dropped) != 1 || a.Read.Samples == 0 {
		t.Fatalf("read = %+v", a.Read)
	}
	if a.State.State != blackbox.SIncomplete {
		t.Fatalf("state = %+v", a.State)
	}
	// The tail the share did not give is a long gap: it cannot be tested.
	var tail *investigate.Span
	for i := range a.Gaps {
		if a.Gaps[i].Window == "incident" && a.Gaps[i].Kind == "long" {
			tail = &a.Gaps[i]
		}
	}
	if tail == nil || tail.Minutes < 4 || len(a.Step.UntestedSpans) == 0 {
		t.Fatalf("gaps = %+v untested = %+v", a.Gaps, a.Step.UntestedSpans)
	}
	if a.Step.Readiness == investigate.Ready {
		t.Fatalf("a read that dropped is Ready: %+v", a.Step)
	}
	// A share that is gone altogether while the page is open reads Unreachable-free
	// failure: the list says Not found, nothing is invented.
	b.world.drop(`\\web-f\Blackbox`)
	if got := b.list().Items[0].State.State; got != blackbox.SNotFound {
		t.Fatalf("after the share went away: %s", got)
	}
	bbFail(t, b.hostReading(it.ID, "2026-10-20T11:30:00Z", "2026-10-20T12:20:00Z"), http.StatusUnprocessableEntity, "not-found")
}

/* ---------------------------------------------------------- gap rule */

// Clumsy-user case 4: a 3-minute gap cannot be tested (A35); a short one warns.
func TestBlackboxAThreeMinuteGapCannotBeTested(t *testing.T) {
	b := newBBEnv(t, nil)
	skip := [][2]time.Time{{invT0.Add(100 * time.Minute), invT0.Add(103 * time.Minute)}}
	b.world.put(share, bbRec("web-b", func(r *simRec) { r.Skip = skip }))
	it := b.mustAdd(share, "")
	a := b.hostAnswer(it.ID, "2026-10-20T10:00:00Z", "2026-10-20T11:30:00Z")
	if len(a.Gaps) != 1 || a.Gaps[0].Kind != "long" || a.Gaps[0].Window != "incident" || a.Gaps[0].Minutes < 3 {
		t.Fatalf("gaps = %+v", a.Gaps)
	}
	if len(a.Step.UntestedSpans) != 1 || a.Step.UntestedSpans[0].Kind != "long" {
		t.Fatalf("untested = %+v", a.Step.UntestedSpans)
	}
	if a.Step.Readiness == investigate.Ready || !strings.Contains(strings.Join(a.Step.Reasons, " "), "long gap") {
		t.Fatalf("step = %+v", a.Step)
	}
	// The busy stretch is still found from the minutes that exist, and the missing
	// minutes are not counted as zero.
	if a.Onset == nil || a.TopProcess != "w3wp" {
		t.Fatalf("onset %v top %q note %q", a.Onset, a.TopProcess, a.Note)
	}
}

func TestBlackboxAShortGapOnlyWarns(t *testing.T) {
	b := newBBEnv(t, nil)
	skip := [][2]time.Time{{invT0.Add(110 * time.Minute), invT0.Add(111 * time.Minute)}}
	b.world.put(share, bbRec("web-b", func(r *simRec) { r.Skip = skip }))
	it := b.mustAdd(share, "")
	a := b.hostAnswer(it.ID, "2026-10-20T10:00:00Z", "2026-10-20T11:30:00Z")
	if len(a.Gaps) != 1 || a.Gaps[0].Kind != "short" {
		t.Fatalf("gaps = %+v", a.Gaps)
	}
	if len(a.Step.UntestedSpans) != 0 {
		t.Fatalf("a short gap made minutes untestable: %+v", a.Step.UntestedSpans)
	}
	if a.Step.Readiness == investigate.Ready || !strings.Contains(strings.Join(a.Step.Reasons, " "), "short gap") {
		t.Fatalf("step = %+v", a.Step)
	}
}

func TestBlackboxANoGapReadsWholeAndTheBusyStretchIsFound(t *testing.T) {
	b := newBBEnv(t, nil)
	b.world.put(share, bbRec("web-b", nil))
	it := b.mustAdd(share, "")
	a := b.hostAnswer(it.ID, "2026-10-20T10:00:00Z", "2026-10-20T11:30:00Z")
	if len(a.Gaps) != 0 || len(a.Step.UntestedSpans) != 0 {
		t.Fatalf("gaps = %+v", a.Gaps)
	}
	if a.H1 != investigate.Yes || a.TopProcess != "w3wp" || a.Onset == nil || a.Covered < 80 {
		t.Fatalf("answer = %+v", a)
	}
	if got := a.Onset.UTC(); got.Before(invT0.Add(80*time.Minute)) || got.After(invT0.Add(86*time.Minute)) {
		t.Fatalf("onset = %v", got)
	}
	// Bad windows.
	for _, body := range []string{
		`{"incident":{"start":"2026-10-20T11:30:00Z","end":"2026-10-20T10:00:00Z"},"reference":{"start":"2026-10-20T09:00:00Z","end":"2026-10-20T10:00:00Z"}}`,
		`{"incident":{"start":"2026-10-01T00:00:00Z","end":"2026-10-20T10:00:00Z"},"reference":{"start":"2026-10-20T09:00:00Z","end":"2026-10-20T10:00:00Z"}}`,
		`{}`,
	} {
		if res := b.post("/api/blackbox/"+it.ID+"/host-reading", body); res.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d", body, res.Code)
		}
	}
}

/* ---------------------------------------------------- the case hook */

func (b *bbEnv) caseWith(labels ...string) string {
	b.t.Helper()
	id := b.e.newCase("host")
	for _, l := range labels {
		res := b.post("/api/cases/"+id+"/sources", jb(map[string]string{"kind": "blackbox", "label": l}))
		if res.Code != http.StatusCreated {
			b.t.Fatalf("tag %s = %d %s", l, res.Code, res.Body)
		}
	}
	return id
}

func (b *bbEnv) investigate(id string) investigationAnswer {
	b.t.Helper()
	res := b.post("/api/cases/"+id+"/investigation", invPost(`,"releasesEntered":true`))
	if res.Code != http.StatusOK {
		b.t.Fatalf("investigation = %d %s", res.Code, res.Body)
	}
	var out investigationAnswer
	decodeBody(b.t, res, &out)
	return out
}

func warned(a investigationAnswer, part string) bool {
	for _, w := range a.Read.Warnings {
		if strings.Contains(w, part) {
			return true
		}
	}
	return false
}

func TestBlackboxCaseUsesTheListedBlackboxForStep9(t *testing.T) {
	b := newBBEnv(t, nil)
	skip := [][2]time.Time{{invT0.Add(100 * time.Minute), invT0.Add(103 * time.Minute)}}
	b.world.put(share, bbRec("web-b", func(r *simRec) { r.Skip = skip }))
	b.mustAdd(share, "")
	id := b.caseWith("web-b")
	a := b.investigate(id)
	if len(a.Result.Steps) != investigate.StepCount {
		t.Fatalf("steps = %d", len(a.Result.Steps))
	}
	st := a.Result.Steps[9]
	if len(st.UntestedSpans) != 1 || st.Readiness == investigate.Ready {
		t.Fatalf("Step 9 = %+v", st)
	}
	if len(a.Read.Warnings) != 0 {
		t.Fatalf("warnings = %v", a.Read.Warnings)
	}
	// A case with no Blackbox leaves Step 9 as it was: no spans, nothing read.
	plain := b.investigate(b.e.newCase("plain"))
	if len(plain.Result.Steps[9].UntestedSpans) != 0 || len(plain.Read.Warnings) != 0 {
		t.Fatalf("plain Step 9 = %+v %v", plain.Result.Steps[9], plain.Read.Warnings)
	}
}

func TestBlackboxCaseWarnings(t *testing.T) {
	b := newBBEnv(t, nil)
	b.world.put(share, bbRec("web-b", nil))
	b.world.put(`\\web-g\Bb`, bbRec("web-g", nil))
	b.mustAdd(share, "")
	b.mustAdd(`\\web-g\Bb`, "")

	// Two Blackboxes: the first is read and the page says so.
	if a := b.investigate(b.caseWith("web-b", "web-g")); !warned(a, "2 Blackboxes") {
		t.Fatalf("warnings = %v", a.Read.Warnings)
	}
	// A Blackbox that is not in the list.
	if a := b.investigate(b.caseWith("nowhere")); !warned(a, "no longer in the Blackbox list") || a.Result.Steps[9].Readiness == investigate.Ready {
		t.Fatalf("warnings = %v step = %+v", a.Read.Warnings, a.Result.Steps[9])
	}
	// A Blackbox in the list that cannot be read now.
	b.world.drop(share)
	if a := b.investigate(b.caseWith("web-b")); !warned(a, "could not be read") || len(a.Result.Steps[9].UntestedSpans) != 0 {
		t.Fatalf("warnings = %v step = %+v", a.Read.Warnings, a.Result.Steps[9])
	}
	// A tag that looks like a credential is refused before it can be saved.
	id := b.e.newCase("cred")
	if res := b.post("/api/cases/"+id+"/sources", `{"kind":"blackbox","label":"http://a:b@web-b:7788 password=x"}`); res.Code == http.StatusCreated {
		t.Fatalf("a credential-like label was saved: %s", res.Body)
	}
}

// Clumsy-user case 5: removed from this PC while a case reads it.
func TestBlackboxRemovedFromThisPCWhileACaseReadsIt(t *testing.T) {
	b := newBBEnv(t, nil)
	res := b.post("/api/blackbox/this-pc/install", `{"name":"web-a"}`)
	if res.Code != http.StatusCreated {
		t.Fatalf("install = %d %s", res.Code, res.Body)
	}
	id := b.caseWith("web-a")
	if a := b.investigate(id); len(a.Read.Warnings) != 0 {
		t.Fatalf("before: %v", a.Read.Warnings)
	}
	// The service goes away behind the list's back: the entry stays and says so.
	if _, err := b.pc.Remove(context.Background(), "web-a", false, ""); err != nil {
		t.Fatal(err)
	}
	l := b.list()
	if len(l.Items) != 1 || l.Items[0].State.State != blackbox.SNotPlaced || l.ThisPC.Installed {
		t.Fatalf("list = %+v this PC = %+v", l.Items, l.ThisPC)
	}
	a := b.investigate(id)
	if !warned(a, "could not be read") || a.Result.Steps[9].Readiness == investigate.Ready {
		t.Fatalf("warnings = %v step = %+v", a.Read.Warnings, a.Result.Steps[9])
	}
	// Now remove the entry too: the case says the Blackbox is no longer in the list.
	if res := b.e.json(http.MethodDelete, "/api/blackbox/"+l.Items[0].ID, ""); res.Code != http.StatusOK {
		t.Fatalf("delete = %d", res.Code)
	}
	if a := b.investigate(id); !warned(a, "no longer in the Blackbox list") {
		t.Fatalf("warnings = %v", a.Read.Warnings)
	}
	// The case itself is intact.
	if res := b.e.get("/api/cases/" + id); res.Code != http.StatusOK {
		t.Fatalf("case = %d", res.Code)
	}
}

/* ------------------------------------------------------------ this PC */

func TestBlackboxInstallSealsTheKeyAndNeverReturnsIt(t *testing.T) {
	b := newBBEnv(t, nil)
	res := b.post("/api/blackbox/this-pc/install", `{"name":"web-a","port":7790}`)
	if res.Code != http.StatusCreated {
		t.Fatalf("install = %d %s", res.Code, res.Body)
	}
	if strings.Contains(res.Body.String(), "ubk-") {
		t.Fatalf("the install answer carries the key: %s", res.Body)
	}
	var out struct {
		Installed bool   `json:"installed"`
		Name      string `json:"name"`
		Port      int    `json:"port"`
		Entry     bbItem `json:"entry"`
	}
	decodeBody(t, res, &out)
	if !out.Installed || out.Name != "web-a" || out.Port != 7790 || !out.Entry.ThisPC || !out.Entry.HasKey || out.Entry.Location != "http://127.0.0.1:7790" {
		t.Fatalf("install = %+v", out)
	}
	k, err := b.bbs.Key(out.Entry.ID)
	if err != nil || !strings.HasPrefix(k, "ubk-demo-") {
		t.Fatalf("sealed key = %q %v", k, err)
	}
	if raw, _ := os.ReadFile(b.path); bytes.Contains(raw, []byte(k)) {
		t.Fatal("the sealed file holds the key in the clear")
	}
	// The list reads it with the sealed key.
	l := b.list()
	if len(l.Items) != 1 || l.Items[0].State.State != blackbox.SRunning || !l.ThisPC.Installed || !l.ThisPC.Listed || l.ThisPC.ListID != out.Entry.ID {
		t.Fatalf("list = %+v %+v", l.Items, l.ThisPC)
	}
	if strings.Contains(b.e.get("/api/blackbox").Body.String(), "ubk-") {
		t.Fatal("the list carries a key")
	}
	// A second install while one is there is refused with the reason.
	f := bbFail(t, b.post("/api/blackbox/this-pc/install", `{"name":"web-a"}`), http.StatusUnprocessableEntity, string(blackbox.ReasonExists))
	if f.What == "" || f.Next == "" {
		t.Fatalf("failure = %+v", f)
	}
	// An install over an entry added by hand keeps that entry and gives it the key.
	if got := len(b.list().Items); got != 1 {
		t.Fatalf("%d entries", got)
	}
}

func TestBlackboxInstallOverAnEntryAddedByHand(t *testing.T) {
	b := newBBEnv(t, nil)
	b.world.put("http://127.0.0.1:7788", bbRec("web-a", func(r *simRec) { r.Key = "ubk-old-hand-added" }))
	hand := b.mustAdd("http://127.0.0.1:7788", "ubk-old-hand-added")
	if hand.ThisPC {
		t.Fatal("a hand-added entry started as this PC")
	}
	if res := b.post("/api/blackbox/this-pc/install", `{"name":"web-a"}`); res.Code != http.StatusCreated {
		t.Fatalf("install = %d %s", res.Code, res.Body)
	}
	l := b.list()
	if len(l.Items) != 1 || l.Items[0].ID != hand.ID || !l.Items[0].ThisPC {
		t.Fatalf("list = %+v", l.Items)
	}
	if k, _ := b.bbs.Key(hand.ID); !strings.HasPrefix(k, "ubk-demo-") {
		t.Fatalf("the entry kept the old key: %q", k)
	}
}

func TestBlackboxThisPCActions(t *testing.T) {
	b := newBBEnv(t, nil)
	b.post("/api/blackbox/this-pc/install", `{"name":"web-a"}`)
	it := b.list().Items[0]

	if res := b.post("/api/blackbox/this-pc/stop", ""); res.Code != http.StatusOK {
		t.Fatalf("stop = %d %s", res.Code, res.Body)
	}
	if got := b.list().Items[0].State; got.State != blackbox.SStopped || got.Readiness != "Cannot test yet" {
		t.Fatalf("state after stop = %+v", got)
	}
	if res := b.post("/api/blackbox/this-pc/start", ""); res.Code != http.StatusOK {
		t.Fatalf("start = %d %s", res.Code, res.Body)
	}
	if got := b.list().Items[0].State.State; got != blackbox.SRunning {
		t.Fatalf("state after start = %s", got)
	}
	res := b.post("/api/blackbox/this-pc/apply", `{"name":"web-a","intervalSeconds":30}`)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"restarted":true`) {
		t.Fatalf("apply = %d %s", res.Code, res.Body)
	}
	bbFail(t, b.post("/api/blackbox/this-pc/apply", `{"name":"web-a","intervalSeconds":7}`), http.StatusUnprocessableEntity, blackbox.ReasonInvalid)
	res = b.post("/api/blackbox/this-pc/snapshot", "")
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"recordings":4`) {
		t.Fatalf("snapshot = %d %s", res.Code, res.Body)
	}
	// Deleting the recordings needs the name typed.
	f := bbFail(t, b.post("/api/blackbox/this-pc/remove", `{"deleteRecordings":true,"confirmName":"nope"}`), http.StatusUnprocessableEntity, blackbox.ReasonConfirm)
	if f.Next == "" {
		t.Fatalf("failure = %+v", f)
	}
	if !b.pc.installed || len(b.list().Items) != 1 {
		t.Fatal("a refused remove changed something")
	}
	res = b.post("/api/blackbox/this-pc/remove", `{"deleteRecordings":true,"confirmName":"web-a"}`)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"recordingsDeleted":true`) {
		t.Fatalf("remove = %d %s", res.Code, res.Body)
	}
	if b.pc.installed || len(b.list().Items) != 0 {
		t.Fatal("the Blackbox is still there")
	}
	if _, err := b.bbs.Key(it.ID); !errors.Is(err, bbsaved.ErrNotFound) {
		t.Fatalf("the key outlived the entry: %v", err)
	}
	// Nothing is installed now.
	bbFail(t, b.post("/api/blackbox/this-pc/stop", ""), http.StatusUnprocessableEntity, blackbox.ReasonNotInstall)
	bbFail(t, b.post("/api/blackbox/this-pc/remove", `{}`), http.StatusUnprocessableEntity, blackbox.ReasonNotInstall)
	// Remove keeping the recordings is the default.
	b.post("/api/blackbox/this-pc/install", `{"name":"web-a"}`)
	res = b.post("/api/blackbox/this-pc/remove", `{}`)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"recordingsDeleted":false`) {
		t.Fatalf("remove = %d %s", res.Code, res.Body)
	}
}

func TestBlackboxThisPCFailureWords(t *testing.T) {
	b := newBBEnv(t, nil)
	cases := []struct {
		err    error
		code   int
		reason string
	}{
		{&blackbox.ThisPCError{Reason: blackbox.ReasonDeclined, Message: "Windows asked for permission and it was declined."}, http.StatusUnprocessableEntity, blackbox.ReasonDeclined},
		{&blackbox.ThisPCError{Reason: string(blackbox.ReasonNoAdmin), Message: "This account is not an administrator."}, http.StatusUnprocessableEntity, string(blackbox.ReasonNoAdmin)},
		{&blackbox.ThisPCError{Reason: string(blackbox.ReasonPortBusy), Message: "Port 7788 is in use."}, http.StatusUnprocessableEntity, string(blackbox.ReasonPortBusy)},
		{errors.New(`C:\secret\path exploded`), http.StatusInternalServerError, "other"},
	}
	for _, c := range cases {
		b.pc.failNext = c.err
		f := bbFail(t, b.post("/api/blackbox/this-pc/install", `{"name":"web-a"}`), c.code, c.reason)
		if f.What == "" || f.Next == "" {
			t.Fatalf("%s: no words: %+v", c.reason, f)
		}
		if strings.Contains(f.Error+f.What+f.Next, "secret") {
			t.Fatalf("an unknown error leaked its text: %+v", f)
		}
		if b.pc.installed || len(b.list().Items) != 0 {
			t.Fatalf("%s: a failed install left something", c.reason)
		}
	}
}

func TestBlackboxOnePCChangeAtATime(t *testing.T) {
	b := newBBEnv(t, nil)
	b.post("/api/blackbox/this-pc/install", `{"name":"web-a"}`)
	b.pc.entered = make(chan struct{}, 1)
	b.pc.hold = make(chan struct{})
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- b.post("/api/blackbox/this-pc/stop", "") }()
	select {
	case <-b.pc.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the first change never started")
	}
	f := bbFail(t, b.post("/api/blackbox/this-pc/start", ""), http.StatusConflict, "busy-here")
	if f.Next == "" {
		t.Fatalf("failure = %+v", f)
	}
	close(b.pc.hold)
	select {
	case res := <-done:
		if res.Code != http.StatusOK {
			t.Fatalf("stop = %d %s", res.Code, res.Body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the first change never finished")
	}
	for _, c := range b.pc.callLog() {
		if c == "start" {
			t.Fatal("the refused press still reached the helper")
		}
	}
}

func TestBlackboxWithoutAHelperOrWithoutTheHub(t *testing.T) {
	b := newBBEnv(t, func(d *BlackboxDeps) { d.ThisPC = nil; d.Program = nil })
	for _, p := range []string{"install", "start", "stop", "apply", "remove", "snapshot"} {
		bbFail(t, b.post("/api/blackbox/this-pc/"+p, `{"name":"web-a"}`), http.StatusServiceUnavailable, "no-helper")
	}
	if l := b.list(); l.ThisPC.Available || l.CanPack {
		t.Fatalf("list = %+v", l)
	}
	bbFail(t, b.post("/api/blackbox/pack", `{"name":"web-b"}`), http.StatusUnprocessableEntity, blackbox.ReasonNoProgram)

	bare := tagEnv(t, casestore.NewMemory(), &fakeRepository{})
	bbFail(t, bare.get("/api/blackbox"), http.StatusServiceUnavailable, "unavailable")
	bbFail(t, bare.json(http.MethodPost, "/api/blackbox", `{"location":"x"}`), http.StatusServiceUnavailable, "unavailable")
}

func TestBlackboxRoutesRefuseTheWrongMethod(t *testing.T) {
	b := newBBEnv(t, nil)
	for _, c := range [][2]string{
		{http.MethodGet, "/api/blackbox/this-pc/install"}, {http.MethodPut, "/api/blackbox"},
		{http.MethodGet, "/api/blackbox/pack"}, {http.MethodPost, "/api/blackbox/this-pc"},
	} {
		if res := b.e.json(c[0], c[1], ""); res.Code != http.StatusMethodNotAllowed && res.Code != http.StatusNotFound {
			t.Fatalf("%s %s = %d", c[0], c[1], res.Code)
		}
	}
	// No token, no answer.
	if res := request(b.e.h, http.MethodGet, "/api/blackbox", "", false); res.Code == http.StatusOK {
		t.Fatalf("an unauthenticated list answered: %d", res.Code)
	}
}

/* ------------------------------------------------------------- the pack */

func TestBlackboxPackHasNoReadKey(t *testing.T) {
	b := newBBEnv(t, nil)
	res := b.post("/api/blackbox/pack", `{"name":"web-b","port":7791}`)
	if res.Code != http.StatusOK {
		t.Fatalf("pack = %d %s", res.Code, res.Body)
	}
	if ct := res.Header().Get("Content-Type"); ct != "application/zip" {
		t.Fatalf("content type %q", ct)
	}
	if cd := res.Header().Get("Content-Disposition"); !strings.Contains(cd, "unravis-blackbox-web-b.zip") {
		t.Fatalf("disposition %q", cd)
	}
	zr, err := zip.NewReader(bytes.NewReader(res.Body.Bytes()), int64(res.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	var cfg string
	hasProgram := false
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(rc)
		rc.Close()
		if bytes.Contains(data, []byte("ubk-")) || bytes.Contains(bytes.ToLower(data), []byte("readkey\":\"")) {
			t.Fatalf("%s carries a read key", f.Name)
		}
		if strings.HasSuffix(f.Name, "blackbox.json") {
			cfg = string(data)
		}
		if bytes.Contains(data, []byte("MZ test recorder program")) {
			hasProgram = true
		}
	}
	if !hasProgram || !strings.Contains(cfg, `"name": "web-b"`) && !strings.Contains(cfg, `"name":"web-b"`) {
		t.Fatalf("pack contents: program %v config %q", hasProgram, cfg)
	}
	if !strings.Contains(cfg, "7791") {
		t.Fatalf("the settings were not used: %s", cfg)
	}
	// Settings the recorder would refuse are refused here.
	bbFail(t, b.post("/api/blackbox/pack", `{"name":"web-b","intervalSeconds":7}`), http.StatusUnprocessableEntity, blackbox.ReasonInvalid)
	bbFail(t, b.post("/api/blackbox/pack", `{"name":"web b!"}`), http.StatusUnprocessableEntity, blackbox.ReasonInvalid)
	bbFail(t, b.post("/api/blackbox/validate", `{"name":"web-b","keepDays":0}`), http.StatusUnprocessableEntity, blackbox.ReasonInvalid)
	if res := b.post("/api/blackbox/validate", `{"name":"web-b"}`); res.Code != http.StatusOK {
		t.Fatalf("validate = %d %s", res.Code, res.Body)
	}
}

/* ------------------------------------------ the key never leaves the list */

// A read key must not be in any case file or export: the case carries only the
// Blackbox name as a label.
func TestBlackboxReadKeyIsInNoCaseFileOrExport(t *testing.T) {
	pc := newCFSPC(t, cfsPassA, true)
	world := newSimWorld()
	now := invT0.Add(3*time.Hour + 10*time.Second)
	world.put("http://web-c:7788", bbRec("web-c", func(r *simRec) { r.Key = bbKeyA }))
	bbs, err := bbsaved.New(filepath.Join(t.TempDir(), "blackboxes.dat"), storetest.RandomProtector())
	if err != nil {
		t.Fatal(err)
	}
	sim := newSimPC(world, now)
	pc.h = New(&fakeRepository{requestPages: []audit.Page{okPage(invRows())}}, testAssets(), testToken, false,
		WithCases(pc.store, mustHosts(t, hostsDoc), importer.NewRegistry(toyImporter{name: "toy", score: 90})),
		WithMasterKey(pc.sess),
		WithBlackbox(BlackboxDeps{Store: bbs, ThisPC: sim, Scan: sim.Scan, Open: world.Open, Now: func() time.Time { return now }}),
	)

	res := pc.do(http.MethodPost, "/api/blackbox", jb(map[string]string{"location": "web-c:7788", "key": bbKeyA}))
	if res.Code != http.StatusCreated {
		t.Fatalf("add = %d %s", res.Code, res.Body)
	}
	id := pc.seed("Blackbox case", 6)
	if res := pc.do(http.MethodPost, "/api/cases/"+id+"/sources", `{"kind":"blackbox","label":"web-c"}`); res.Code != http.StatusCreated {
		t.Fatalf("tag = %d %s", res.Code, res.Body)
	}
	inv := pc.do(http.MethodPost, "/api/cases/"+id+"/investigation", invPost(`,"releasesEntered":true`))
	if inv.Code != http.StatusOK {
		t.Fatalf("investigation = %d %s", inv.Code, inv.Body)
	}
	if a := (investigationAnswer{}); true {
		decodeBody(t, inv, &a)
		if len(a.Result.Steps[9].Findings) == 0 && a.Result.Steps[9].Readiness == "" {
			t.Fatalf("Step 9 did not run: %+v", a.Result.Steps[9])
		}
	}

	outputs := map[string]string{
		"investigation": inv.Body.String(),
		"case":          pc.do(http.MethodGet, "/api/cases/"+id, "").Body.String(),
		"sources":       pc.do(http.MethodGet, "/api/cases/"+id+"/sources", "").Body.String(),
		"list":          pc.do(http.MethodGet, "/api/blackbox", "").Body.String(),
		"preview":       pc.do(http.MethodPost, "/api/export/preview", `{"caseId":`+q(id)+`}`).Body.String(),
		"logs":          pc.logs.String(),
	}
	// The tag the store holds is a label and nothing else.
	c, err := pc.store.Open(context.Background(), casestore.CaseID(id))
	if err != nil {
		t.Fatal(err)
	}
	tags, err := c.(casestore.Tagger).Sources(context.Background())
	c.Close()
	if err != nil || len(tags) != 1 || tags[0].Kind != "blackbox" || tags[0].Label != "web-c" {
		t.Fatalf("tags = %+v %v", tags, err)
	}
	outputs["tags"] = jb(tags)

	// The export, and every file in the case folder.
	out := t0Dir(t)
	ex := pc.export(id, out, cfsPassA)
	if ex.Code != http.StatusOK {
		t.Fatalf("export = %d %s", ex.Code, ex.Body)
	}
	outputs["export reply"] = ex.Body.String()
	for _, root := range []string{out, pc.root, pc.dir} {
		_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || info.Size() > 64<<20 {
				return nil
			}
			if data, rerr := os.ReadFile(p); rerr == nil {
				outputs[p] = string(data)
			}
			return nil
		})
	}
	for what, body := range outputs {
		if strings.Contains(body, bbKeyA) || strings.Contains(body, "SENTINEL-read-key") {
			t.Errorf("the read key is in %s", what)
		}
	}
	if k, err := bbs.Key(firstID(t, bbs)); err != nil || k != bbKeyA {
		t.Fatalf("the key is not where it belongs: %q %v", k, err)
	}
}

func firstID(t *testing.T, s BlackboxStore) string {
	t.Helper()
	es, err := s.List()
	if err != nil || len(es) == 0 {
		t.Fatalf("list = %v %v", es, err)
	}
	return es[0].ID
}

func TestBlackboxFailureAnswersNeverCarryTheKeyText(t *testing.T) {
	b := newBBEnv(t, nil)
	b.world.put("http://web-c:7788", bbRec("web-c", func(r *simRec) { r.Key = bbKeyA }))
	for _, key := range []string{bbKeyB, bbKeyA + "x"} {
		res := b.add("web-c:7788", key)
		if strings.Contains(res.Body.String(), "SENTINEL") {
			t.Fatalf("a refusal echoed the key: %s", res.Body)
		}
	}
}

// Clumsy-user extras: a double click on Forget, a key typed for a folder, a key made only of spaces.
func TestBlackboxClumsyRepeatsAreAnswered(t *testing.T) {
	b := newBBEnv(t, nil)
	b.world.put(share, bbRec("web-b", nil))
	it := b.mustAdd(share, "")
	if res := b.e.json(http.MethodDelete, "/api/blackbox/"+it.ID, ""); res.Code != http.StatusOK {
		t.Fatalf("first delete = %d %s", res.Code, res.Body)
	}
	bbFail(t, b.e.json(http.MethodDelete, "/api/blackbox/"+it.ID, ""), http.StatusNotFound, "not-listed")
	// A key for a share is refused and nothing is stored.
	it = b.mustAdd(share, "")
	res := b.e.json(http.MethodPut, "/api/blackbox/"+it.ID+"/key", `{"key":"ubk-not-a-real-key"}`)
	bbFail(t, res, http.StatusUnprocessableEntity, "invalid")
	if strings.Contains(res.Body.String(), "ubk-not-a-real-key") {
		t.Fatalf("the typed key was echoed: %s", res.Body)
	}
	bbFail(t, b.e.json(http.MethodPut, "/api/blackbox/"+it.ID+"/key", `{"key":"   "}`), http.StatusUnprocessableEntity, "invalid")
	// Adding an empty place or only spaces is refused.
	if res := b.add("   ", ""); res.Code < 400 || res.Code >= 500 {
		t.Fatalf("empty place = %d %s", res.Code, res.Body)
	}
}

// F3: the read key of the Blackbox installed on this PC is shown only on a press of
// Show or Copy (A33, wireframe 65). It is never in the list, a check, a failure or the
// sealed file, and a typed-URL entry never gives its key back.
func TestBlackboxRevealKeyOnlyForThisPC(t *testing.T) {
	b := newBBEnv(t, nil)
	res := b.post("/api/blackbox/this-pc/install", `{"name":"web-a"}`)
	if res.Code != http.StatusCreated {
		t.Fatalf("install = %d %s", res.Code, res.Body)
	}
	it := b.list().Items[0]
	sealed, err := b.bbs.Key(it.ID)
	if err != nil || sealed == "" {
		t.Fatalf("sealed key = %q %v", sealed, err)
	}

	got := b.post("/api/blackbox/"+it.ID+"/reveal-key", "")
	if got.Code != http.StatusOK {
		t.Fatalf("reveal = %d %s", got.Code, got.Body)
	}
	var out bbRevealKeyAnswer
	decodeBody(t, got, &out)
	if out.Key != sealed {
		t.Fatalf("revealed %q, want the sealed key", out.Key)
	}
	if cc := got.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Fatalf("Cache-Control = %q", cc)
	}

	// The key is in no other answer.
	for _, p := range []string{"/api/blackbox", "/api/blackbox/this-pc"} {
		if body := b.e.get(p).Body.String(); strings.Contains(body, sealed) {
			t.Fatalf("%s carries the key", p)
		}
	}
	if raw, _ := os.ReadFile(b.path); bytes.Contains(raw, []byte(sealed)) {
		t.Fatal("the sealed file holds the key in the clear")
	}
	// A second install refusal and a repeat reveal do not leak it into a failure.
	if f := b.post("/api/blackbox/this-pc/install", `{"name":"web-a"}`); strings.Contains(f.Body.String(), sealed) {
		t.Fatalf("a refusal carried the key: %s", f.Body)
	}
}

func TestBlackboxRevealKeyRefusedForTypedEntryAndUnknownId(t *testing.T) {
	b := newBBEnv(t, nil)
	b.world.put("http://web-c:7788", bbRec("web-c", func(r *simRec) { r.Key = bbKeyA }))
	it := b.mustAdd("web-c:7788", bbKeyA)

	res := b.post("/api/blackbox/"+it.ID+"/reveal-key", "")
	bbFail(t, res, http.StatusForbidden, "not-this-pc")
	if strings.Contains(res.Body.String(), "SENTINEL") {
		t.Fatalf("a refusal carried the key: %s", res.Body)
	}
	bbFail(t, b.post("/api/blackbox/bb-nope/reveal-key", ""), http.StatusNotFound, "not-listed")
	// GET is not allowed: the key is never reachable by a link.
	if got := b.e.get("/api/blackbox/" + it.ID + "/reveal-key"); got.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET reveal = %d", got.Code)
	}
}

/* ------------------------------------------------- P9 fix round */

// A garbage Listens on address and a system folder are refused in plain words on
// every route that checks the settings, with no file or field name in the sentence.
func TestBlackboxBadSettingsAreRefusedInPlainWords(t *testing.T) {
	b := newBBEnv(t, nil)
	bodies := map[string]string{
		"bind":   `{"name":"web-b","bind":"not an address !!"}`,
		"folder": `{"name":"web-b","folder":"C:\\Windows\\System32"}`,
	}
	for _, route := range []string{"/api/blackbox/validate", "/api/blackbox/pack", "/api/blackbox/this-pc/apply", "/api/blackbox/this-pc/install"} {
		for label, body := range bodies {
			if label == "folder" && runtime.GOOS != "windows" {
				continue
			}
			f := bbFail(t, b.post(route, body), http.StatusUnprocessableEntity, blackbox.ReasonInvalid)
			if f.What == "" || strings.Contains(f.What, "blackbox.json") || strings.Contains(f.What, `"`) {
				t.Errorf("%s %s: what = %q", route, label, f.What)
			}
		}
	}
	if len(b.list().Items) != 0 {
		t.Fatal("a refused install left an entry")
	}
}

// Clumsy-user finding 11: the same URL again with a wrong key is a duplicate, not a
// refused key, and the saved key stays.
func TestBlackboxDuplicateIsAnsweredBeforeTheKeyIsChecked(t *testing.T) {
	b := newBBEnv(t, nil)
	b.world.put("http://web-c:7788", bbRec("web-c", func(r *simRec) { r.Key = bbKeyA }))
	first := b.mustAdd("web-c:7788", bbKeyA)
	f := bbFail(t, b.add("web-c:7788", bbKeyB), http.StatusConflict, "duplicate")
	if f.Existing == nil || f.Existing.ID != first.ID {
		t.Fatalf("existing = %+v", f.Existing)
	}
	if k, _ := b.bbs.Key(first.ID); k != bbKeyA {
		t.Fatalf("saved key changed: %q", k)
	}
	// Trying without saving still checks the key.
	bbFail(t, b.post("/api/blackbox/test", jb(map[string]string{"location": "web-c:7788", "key": bbKeyB})), http.StatusUnprocessableEntity, "key")
}

// Finding 6: a Blackbox that is Incomplete says how many files were not read, so the
// facts do not read as "no gaps".
func TestBlackboxIncompleteFactsCountTheUnreadFiles(t *testing.T) {
	b := newBBEnv(t, nil)
	b.setNow(invT0.Add(3*time.Hour + 20*time.Minute + 10*time.Second))
	b.world.put(`\\web-f\Blackbox`, bbRec("web-f", func(r *simRec) {
		r.End = invT0.Add(3*time.Hour + 20*time.Minute)
		r.DropBefore = 5 * time.Minute
	}))
	it := b.mustAdd(`\\web-f\Blackbox`, "")
	if it.State.State != blackbox.SIncomplete {
		t.Fatalf("state = %+v", it.State)
	}
	if it.Facts == nil || it.Facts.Unread < 1 {
		t.Fatalf("facts = %+v", it.Facts)
	}
	b.world.put(share, bbRec("web-b", nil))
	if whole := b.mustAdd(share, ""); whole.Facts == nil || whole.Facts.Unread != 0 {
		t.Fatalf("a whole read has unread files: %+v", whole.Facts)
	}
}
