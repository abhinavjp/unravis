package discover_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"api-audit-explorer/internal/discover"
	"api-audit-explorer/internal/discover/fake"
	"api-audit-explorer/internal/importer"
)

// verifyEnv is a fake host with the four fixtures from testdata/verify on it.
func verifyEnv(t *testing.T) (*fake.Env, map[string]string) {
	t.Helper()
	e := fake.New()
	mod := time.Date(2001, 9, 11, 0, 0, 0, 0, time.UTC)
	paths := map[string]string{
		"good_iis":     `D:\Logs\iis\u_ex010911.log`,
		"good_log4net": `D:\Apps\web-a\app.log`,
		"empty":        `D:\Logs\empty\app.log`,
		"binary":       `D:\Logs\binary\app.log`,
	}
	files := map[string]string{
		"good_iis": "good_iis.log", "good_log4net": "good_log4net.log", "empty": "empty.log", "binary": "binary.log",
	}
	for k, p := range paths {
		b, err := os.ReadFile(filepath.Join("testdata", "verify", files[k]))
		if err != nil {
			t.Fatal(err)
		}
		e.FS.AddFile(p, int64(len(b)), mod, b)
	}
	return e, paths
}

func scanOne(t *testing.T, e discover.Env, fs ...discover.Finding) discover.Scan {
	t.Helper()
	return discover.Run(context.Background(), e, []discover.Detector{fixed("iis", fs...)})
}

func TestVerifyGoodFileIsReady(t *testing.T) {
	e, p := verifyEnv(t)
	got := scanOne(t, envWith(e), finding("iis", "iis-w3c", "u_ex010911.log", p["good_iis"]))
	if len(got.Findings) != 1 {
		t.Fatalf("findings %+v", got.Findings)
	}
	f := got.Findings[0]
	if !f.Verified || f.Importer != "iis-w3c" || f.Unreadable != "" {
		t.Fatalf("good file: verified=%v importer=%q unreadable=%q", f.Verified, f.Importer, f.Unreadable)
	}
	if !got.Complete {
		t.Error("a clean scan must stay complete")
	}
}

func TestVerifyUnreadableFindingsKeepTheirRow(t *testing.T) {
	e, p := verifyEnv(t)
	cases := []struct {
		name, importer, path, reason string
	}{
		{"empty file", "log4net-text", p["empty"], "discover.emptyfile.shows"},
		{"binary file", "log4net-text", p["binary"], "discover.wrongformat.shows"},
		{"IIS file named as log4net", "log4net-text", p["good_iis"], "discover.wrongformat.shows"},
		{"log4net file named as IIS", "iis-w3c", p["good_log4net"], "discover.wrongformat.shows"},
		{"missing file", "iis-w3c", `D:\Logs\gone\u_ex010911.log`, "discover.cannotopen.shows"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := scanOne(t, envWith(e), finding("iis", c.importer, c.name, c.path))
			if len(got.Findings) != 1 {
				t.Fatalf("the row must stay: %+v", got.Findings)
			}
			f := got.Findings[0]
			if f.Verified || f.Importer != "" || f.Unreadable != c.reason {
				t.Fatalf("verified=%v importer=%q unreadable=%q, want not verified, no importer, %q", f.Verified, f.Importer, f.Unreadable, c.reason)
			}
			if f.Display != c.name || f.Detector != "iis" || f.Shows == "" {
				t.Errorf("the rest of the row must be unchanged: %+v", f)
			}
			if txt, ok := discover.WordingText(f.Unreadable); !ok || txt == "" {
				t.Errorf("reason key %q has no wording", f.Unreadable)
			}
		})
	}
}

func TestVerifyDeniedFileIsDemoted(t *testing.T) {
	e, p := verifyEnv(t)
	e.FS.Deny(`D:\Logs\iis`)
	got := scanOne(t, envWith(e), finding("iis", "iis-w3c", "x", p["good_iis"]))
	f := got.Findings[0]
	if f.Verified || f.Importer != "" || f.Unreadable != "discover.cannotopen.shows" {
		t.Fatalf("%+v", f)
	}
}

// A rotated set whose current file is still empty is judged by the others too.
func TestVerifyRotatedSetPassesWhenAnyFileReads(t *testing.T) {
	e, p := verifyEnv(t)
	got := scanOne(t, envWith(e), finding("iis", "iis-w3c", "set", p["empty"], p["good_iis"], p["empty"]))
	if f := got.Findings[0]; !f.Verified || f.Importer != "iis-w3c" {
		t.Fatalf("%+v", f)
	}
	// All samples empty: the empty reason, not the format one.
	got = scanOne(t, envWith(e), finding("iis", "iis-w3c", "set", p["empty"], p["empty"]))
	if f := got.Findings[0]; f.Verified || f.Unreadable != "discover.emptyfile.shows" {
		t.Fatalf("%+v", f)
	}
}

// countingFS counts Head calls and the largest size asked for.
type countingFS struct {
	discover.FS
	heads  atomic.Int64
	maxAsk atomic.Int64
}

func (c *countingFS) Head(path string, n int) ([]byte, error) {
	c.heads.Add(1)
	for {
		cur := c.maxAsk.Load()
		if int64(n) <= cur || c.maxAsk.CompareAndSwap(cur, int64(n)) {
			break
		}
	}
	return c.FS.Head(path, n)
}

func TestVerifyReadsAFewBoundedHeads(t *testing.T) {
	e, p := verifyEnv(t)
	env := envWith(e)
	c := &countingFS{FS: env.FS}
	env.FS = c
	many := make([]string, 0, 40)
	for i := 0; i < 40; i++ {
		many = append(many, p["good_iis"])
	}
	// Distinct names so the merge keeps them apart is not needed: one finding, 40 paths.
	got := scanOne(t, env, finding("iis", "iis-w3c", "big", many...))
	if !got.Findings[0].Verified {
		t.Fatalf("%+v", got.Findings[0])
	}
	if n := c.heads.Load(); n > 3 {
		t.Errorf("%d heads read for one finding, want at most 3", n)
	}
	if m := c.maxAsk.Load(); m > importer.HeadSize || m <= 0 {
		t.Errorf("asked for %d bytes, want a bounded head of at most %d", m, importer.HeadSize)
	}
}

func TestVerifyEventChannels(t *testing.T) {
	e, _ := verifyEnv(t)
	e.Channels.Names = []string{"Application", "System", "Security"}
	e.Channels.Denied = map[string]error{"Security": discover.ErrAccessDenied}
	ch := func(name string) discover.Finding {
		return discover.Finding{Detector: "events", Kind: discover.KindEventChannel, Importer: "win-events", Display: name,
			Confidence: 70, Shows: "discover.events.shows"}
	}
	got := discover.Run(context.Background(), envWith(e), []discover.Detector{
		fixed("events", ch("Application"), ch("system"), ch("Security"), ch("Gone")),
	})
	by := map[string]discover.Finding{}
	for _, f := range got.Findings {
		by[f.Display] = f
	}
	for _, name := range []string{"Application", "system"} {
		if f := by[name]; !f.Verified || f.Importer != "win-events" {
			t.Errorf("%s: %+v", name, f)
		}
	}
	for _, name := range []string{"Security", "Gone"} {
		if f := by[name]; f.Verified || f.Importer != "" || f.Unreadable != "discover.cannotopen.shows" {
			t.Errorf("%s: %+v", name, f)
		}
	}
}

// Findings that were never selectable files are left exactly as the detector made them.
func TestVerifyLeavesOtherKindsAlone(t *testing.T) {
	e, _ := verifyEnv(t)
	live := discover.Finding{Detector: "host", Kind: discover.KindLiveSeries, Importer: "win-live", Display: "live", Shows: "discover.live.shows"}
	exp := discover.Finding{Detector: "host", Kind: discover.KindExportNeeded, Display: "blg", Shows: "discover.perfmon.shows", How: "discover.perfmon.how"}
	got := discover.Run(context.Background(), envWith(e), []discover.Detector{fixed("host", live, exp)})
	if len(got.Findings) != 2 {
		t.Fatalf("%+v", got.Findings)
	}
	for _, f := range got.Findings {
		if f.Verified || f.Unreadable != "" {
			t.Errorf("%s: touched %+v", f.Display, f)
		}
		if f.Display == "live" && f.Importer != "win-live" {
			t.Errorf("live series lost its importer")
		}
	}
}

func TestVerifyJSONFields(t *testing.T) {
	e, p := verifyEnv(t)
	got := scanOne(t, envWith(e),
		finding("iis", "iis-w3c", "ok", p["good_iis"]),
		finding("iis", "log4net-text", "bad", p["empty"]))
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Findings []map[string]any `json:"findings"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, f := range m.Findings {
		v, hasV := f["verified"]
		if !hasV {
			t.Fatalf("verified missing from %v", f)
		}
		switch f["display"] {
		case "ok":
			if v != true {
				t.Errorf("ok: %v", f)
			}
			if _, has := f["unreadable"]; has {
				t.Error("unreadable must be omitted on a verified finding")
			}
		case "bad":
			if v != false || f["unreadable"] != "discover.emptyfile.shows" {
				t.Errorf("bad: %v", f)
			}
			if _, has := f["importer"]; has {
				t.Error("importer must be omitted on a demoted finding")
			}
		}
	}
	if strings.Contains(string(b), `D:\\`) || strings.Contains(string(b), "Logs") {
		t.Fatalf("a path reached the JSON: %s", b)
	}
}

func TestVerifyDoesNotChangeOrderOrIDs(t *testing.T) {
	e, p := verifyEnv(t)
	got := scanOne(t, envWith(e),
		finding("iis", "iis-w3c", "a", p["good_iis"]),
		finding("iis", "log4net-text", "b", p["empty"]),
		finding("iis", "log4net-text", "c", p["good_log4net"]))
	if len(got.Findings) != 3 {
		t.Fatalf("%+v", got.Findings)
	}
	ready := 0
	for _, f := range got.Findings {
		if f.Verified {
			ready++
		}
		if len(f.ID) != 26 {
			t.Errorf("ID %q", f.ID)
		}
	}
	if ready != 2 {
		t.Errorf("%d ready, want 2", ready)
	}
}

// --- budget and cancellation ---

// blockingFS holds every Head until release closes, like a share that hangs.
type blockingFS struct {
	discover.FS
	release chan struct{}
	started atomic.Int64
}

func (b *blockingFS) Head(path string, n int) ([]byte, error) {
	b.started.Add(1)
	<-b.release
	return b.FS.Head(path, n)
}

// Distinct findings need distinct path sets or the merge folds them together.
func distinctFindings(e *fake.Env, content []byte, n int) []discover.Finding {
	mod := time.Date(2001, 9, 11, 0, 0, 0, 0, time.UTC)
	out := make([]discover.Finding, 0, n)
	for i := 0; i < n; i++ {
		p := fmt.Sprintf(`D:\Logs\site%03d\u_ex010911.log`, i)
		e.FS.AddFile(p, int64(len(content)), mod, content)
		out = append(out, finding("iis", "iis-w3c", fmt.Sprintf("site %d", i), p))
	}
	return out
}

func TestVerify200FindingsFinishInsideTheBudget(t *testing.T) {
	e, _ := verifyEnv(t)
	good, _ := os.ReadFile(filepath.Join("testdata", "verify", "good_iis.log"))
	fs := distinctFindings(e, good, 200)
	start := time.Now()
	got := discover.RunWith(context.Background(), envWith(e), []discover.Detector{fixed("iis", fs...)},
		discover.Limits{PerDetector: 10 * time.Second, Global: 20 * time.Second})
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("200 findings took %v", d)
	}
	if len(got.Findings) != 200 || !got.Complete {
		t.Fatalf("%d findings, complete=%v", len(got.Findings), got.Complete)
	}
	for _, f := range got.Findings {
		if !f.Verified {
			t.Fatalf("not verified: %+v", f)
		}
	}
}

func TestVerifyTimeBudgetDemotesAndDoesNotWait(t *testing.T) {
	e, _ := verifyEnv(t)
	good, _ := os.ReadFile(filepath.Join("testdata", "verify", "good_iis.log"))
	fs := distinctFindings(e, good, 200)
	env := envWith(e)
	b := &blockingFS{FS: env.FS, release: make(chan struct{})}
	env.FS = b
	t.Cleanup(func() { close(b.release) })

	start := time.Now()
	got := discover.RunWith(context.Background(), env, []discover.Detector{fixed("iis", fs...)},
		discover.Limits{PerDetector: 10 * time.Second, Global: 150 * time.Millisecond})
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("a hung file system held the scan for %v", d)
	}
	if got.Complete {
		t.Error("a cut-off verify pass must leave the scan incomplete")
	}
	if len(got.Findings) != 200 {
		t.Fatalf("%d findings", len(got.Findings))
	}
	for _, f := range got.Findings {
		if f.Verified || f.Importer != "" || f.Unreadable != "discover.verifytimeout.shows" {
			t.Fatalf("a finding was kept ready or mislabelled: %+v", f)
		}
	}
	if n := b.started.Load(); n > 8 {
		t.Errorf("%d checks started: the worker bound was not respected", n)
	}
}

func TestVerifyStopsWhenTheContextIsCancelled(t *testing.T) {
	e, _ := verifyEnv(t)
	good, _ := os.ReadFile(filepath.Join("testdata", "verify", "good_iis.log"))
	fs := distinctFindings(e, good, 50)
	env := envWith(e)
	b := &blockingFS{FS: env.FS, release: make(chan struct{})}
	env.FS = b
	t.Cleanup(func() { close(b.release) })

	ctx, cancel := context.WithCancel(context.Background())
	var once sync.Once
	go func() {
		for b.started.Load() == 0 {
			time.Sleep(time.Millisecond)
		}
		once.Do(cancel)
	}()
	done := make(chan discover.Scan, 1)
	go func() {
		done <- discover.Run(ctx, env, []discover.Detector{fixed("iis", fs...)})
	}()
	select {
	case got := <-done:
		for _, f := range got.Findings {
			if f.Verified || f.Unreadable != "discover.verifytimeout.shows" {
				t.Fatalf("%+v", f)
			}
		}
		if got.Complete {
			t.Error("cancelled scan must not be complete")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not stop the verify pass")
	}
}

type panicFS struct{ discover.FS }

func (panicFS) Head(string, int) ([]byte, error) { panic("SENTINEL-7f3a") }

func TestVerifySurvivesAPanic(t *testing.T) {
	e, p := verifyEnv(t)
	env := envWith(e)
	env.FS = panicFS{env.FS}
	got := scanOne(t, env, finding("iis", "iis-w3c", "x", p["good_iis"]))
	f := got.Findings[0]
	if f.Verified || f.Importer != "" || f.Unreadable != "discover.cannotopen.shows" {
		t.Fatalf("%+v", f)
	}
	b, _ := json.Marshal(got)
	if strings.Contains(string(b), "SENTINEL") {
		t.Fatal("panic text reached the scan")
	}
}
