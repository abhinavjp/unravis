package cliattach

import (
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

func TestChildEnvDropsWhatMovesTheFolders(t *testing.T) {
	in := []string{"PATH=C:\\Windows", "AUDIT_CONFIG_DIR=D:\\x", "audit_session_token=secret", "UNRAVIS_RUN_DIR=D:\\r", "Unravis_Other=1", "LocalAppData=D:\\elsewhere", "SystemRoot=C:\\Windows", "AUDITOR=keep", "A=b=c"}
	got := childEnv(in, `C:\Users\me\AppData\Local`)
	want := []string{"PATH=C:\\Windows", "SystemRoot=C:\\Windows", "AUDITOR=keep", "A=b=c", `LOCALAPPDATA=C:\Users\me\AppData\Local`}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %q", got)
	}
	if got := childEnv([]string{"PATH=x"}, ""); len(got) != 1 {
		t.Errorf("no folder given: %q", got)
	}
}

func TestProcessGone(t *testing.T) {
	if !ProcessGone(1<<30, time.Now()) {
		t.Skip("this system reports a process for an unlikely pid")
	}
}

func TestParseEnvBlock(t *testing.T) {
	block := utf16.Encode([]rune("PATH=C:\\x\x00=C:=C:\\\x00LOCALAPPDATA=C:\\Users\\\u00e9\x00\x00"))
	got := parseEnvBlock(block)
	if len(got) != 2 || got[0] != "PATH=C:\\x" || got[1] != "LOCALAPPDATA=C:\\Users\\\u00e9" {
		t.Errorf("%q", got)
	}
	if got := parseEnvBlock(nil); len(got) != 0 {
		t.Errorf("empty: %q", got)
	}
	if got := parseEnvBlock(utf16.Encode([]rune("A=b"))); len(got) != 1 || got[0] != "A=b" { // no end: what there is
		t.Errorf("cut: %q", got)
	}
}
