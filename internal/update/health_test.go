package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var sha = strings.Repeat("a", 64)

func rig(t *testing.T, running string) (*Tracker, Marker) {
	t.Helper()
	prev := filepath.Join(t.TempDir(), PreviousDir)
	if err := os.MkdirAll(prev, 0o755); err != nil {
		t.Fatal(err)
	}
	m := Marker{From: "1.0.0", To: "1.1.0", SetupSHA256: sha, At: time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)}
	if err := WriteMarker(prev, m); err != nil {
		t.Fatal(err)
	}
	return &Tracker{StateDir: t.TempDir(), PrevDir: prev, Version: running}, m
}

func TestMarkerRoundTripAndValidation(t *testing.T) {
	tr, m := rig(t, "1.1.0")
	got, err := ReadMarker(tr.PrevDir)
	if err != nil || got != m {
		t.Fatalf("%+v %v", got, err)
	}
	for _, bad := range []Marker{{From: "x", To: "1.1.0", SetupSHA256: sha}, {From: "1.0.0", To: "v1.1.0", SetupSHA256: sha}, {From: "1.0.0", To: "1.1.0", SetupSHA256: "zz"}} {
		if WriteMarker(tr.PrevDir, bad) == nil {
			t.Errorf("%+v must be refused", bad)
		}
	}
	// a hand-edited marker is refused when read
	os.WriteFile(filepath.Join(tr.PrevDir, MarkerName), []byte(`{"from":"1.0.0","to":"../x","setupSha256":"`+sha+`"}`), 0o644)
	if _, err := ReadMarker(tr.PrevDir); err == nil {
		t.Fatal("bad marker accepted")
	}
}

func TestNoMarkerOrOtherVersionMeansNothingToWatch(t *testing.T) {
	tr, _ := rig(t, "1.2.0") // running a version the marker does not name
	if a, _ := tr.Begin(); a != ActNone {
		t.Fatalf("action %v", a)
	}
	tr2 := &Tracker{StateDir: t.TempDir(), PrevDir: filepath.Join(t.TempDir(), "none"), Version: "1.1.0"}
	if a, _ := tr2.Begin(); a != ActNone {
		t.Fatalf("action %v", a)
	}
}

func TestThreeFailedStartsAreAllowedTheFourthRecovers(t *testing.T) {
	tr, m := rig(t, "1.1.0")
	for i := 1; i <= MaxFailedStarts; i++ {
		if a, got := tr.Begin(); a != ActWatch || got != m {
			t.Fatalf("start %d: %v", i, a)
		}
	}
	if a, got := tr.Begin(); a != ActRecover || got != m {
		t.Fatalf("fourth start: %v", a)
	}
}

func TestConfirmEndsWatching(t *testing.T) {
	tr, _ := rig(t, "1.1.0")
	tr.Begin()
	tr.Confirm()
	for i := 0; i < 10; i++ {
		if a, _ := tr.Begin(); a != ActNone {
			t.Fatalf("after confirm: %v", a)
		}
	}
}

func TestACleanShortVisitIsNotACrash(t *testing.T) {
	tr, _ := rig(t, "1.1.0")
	for i := 0; i < 20; i++ {
		if a, _ := tr.Begin(); a != ActWatch {
			t.Fatalf("visit %d: %v", i, a)
		}
		tr.CleanExit()
	}
}

func TestACleanExitAfterConfirmChangesNothing(t *testing.T) {
	tr, _ := rig(t, "1.1.0")
	tr.Begin()
	tr.Confirm()
	tr.CleanExit()
	if a, _ := tr.Begin(); a != ActNone {
		t.Fatal(a)
	}
}

func TestANewMarkerResetsTheCount(t *testing.T) {
	tr, m := rig(t, "1.1.0")
	for i := 0; i < MaxFailedStarts; i++ {
		tr.Begin()
	}
	// the same version installed again later: a fresh marker time starts a fresh count
	m.At = m.At.Add(time.Hour)
	if err := WriteMarker(tr.PrevDir, m); err != nil {
		t.Fatal(err)
	}
	if a, _ := tr.Begin(); a != ActWatch {
		t.Fatalf("%v", a)
	}
}

func TestSkipListIsExactAndBounded(t *testing.T) {
	tr := &Tracker{StateDir: t.TempDir()}
	if tr.Skipped("1.1.0") {
		t.Fatal("empty list skips")
	}
	tr.Skip("1.1.0")
	tr.Skip("1.1.0")
	if !tr.Skipped("1.1.0") || tr.Skipped("1.1.1") || tr.Skipped("1.1") {
		t.Fatal("skip must match the exact version only")
	}
	for i := 0; i < 50; i++ {
		tr.Skip("2.0." + string(rune('a'+i%26)) + string(rune('a'+i/26)))
	}
	var f skipFile
	if err := readJSON(tr.skipPath(), &f); err != nil || len(f.Versions) > maxSkips {
		t.Fatalf("%d %v", len(f.Versions), err)
	}
}

func TestOversizedStateFileIsIgnored(t *testing.T) {
	tr := &Tracker{StateDir: t.TempDir()}
	os.WriteFile(tr.skipPath(), []byte(strings.Repeat(" ", maxStateFile+1)), 0o644)
	if tr.Skipped("1.1.0") {
		t.Fatal("huge file read")
	}
}
