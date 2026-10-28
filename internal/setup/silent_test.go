package setup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSilentConfigParses(t *testing.T) {
	cfg, err := ParseSilentConfig([]byte("\xef\xbb\xbf" + `{
  "scope": "machine",
  "location": "C:\\Program Files\\Unravis",
  "blackbox": {"name": "web-01", "folder": "D:\\UnravisBlackbox", "keepDays": 14, "maxGB": 20, "minFreePercent": 10, "port": 7788},
  "firewall": {"remoteAddresses": ["10.0.0.5"]},
  "startAtSignIn": false,
  "openWhenDone": true,
  "readKeyFile": "C:\\Users\\Ana\\blackbox-key.txt"
}`))
	if err != nil {
		t.Fatal(err)
	}
	req, err := cfg.Request(OpInstall)
	if err != nil {
		t.Fatal(err)
	}
	o := req.Options
	if !req.Silent || o.Scope != ScopeMachine || o.Blackbox.Name != "web-01" || o.Firewall.RemoteAddresses[0] != "10.0.0.5" || o.StartAtSignIn {
		t.Fatalf("%+v", req)
	}
	if o.OpenWhenDone {
		t.Fatal("openWhenDone is forced false in silent mode")
	}
	if o.ReadKeyFile != `C:\Users\Ana\blackbox-key.txt` {
		t.Fatal(o.ReadKeyFile)
	}
}

func TestSilentConfigDefaults(t *testing.T) {
	cfg, err := ParseSilentConfig([]byte(`{"scope":"user"}`))
	if err != nil {
		t.Fatal(err)
	}
	req, err := cfg.Request(OpInstall)
	if err != nil {
		t.Fatal(err)
	}
	if !req.Options.StartAtSignIn || req.Options.Blackbox != nil || req.Options.Location != "" {
		t.Fatalf("%+v", req.Options)
	}
}

func TestSilentConfigRefusesBrokenFiles(t *testing.T) {
	cases := []struct{ name, json, want string }{
		{"empty", "  ", "empty"},
		{"syntax", "{\n  \"scope\": \"user\",\n}", "line 3"},
		{"cut short", `{"scope": "user"`, "ends too early"},
		{"unknown key", `{"scope":"user","blackBox":null}`, `"blackBox"`},
		{"misspelt nested", `{"scope":"machine","blackbox":{"name":"a","folder":"D:\\B","port":7788,"retention":3}}`, `"retention"`},
		{"wrong type", `{"scope":"user","startAtSignIn":"yes"}`, `"startAtSignIn" must be true or false`},
		{"port as text", `{"scope":"machine","blackbox":{"port":"7788"}}`, "must be a number"},
		{"two objects", `{"scope":"user"} {"scope":"machine"}`, "more after its closing brace"},
		{"array", `[{"scope":"user"}]`, "one JSON object"},
		{"too big", `{"location":"` + strings.Repeat("a", 70<<10) + `"}`, "under"},
	}
	for _, c := range cases {
		_, err := ParseSilentConfig([]byte(c.json))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want it to mention %q", c.name, err, c.want)
		}
	}
}

func TestSilentConfigChoicesThatCannotRun(t *testing.T) {
	cases := []struct {
		name, json string
		op         Op
		want       string
	}{
		{"no scope", `{}`, OpInstall, `"scope"`},
		{"bad scope", `{"scope":"everyone"}`, OpInstall, `"user" or "machine"`},
		{"blackbox without key file", `{"scope":"machine","blackbox":{"name":"a","folder":"D:\\B","keepDays":7,"maxGB":2,"minFreePercent":10,"port":7788}}`, OpInstall, "readKeyFile"},
		{"firewall without blackbox", `{"scope":"machine","firewall":{"remoteAddresses":["10.0.0.5"]}}`, OpInstall, "no \"blackbox\""},
		{"uninstall in an install", `{"scope":"user","uninstall":{"deleteCases":true}}`, OpInstall, "--uninstall"},
		{"quoted true", `{"uninstall":{"deleteCases":"true"}}`, OpUninstall, "true or false (without quotes)"},
		{"number", `{"uninstall":{"deleteRecordings":1}}`, OpUninstall, "true or false"},
	}
	for _, c := range cases {
		cfg, err := ParseSilentConfig([]byte(c.json))
		if err == nil {
			_, err = cfg.Request(c.op)
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want it to mention %q", c.name, err, c.want)
		}
	}
}

func TestSilentUninstallDeletesOnlyOnExplicitTrue(t *testing.T) {
	for _, c := range []struct {
		json         string
		cases, recos bool
	}{
		{`{}`, false, false},
		{`{"uninstall":{}}`, false, false},
		{`{"uninstall":{"deleteCases":false,"deleteRecordings":null}}`, false, false},
		{`{"uninstall":{"deleteCases":true}}`, true, false},
		{`{"uninstall":{"deleteCases":true,"deleteRecordings":true}}`, true, true},
	} {
		cfg, err := ParseSilentConfig([]byte(c.json))
		if err != nil {
			t.Fatal(err)
		}
		req, err := cfg.Request(OpUninstall)
		if err != nil {
			t.Fatal(err)
		}
		if req.Uninstall.DeleteCases != c.cases || req.Uninstall.DeleteRecordings != c.recos {
			t.Fatalf("%s: %+v", c.json, req.Uninstall)
		}
		if c.cases != (req.Uninstall.ConfirmCases == ConfirmDeleteWord) {
			t.Fatalf("%s: confirmation %q", c.json, req.Uninstall.ConfirmCases)
		}
	}
}

// A whole silent run: install with a Blackbox, the read key in its file and never in the log, then a silent uninstall that deletes
// recordings only because the file said true.
func TestSilentRunEndToEnd(t *testing.T) {
	e := newEnv(t, "1.0.0")
	keyFile := filepath.Join(e.dirs.UserProfile, "Documents", "bb key é.txt")
	if err := os.MkdirAll(filepath.Dir(keyFile), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := SilentConfig{Scope: ScopeMachine, Blackbox: e.withBlackbox(e.machineOpts(), false).Blackbox, ReadKeyFile: keyFile}
	req, err := cfg.Request(OpInstall)
	if err != nil {
		t.Fatal(err)
	}
	res := e.r.Perform(context.Background(), req)
	if res.Outcome != OutcomeDone || res.Outcome.ExitCode() != 0 {
		t.Fatalf("%+v", res)
	}
	key, err := os.ReadFile(keyFile)
	if err != nil || !strings.HasPrefix(strings.TrimSpace(string(key)), "ubk-") {
		t.Fatalf("key file %q %v", key, err)
	}
	for _, l := range e.log {
		if strings.Contains(l, strings.TrimSpace(string(key))) {
			t.Fatalf("the log has the read key: %q", l)
		}
	}
	rec := filepath.Join(cfg.Blackbox.Folder, "bb-20261027-10.ndjson")
	writeFile(t, rec, "{}")

	ucfg, _ := ParseSilentConfig([]byte(`{"uninstall":{"deleteRecordings":true}}`))
	ureq, err := ucfg.Request(OpUninstall)
	if err != nil {
		t.Fatal(err)
	}
	old, _ := Detect(e.sys.Reg)
	FillRecordingsConfirmation(&ureq, old)
	if ureq.Uninstall.ConfirmRecordings != "web-b" {
		t.Fatalf("%+v", ureq.Uninstall)
	}
	if res := e.r.Perform(context.Background(), ureq); res.Outcome != OutcomeDone {
		t.Fatalf("%+v", res)
	}
	if exists(rec) {
		t.Fatal("recordings stay unless the file said true")
	}
}
