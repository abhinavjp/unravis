package blackbox

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func stManifest(last time.Time, mut func(*Manifest)) Manifest {
	first := last.Add(-time.Hour)
	m := Manifest{Format: FormatVersion, Name: "web-b", IntervalSeconds: 15, State: ManifestRunning, FirstSample: &first, LastSample: &last, Created: first}
	if mut != nil {
		mut(&m)
	}
	return m
}

func stReading(m Manifest, now time.Time, mut func(*Reading)) *Reading {
	r := &Reading{Manifest: m, ReadAt: now}
	if mut != nil {
		mut(r)
	}
	return r
}

func pct(v float64) *float64 { return &v }

func derive(r *Reading, rep *ReadReport) StateInfo {
	return DeriveState(StateInput{Placed: true, Reading: r, Report: rep})
}

func TestEveryStateIsReachableWithItsWords(t *testing.T) {
	now := t0.Add(2 * time.Hour)
	fresh := now.Add(-10 * time.Second)
	cases := []struct {
		name      string
		in        StateInput
		state     PageState
		readiness string
		tone      string
	}{
		{"running", StateInput{Placed: true, Reading: stReading(stManifest(fresh, nil), now, nil)}, SRunning, WordReady, ToneGood},
		{"running late", StateInput{Placed: true, Reading: stReading(stManifest(now.Add(-time.Minute), nil), now, nil)}, SRunningLate, WordPartlyReady, ToneWarn},
		{"stopped", StateInput{Placed: true, Reading: stReading(stManifest(fresh, func(m *Manifest) { m.State = ManifestStopped }), now, nil)}, SStopped, WordPartlyReady, ToneIdle},
		{"disk low by status", StateInput{Placed: true, Reading: stReading(stManifest(fresh, nil), now, func(r *Reading) { r.Status = &StatusDoc{FreePercent: pct(7)} })}, SDiskLow, WordPartlyReady, ToneWarn},
		{"disk low by pause", StateInput{Placed: true, Reading: stReading(stManifest(fresh, func(m *Manifest) {
			m.Events = []Event{{T: fresh, Kind: EventPausedDiskLow}}
		}), now, nil)}, SDiskLow, WordPartlyReady, ToneBad},
		{"clock differs", StateInput{Placed: true, Reading: stReading(stManifest(fresh, nil), now, func(r *Reading) { r.ServerTime = now.Add(5 * time.Second); r.Status = &StatusDoc{} })}, SClockDiffers, WordPartlyReady, ToneWarn},
		{"not found", StateInput{Placed: true, Err: readErr(ReadNotFound, "No Blackbox is in that folder.", nil)}, SNotFound, WordCannotTestYet, ToneBad},
		{"unreachable", StateInput{Placed: true, Err: readErr(ReadUnreachable, "The share did not answer.", nil)}, SUnreachable, WordCannotTestYet, ToneBad},
		{"incomplete", StateInput{Placed: true, Reading: stReading(stManifest(fresh, nil), now, nil), Report: &ReadReport{Dropped: []string{"bb-x"}}}, SIncomplete, WordPartlyReady, ToneWarn},
		{"not placed", StateInput{Placed: false}, SNotPlaced, WordCannotTestYet, ToneIdle},
	}
	seen := map[PageState]bool{}
	for _, c := range cases {
		si := DeriveState(c.in)
		if si.State != c.state || si.Readiness != c.readiness || si.Tone != c.tone {
			t.Errorf("%s: got %+v", c.name, si)
		}
		if si.Label == "" || si.Detail == "" || si.Label != c.state.Label() {
			t.Errorf("%s: words missing: %+v", c.name, si)
		}
		seen[si.State] = true
	}
	for _, s := range AllStates() {
		if !seen[s] {
			t.Errorf("state %s is not covered", s)
		}
	}
}

func TestStateLabelsAreTheDocumentedWords(t *testing.T) {
	want := map[PageState]string{SRunning: "Running", SRunningLate: "Running late", SStopped: "Stopped", SDiskLow: "Disk low",
		SClockDiffers: "Clock differs", SNotFound: "Not found", SUnreachable: "Unreachable", SIncomplete: "Incomplete", SNotPlaced: "Not placed"}
	if len(AllStates()) != len(want) {
		t.Fatalf("%d states", len(AllStates()))
	}
	for s, l := range want {
		if s.Label() != l {
			t.Errorf("%s -> %q", s, s.Label())
		}
	}
}

func TestRunningLateIsMoreThanThreeIntervals(t *testing.T) {
	now := t0.Add(2 * time.Hour)
	for _, c := range []struct {
		age  time.Duration
		want PageState
	}{{45 * time.Second, SRunning}, {46 * time.Second, SRunningLate}, {10 * time.Minute, SRunningLate}} {
		si := derive(stReading(stManifest(now.Add(-c.age), nil), now, nil), nil)
		if si.State != c.want {
			t.Errorf("age %v: %s want %s", c.age, si.State, c.want)
		}
	}
}

// Lateness is judged on the recorder's clock when it is known, so a PC whose own
// clock is wrong does not make a healthy recorder look late.
func TestLatenessUsesTheRecordersClockWhenKnown(t *testing.T) {
	recorderNow := t0.Add(2 * time.Hour)
	m := stManifest(recorderNow.Add(-10*time.Second), nil)
	r := stReading(m, recorderNow.Add(10*time.Minute), func(r *Reading) { r.ServerTime = recorderNow; r.Status = &StatusDoc{} })
	si := derive(r, nil)
	if si.State != SClockDiffers {
		t.Fatalf("state %s", si.State)
	}
	if si.SkewSeconds == nil || *si.SkewSeconds > -599 || *si.SkewSeconds < -601 {
		t.Fatalf("skew %v", si.SkewSeconds)
	}
}

func TestClockLimitIsOneSecond(t *testing.T) {
	now := t0.Add(2 * time.Hour)
	m := stManifest(now.Add(-5*time.Second), nil)
	for _, c := range []struct {
		skew time.Duration
		want PageState
	}{{900 * time.Millisecond, SRunning}, {-900 * time.Millisecond, SRunning}, {1100 * time.Millisecond, SClockDiffers}, {-5 * time.Second, SClockDiffers}} {
		si := derive(stReading(m, now, func(r *Reading) { r.ServerTime = now.Add(c.skew); r.Status = &StatusDoc{} }), nil)
		if si.State != c.want {
			t.Errorf("skew %v: %s want %s", c.skew, si.State, c.want)
		}
	}
}

func TestFolderHasNoClockToCompareAndSaysSo(t *testing.T) {
	now := t0.Add(2 * time.Hour)
	si := derive(stReading(stManifest(now.Add(-5*time.Second), nil), now, nil), nil)
	if si.State != SRunning || si.SkewSeconds != nil || !strings.Contains(si.Detail, "clock was not compared") {
		t.Fatalf("%+v", si)
	}
}

func TestDiskLowLimitIsTenPercentAndResumeClearsAPause(t *testing.T) {
	now := t0.Add(2 * time.Hour)
	fresh := now.Add(-5 * time.Second)
	for _, c := range []struct {
		free float64
		want PageState
	}{{10, SRunning}, {9.9, SDiskLow}, {50, SRunning}} {
		si := derive(stReading(stManifest(fresh, nil), now, func(r *Reading) { r.Status = &StatusDoc{FreePercent: pct(c.free)} }), nil)
		if si.State != c.want {
			t.Errorf("free %v: %s want %s", c.free, si.State, c.want)
		}
	}
	resumed := stManifest(fresh, func(m *Manifest) {
		m.Events = []Event{{T: fresh.Add(-time.Hour), Kind: EventPausedDiskLow}, {T: fresh.Add(-time.Minute), Kind: EventResumed}}
	})
	if si := derive(stReading(resumed, now, nil), nil); si.State != SRunning {
		t.Fatalf("a resumed recorder is running: %+v", si)
	}
}

func TestStoppedWinsOverLate(t *testing.T) {
	now := t0.Add(2 * time.Hour)
	m := stManifest(now.Add(-time.Hour), func(m *Manifest) { m.State = ManifestStopped })
	if si := derive(stReading(m, now, nil), nil); si.State != SStopped {
		t.Fatalf("%+v", si)
	}
}

func TestIncompleteWinsOverRunning(t *testing.T) {
	now := t0.Add(2 * time.Hour)
	r := stReading(stManifest(now.Add(-5*time.Second), nil), now, nil)
	for _, rep := range []*ReadReport{{Missing: []string{"a"}}, {Dropped: []string{"a"}}} {
		if si := derive(r, rep); si.State != SIncomplete {
			t.Errorf("%+v -> %s", rep, si.State)
		}
	}
	// A torn last line alone is a recorder still writing: not Incomplete.
	if si := derive(r, &ReadReport{Torn: []string{"a"}}); si.State != SRunning {
		t.Fatalf("torn only: %s", si.State)
	}
}

func TestIncompleteDetailUsesSingularOrPluralFileWording(t *testing.T) {
	now := t0.Add(2 * time.Hour)
	r := stReading(stManifest(now.Add(-5*time.Second), nil), now, nil)

	si := derive(r, &ReadReport{Missing: []string{"a"}})
	if !strings.Contains(si.Detail, "1 recording file could not be read to the end") {
		t.Fatalf("n=1: %s", si.Detail)
	}

	si = derive(r, &ReadReport{Missing: []string{"a", "b"}})
	if !strings.Contains(si.Detail, "2 recording files could not be read to the end") {
		t.Fatalf("n=2: %s", si.Detail)
	}
}

func TestReadFailuresMapToUnreachableWithAReason(t *testing.T) {
	for _, c := range []struct {
		kind   ReadKind
		state  PageState
		reason string
	}{
		{ReadNotFound, SNotFound, ""},
		{ReadUnreachable, SUnreachable, "timeout"},
		{ReadKeyRefused, SUnreachable, "key"},
		{ReadThrottled, SUnreachable, "wait"},
		{ReadBusy, SUnreachable, "busy"},
		{ReadNewerFormat, SUnreachable, "newer-format"},
	} {
		si := DeriveState(StateInput{Placed: true, Err: readErr(c.kind, "words", nil)})
		if si.State != c.state || si.Reason != c.reason || si.Readiness != WordCannotTestYet || si.Detail != "words" {
			t.Errorf("%s: %+v", c.kind, si)
		}
	}
	// An error that is not a read error is never shown as fine.
	si := DeriveState(StateInput{Placed: true, Err: errors.New("boom")})
	if si.State != SUnreachable || si.Readiness != WordCannotTestYet {
		t.Fatalf("%+v", si)
	}
	// No reading and no error yet: unreachable, not running.
	if si := DeriveState(StateInput{Placed: true}); si.State != SUnreachable {
		t.Fatalf("%+v", si)
	}
}

func TestNotPlacedWinsOverEverything(t *testing.T) {
	si := DeriveState(StateInput{Placed: false, Err: readErr(ReadNotFound, "x", nil)})
	if si.State != SNotPlaced {
		t.Fatalf("%+v", si)
	}
}

// The state is derived from a real recorder folder: the whole path from recording
// to state, with no fixture made by hand.
func TestStateFromARealRecordingFolder(t *testing.T) {
	g := recordFolder(t, 20)
	src := folderSource(t, g.s.Dir())
	now := g.clk.t.Add(5 * time.Second)
	rd, err := ProbeSource(t.Context(), src, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if si := derive(&rd, nil); si.State != SRunning {
		t.Fatalf("%+v", si)
	}
	late := now.Add(5 * time.Minute)
	rd2, _ := ProbeSource(t.Context(), src, func() time.Time { return late })
	if si := derive(&rd2, nil); si.State != SRunningLate {
		t.Fatalf("%+v", si)
	}
}

func TestStateOverALiveStatusServer(t *testing.T) {
	g := newSrvRig(t)
	src := httpSource(t, g.ts, g.key)
	rd, err := ProbeSource(t.Context(), src, g.clk.now)
	if err != nil {
		t.Fatal(err)
	}
	if si := derive(&rd, nil); si.State != SRunning || si.SkewSeconds == nil {
		t.Fatalf("%+v", si)
	}
	// This PC's clock is 8 s ahead of the recorder's.
	rd2, _ := ProbeSource(t.Context(), src, func() time.Time { return g.clk.t.Add(8 * time.Second) })
	if si := derive(&rd2, nil); si.State != SClockDiffers {
		t.Fatalf("%+v", si)
	}
}
