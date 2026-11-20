package main

import (
	"errors"
	"io"
	"strings"
	"testing"

	"api-audit-explorer/internal/blackbox"
)

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

func TestFindUsesTheGivenFolderWithoutScanning(t *testing.T) {
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
