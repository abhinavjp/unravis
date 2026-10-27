package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"api-audit-explorer/internal/blackbox"
)

// Clumsy-user cases at the command line: what a hurried admin does wrong, and
// what the Blackbox says and leaves behind.

type smallDisk struct{}

func (smallDisk) Free(string) (blackbox.DiskInfo, error) {
	return blackbox.DiskInfo{Free: 200 << 20, Total: 100 << 30}, nil
}

func jsonAnswer(t *testing.T, out string) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &got); err != nil {
		t.Fatalf("not one line of JSON: %q (%v)", out, err)
	}
	return got
}

func TestClumsyInstallTwiceSaysSoAndKeepsTheFirstKey(t *testing.T) {
	r := newRig(t)
	if c, out, _ := r.do(append([]string{"install", "--json"}, r.args...)...); c != 0 {
		t.Fatal(out)
	}
	keyPath := filepath.Join(r.dir, defaultKeyName)
	before, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	c, out, _ := r.do(append([]string{"install", "--json"}, r.args...)...)
	got := jsonAnswer(t, out)
	if c != exitFail || got["ok"] != false || got["reason"] != "service-exists" {
		t.Fatalf("%d %v", c, got)
	}
	if strings.Contains(out, "ubk-") {
		t.Fatalf("a second install printed a key: %s", out)
	}
	if after, _ := os.ReadFile(keyPath); string(after) != string(before) {
		t.Fatal("a second install replaced the first read key")
	}
	if r.svc.state != blackbox.SvcRunning {
		t.Fatal("the first install was disturbed")
	}
}

func TestClumsyDefaultPortBusyNamesPort7788(t *testing.T) {
	r := newRig(t)
	r.env.port = errors.New("in use")
	c, out, _ := r.do(append([]string{"install", "--json"}, r.args...)...)
	got := jsonAnswer(t, out)
	if c != exitFail || got["reason"] != "port-busy" || !strings.Contains(got["message"].(string), "7788") {
		t.Fatalf("%d %v", c, got)
	}
	if _, err := os.Stat(filepath.Join(r.dir, defaultKeyName)); err == nil {
		t.Fatal("a key file was left behind")
	}
}

func TestClumsyInstallOnAFullDriveSaysFolderAndLeavesNothing(t *testing.T) {
	r := newRig(t)
	r.d.mgr.Disk = smallDisk{}
	c, out, _ := r.do(append([]string{"install", "--json"}, r.args...)...)
	got := jsonAnswer(t, out)
	if c != exitFail || got["reason"] != "folder" {
		t.Fatalf("%d %v", c, got)
	}
	if r.svc.state != blackbox.SvcNotInstalled || len(r.svc.calls) != 0 {
		t.Fatalf("the service was touched: %v", r.svc.calls)
	}
	if _, err := os.Stat(filepath.Join(r.dir, defaultKeyName)); err == nil {
		t.Fatal("a key file was left behind")
	}
}

func TestClumsyAntivirusRemovesTheExeAndInstallSaysAntivirus(t *testing.T) {
	r := newRig(t)
	if err := os.Remove(r.env.exe); err != nil {
		t.Fatal(err)
	}
	c, out, _ := r.do(append([]string{"install", "--json"}, r.args...)...)
	got := jsonAnswer(t, out)
	if c != exitFail || got["reason"] != "antivirus" {
		t.Fatalf("%d %v", c, got)
	}
	if r.svc.state != blackbox.SvcNotInstalled {
		t.Fatalf("service left behind: %v", r.svc.calls)
	}
}

func TestClumsyRemoveTwiceAndTypoedNameDeleteNothing(t *testing.T) {
	r := newRig(t)
	if c, out, _ := r.do(append([]string{"install", "--json", "--name", "web-b"}, r.args...)...); c != 0 {
		t.Fatal(out)
	}
	rec := filepath.Join(r.dir, "rec")
	if err := os.WriteFile(filepath.Join(rec, "bb-20261027-10.ndjson"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A typo in the confirming name changes nothing, not even the service.
	c, out, _ := r.do("remove", "--json", "--delete-recordings", "--confirm-name", "web-bb", "--config", filepath.Join(r.dir, defaultConfigName))
	got := jsonAnswer(t, out)
	if c != exitFail || got["ok"] != false || r.svc.state != blackbox.SvcRunning {
		t.Fatalf("%d %v state %v", c, got, r.svc.state)
	}
	if _, err := os.Stat(filepath.Join(rec, "bb-20261027-10.ndjson")); err != nil {
		t.Fatal("a recording was deleted by a typo")
	}
	// Plain remove keeps the recordings; a second one says nothing is installed.
	if c, out, _ := r.do("remove", "--json"); c != 0 {
		t.Fatal(out)
	}
	c, out, _ = r.do("remove", "--json")
	got = jsonAnswer(t, out)
	if c != exitFail || got["ok"] != false {
		t.Fatalf("%d %v", c, got)
	}
	if _, err := os.Stat(filepath.Join(rec, "bb-20261027-10.ndjson")); err != nil {
		t.Fatal("recordings lost by remove")
	}
}

func TestClumsyStopWhenNotInstalledAndApplyWithAHandEditedBrokenFile(t *testing.T) {
	r := newRig(t)
	if c, out, _ := r.do("stop", "--json"); c != exitFail || jsonAnswer(t, out)["ok"] != false {
		t.Fatalf("%d %s", c, out)
	}
	if c, out, _ := r.do(append([]string{"install", "--json"}, r.args...)...); c != 0 {
		t.Fatal(out)
	}
	cfgPath := filepath.Join(r.dir, defaultConfigName)
	// Someone edits the file and leaves a comma behind.
	if err := os.WriteFile(cfgPath, []byte(`{"name":"x","keepDays":7,}`), 0o644); err != nil {
		t.Fatal(err)
	}
	c, out, _ := r.do("apply", "--json")
	if c != exitFail || jsonAnswer(t, out)["ok"] != false {
		t.Fatalf("%d %s", c, out)
	}
	if r.svc.state != blackbox.SvcRunning {
		t.Fatal("a broken file stopped the recorder")
	}
	// Housekeeping off is not a setting.
	if err := os.WriteFile(cfgPath, []byte(`{"name":"x","keepDays":0}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if c, out, _ := r.do("apply", "--json"); c != exitFail || !strings.Contains(out, "housekeeping cannot be turned off") {
		t.Fatalf("%d %s", c, out)
	}
	if r.svc.state != blackbox.SvcRunning {
		t.Fatal("the recorder was stopped")
	}
}
