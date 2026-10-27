package blackbox

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultsMatchDecisions(t *testing.T) {
	c := Default()
	c.Name = "web-01"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if c.Folder != `D:\UnravisBlackbox` || c.KeepDays != 7 || c.MaxGB != 2 || c.MinFreePercent != 10 || c.Port != 7788 || c.IntervalSeconds != 15 {
		t.Fatalf("defaults drifted from A31/A37: %+v", c)
	}
	if c.Probe.Enabled {
		t.Fatal("the probe block must be off")
	}
}

func TestChooseFolder(t *testing.T) {
	if got := ChooseFolder(func(string) bool { return true }); got != DefaultFolder {
		t.Fatal(got)
	}
	if got := ChooseFolder(func(string) bool { return false }); got != FallbackFolder {
		t.Fatal(got)
	}
}

func TestParseConfigFillsDefaultsAndKeepsProbeOff(t *testing.T) {
	c, err := ParseConfig([]byte(`{"name":"sql-prod-01"}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.KeepDays != 7 || c.Port != 7788 || c.Bind != "127.0.0.1" || len(c.LogPaths) != 1 || c.Probe.Enabled {
		t.Fatalf("%+v", c)
	}
	if !c.Record.Processes || !c.Record.LogsCatalogue || c.Record.CopyLogsOnSnapshot {
		t.Fatalf("record defaults: %+v", c.Record)
	}
}

func TestParseConfigAcceptsByteOrderMark(t *testing.T) {
	// PowerShell's Out-File writes a UTF-8 BOM.
	if _, err := ParseConfig([]byte("\xEF\xBB\xBF" + `{"name":"a"}`)); err != nil {
		t.Fatal(err)
	}
}

func TestParseConfigRefusals(t *testing.T) {
	cases := map[string]string{
		"typo in a key":         `{"name":"a","keepDay":3}`,
		"no name":               `{}`,
		"bad name":              `{"name":"a b"}`,
		"name with path":        `{"name":"..\\x"}`,
		"relative folder":       `{"name":"a","folder":"rec"}`,
		"zero keepDays":         `{"name":"a","keepDays":0}`,
		"negative keepDays":     `{"name":"a","keepDays":-1}`,
		"zero maxGB":            `{"name":"a","maxGB":0}`,
		"zero free":             `{"name":"a","minFreePercent":0}`,
		"free above 50":         `{"name":"a","minFreePercent":90}`,
		"port 0":                `{"name":"a","port":0}`,
		"port too high":         `{"name":"a","port":70000}`,
		"odd interval":          `{"name":"a","intervalSeconds":7}`,
		"probe on":              `{"name":"a","probe":{"enabled":true}}`,
		"relative log path":     `{"name":"a","logPaths":["logs"]}`,
		"trailing text":         `{"name":"a"} {"name":"b"}`,
		"not json":              `name: a`,
		"empty file":            ``,
		"wrong type for a port": `{"name":"a","port":"7788"}`,
	}
	for label, text := range cases {
		_, err := ParseConfig([]byte(text))
		if !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("%s: want ErrInvalidConfig, got %v", label, err)
		}
	}
}

func TestParseConfigHousekeepingMessageSaysItCannotBeOff(t *testing.T) {
	_, err := ParseConfig([]byte(`{"name":"a","keepDays":0}`))
	if err == nil || !strings.Contains(err.Error(), "cannot be turned off") {
		t.Fatal(err)
	}
}

func TestSaveAndLoadRoundTripWithBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "blackbox.json")
	c := Default()
	c.Name = "web-01"
	c.Folder = filepath.Join(dir, "rec")
	if err := SaveConfig(path, c, true); err != nil {
		t.Fatal(err)
	}
	c.KeepDays = 3
	if err := SaveConfig(path, c, true); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConfig(path)
	if err != nil || got.KeepDays != 3 || got.Name != "web-01" {
		t.Fatalf("%+v %v", got, err)
	}
	old, err := LoadConfig(path + ".bak")
	if err != nil || old.KeepDays != 7 {
		t.Fatalf("backup should hold the previous file: %+v %v", old, err)
	}
	// No temporary file is left next to it.
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("temporary file left: %s", e.Name())
		}
	}
}

func TestSaveConfigRefusesInvalidAndKeepsOldFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "blackbox.json")
	c := Default()
	c.Name = "ok"
	c.Folder = filepath.Join(dir, "rec")
	if err := SaveConfig(path, c, false); err != nil {
		t.Fatal(err)
	}
	c.KeepDays = 0
	if err := SaveConfig(path, c, false); !errors.Is(err, ErrInvalidConfig) {
		t.Fatal(err)
	}
	if got, err := LoadConfig(path); err != nil || got.KeepDays != 7 {
		t.Fatalf("old file must survive: %+v %v", got, err)
	}
}

func TestMarshalShowsEmptyProbeBlock(t *testing.T) {
	c := Default()
	c.Name = "x"
	b, _ := c.Marshal()
	if !strings.Contains(string(b), `"probe": {`) || !strings.Contains(string(b), `"enabled": false`) {
		t.Fatalf("probe block missing:\n%s", b)
	}
}

// A53: without the owner's opt-in the status page answers on loopback only.
func TestDefaultBindIsLoopbackNotEveryInterface(t *testing.T) {
	if DefaultBind != "127.0.0.1" || Default().Bind != "127.0.0.1" {
		t.Fatalf("default bind is %q, want 127.0.0.1 (A53)", Default().Bind)
	}
	c, err := ParseConfig([]byte(`{"name":"a"}`))
	if err != nil || c.Bind != "127.0.0.1" {
		t.Fatalf("a file with no bind must listen on loopback: %+v %v", c, err)
	}
}
