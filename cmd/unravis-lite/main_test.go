package main

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/blackbox"
)

// RV-LITE-1: the link is opened by hand, often on a slow first start, so it must outlive a slow start (the full app gives a
// hand-opened link 10 minutes; the old 2 minutes could run out before the person got to the browser).
func TestLaunchCodeOutlivesASlowFirstStart(t *testing.T) {
	if codeLife < 5*time.Minute {
		t.Fatalf("the one-time link lives only %s, too short for a slow first start", codeLife)
	}
}

func TestParseRefusesBadInput(t *testing.T) {
	for _, args := range [][]string{{"extra"}, {"--port", "70000"}, {"--port", "-1"}} {
		if _, err := parse(args, io.Discard); err == nil {
			t.Errorf("%v should be refused", args)
		}
	}
	o, err := parse([]string{"--folder", `D:\x`, "--open", "--port", "0"}, io.Discard)
	if err != nil || o.folder != `D:\x` || !o.open {
		t.Fatalf("%+v %v", o, err)
	}
}

func fakeStat(t *testing.T, err error) {
	t.Helper()
	old := statFolder
	statFolder = func(string) (os.FileInfo, error) {
		if err != nil {
			return nil, err
		}
		return os.Stat(t.TempDir())
	}
	t.Cleanup(func() { statFolder = old })
}

func TestFindRefusesAFolderThatCannotBeOpened(t *testing.T) {
	fakeStat(t, os.ErrNotExist)
	if _, _, err := find(`D:\nope`, nil); err == nil || !strings.Contains(err.Error(), "cannot open") {
		t.Fatalf("%v", err)
	}
}

func TestFindUsesTheGivenFolderWithoutScanning(t *testing.T) {
	fakeStat(t, nil)
	scan := func() (blackbox.LocalFound, error) { t.Fatal("must not scan"); return blackbox.LocalFound{}, nil }
	loc, _, err := find(`D:\Recordings`, scan)
	if err != nil || loc.Kind != blackbox.LocFolder || loc.Path != `D:\Recordings` {
		t.Fatalf("%+v %v", loc, err)
	}
	if loc, _, err = find(`\\srv\share`, scan); err != nil || loc.Kind != blackbox.LocShare {
		t.Fatalf("%+v %v", loc, err)
	}
}

func TestFindRefusesAnAddress(t *testing.T) {
	if _, _, err := find("http://web-b:7788", nil); err == nil || !strings.Contains(err.Error(), "folder or a share") {
		t.Fatalf("%v", err)
	}
	if _, _, err := find("relative\\dir", nil); err == nil {
		t.Fatal("a relative path must be refused")
	}
}

func TestFindFromThisPC(t *testing.T) {
	fakeStat(t, nil)
	ok := func() (blackbox.LocalFound, error) {
		return blackbox.LocalFound{Installed: true, Folder: `E:\bb`, Name: "web-a"}, nil
	}
	loc, name, err := find("  ", ok)
	if err != nil || loc.Path != `E:\bb` || name != "web-a" {
		t.Fatalf("%+v %q %v", loc, name, err)
	}
	for _, scan := range []func() (blackbox.LocalFound, error){
		func() (blackbox.LocalFound, error) { return blackbox.LocalFound{}, nil },
		func() (blackbox.LocalFound, error) { return blackbox.LocalFound{Installed: true}, nil },
		func() (blackbox.LocalFound, error) { return blackbox.LocalFound{}, errors.New("denied") },
	} {
		_, _, err := find("", scan)
		if err == nil || !strings.Contains(err.Error(), "--folder") {
			t.Errorf("the message must name --folder: %v", err)
		}
	}
}

func TestFindSaysWhenSettingsCannotBeRead(t *testing.T) {
	scan := func() (blackbox.LocalFound, error) {
		return blackbox.LocalFound{Installed: true, ConfigUnreadable: true}, nil
	}
	_, _, err := find("", scan)
	if err == nil || !strings.Contains(err.Error(), "could not be read") || strings.Contains(err.Error(), "no Blackbox was found") {
		t.Fatalf("%v", err)
	}
}
