package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/update"
)

func healthRig(t *testing.T) (*update.Tracker, update.Marker) {
	t.Helper()
	prev := filepath.Join(t.TempDir(), update.PreviousDir)
	if err := os.MkdirAll(prev, 0o755); err != nil {
		t.Fatal(err)
	}
	m := update.Marker{From: "1.0.0", To: "1.1.0", SetupSHA256: strings.Repeat("b", 64), At: time.Now().UTC()}
	if err := update.WriteMarker(prev, m); err != nil {
		t.Fatal(err)
	}
	return &update.Tracker{StateDir: t.TempDir(), PrevDir: prev, Version: "1.1.0"}, m
}

func TestGoBackSkipsTheVersionAndStartsTheMarkedSetup(t *testing.T) {
	tr, m := healthRig(t)
	var gotPath, gotSha, gotVer string
	ok := goBack(tr, m, func(p, s, v string) error { gotPath, gotSha, gotVer = p, s, v; return nil }, func(string, ...any) {})
	if !ok || gotPath != tr.SetupPath() || gotSha != m.SetupSHA256 || gotVer != "1.0.0" {
		t.Fatalf("%v %q %q %q", ok, gotPath, gotSha, gotVer)
	}
	if !tr.Skipped("1.1.0") || tr.Skipped("1.1.1") {
		t.Fatal("the failed version must be skipped, and only it")
	}
}

func TestGoBackThatCannotStartLetsTheAppStart(t *testing.T) {
	tr, m := healthRig(t)
	if goBack(tr, m, func(string, string, string) error { return errors.New("no") }, func(string, ...any) {}) {
		t.Fatal("a rollback that did not start must not stop the app")
	}
}

func TestACrashIsNotCountedAsAVisit(t *testing.T) {
	for name, run := range map[string]func(end func(*error)) (err error){
		"error": func(end func(*error)) (err error) { defer end(&err); return errors.New("port in use") },
		"panic": func(end func(*error)) (err error) {
			defer func() { _ = recover() }() // the test catches what end raises again
			defer end(&err)
			panic("boom")
		},
	} {
		tr, _ := healthRig(t)
		tr.Begin()
		run(watchHealth(tr, time.Hour))
		if a, _ := tr.Begin(); a != update.ActWatch {
			t.Fatalf("%s: %v", name, a)
		}
		// two starts are on record, so the failure was counted: one more unconfirmed start per loop reaches recovery.
		tr.Begin()
		if a, _ := tr.Begin(); a != update.ActRecover {
			t.Fatalf("%s: a failed run did not count: %v", name, a)
		}
	}
}

func TestACleanShortVisitDoesNotCount(t *testing.T) {
	tr, _ := healthRig(t)
	for i := 0; i < 10; i++ {
		tr.Begin()
		var err error
		func() { defer watchHealth(tr, time.Hour)(&err) }()
	}
	if a, _ := tr.Begin(); a != update.ActWatch {
		t.Fatalf("%v", a)
	}
}

func TestStayingUpConfirms(t *testing.T) {
	tr, _ := healthRig(t)
	tr.Begin()
	end := watchHealth(tr, 10*time.Millisecond)
	time.Sleep(80 * time.Millisecond)
	var err error
	end(&err)
	for i := 0; i < 6; i++ {
		if a, _ := tr.Begin(); a != update.ActNone {
			t.Fatalf("not confirmed: %v", a)
		}
	}
}
