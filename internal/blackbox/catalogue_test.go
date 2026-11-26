package blackbox

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

type stubGroup struct {
	name  string
	calls int
}

func (g *stubGroup) Name() string   { return g.name }
func (g *stubGroup) Optional() bool { return true }
func (g *stubGroup) Collect(time.Time, float64) (any, error) {
	g.calls++
	return &VMGroup{}, nil
}

func boolp(b bool) *bool { return &b }

// testConfig is a valid configuration on any OS: the folder is a real absolute path of the test machine.
func testConfig(t *testing.T) Config {
	t.Helper()
	c := Default()
	c.Name = "bb"
	c.Folder = filepath.Join(t.TempDir(), "UnravisBlackbox")
	c.LogPaths = []string{t.TempDir()}
	return c
}

var collectorIDRe = regexp.MustCompile(`^[a-z]+\.[a-z0-9]+$`)

// Every registered group is in the catalogue exactly once and the catalogue names no group that is not registered, so
// a new group cannot ship without an id (and settings cannot name something that does not exist).
func TestCatalogueMatchesRegistry(t *testing.T) {
	registryMu.Lock()
	regs := map[string]registration{}
	for k, v := range registry {
		regs[k] = v
	}
	registryMu.Unlock()
	seenID := map[string]bool{}
	seenGroup := map[string]bool{}
	for _, c := range Catalogue() {
		if !collectorIDRe.MatchString(c.ID) {
			t.Errorf("id %q is not <area>.<name> in lower case", c.ID)
		}
		if seenID[c.ID] {
			t.Errorf("id %q appears twice", c.ID)
		}
		seenID[c.ID] = true
		switch c.Kind {
		case KindCounter, KindDMV, KindCommand, KindFile, KindPath:
		default:
			t.Errorf("%s: unknown kind %q", c.ID, c.Kind)
		}
		if c.Title == "" || (c.Load != LoadLight && c.Load != LoadHeavy) {
			t.Errorf("%s: needs a title and a load tag", c.ID)
		}
		if c.Group == "" {
			continue
		}
		if seenGroup[c.Group] {
			t.Errorf("group %q has two ids", c.Group)
		}
		seenGroup[c.Group] = true
		reg, ok := regs[c.Group]
		if !ok {
			t.Errorf("%s names group %q, which is not registered", c.ID, c.Group)
			continue
		}
		if c.Always == reg.optional {
			t.Errorf("%s: always=%v but the group's optional=%v; the two must agree", c.ID, c.Always, reg.optional)
		}
	}
	for name := range regs {
		if !seenGroup[name] {
			t.Errorf("group %q has no catalogue id", name)
		}
	}
}

func TestCatalogueIsSortedAndACopy(t *testing.T) {
	a := Catalogue()
	for i := 1; i < len(a); i++ {
		if a[i-1].ID >= a[i].ID {
			t.Fatalf("not sorted at %s", a[i].ID)
		}
	}
	a[0].Title = "changed"
	if Catalogue()[0].Title == "changed" {
		t.Fatal("Catalogue returned the live slice")
	}
}

func TestCollectorsValidation(t *testing.T) {
	good := func() Config { return testConfig(t) }
	cases := []struct {
		name string
		set  map[string]CollectorSetting
		want string // "" = valid
	}{
		{"none", nil, ""},
		{"off and slower", map[string]CollectorSetting{"host.tcp": {On: boolp(false)}, "host.dumps": {EverySec: 60}}, ""},
		{"unknown id", map[string]CollectorSetting{"redis.slowlog": {On: boolp(true)}}, "not a known collector"},
		{"always on cannot go off", map[string]CollectorSetting{"host.diskcpu": {On: boolp(false)}}, "cannot be switched off"},
		{"always on may be slower", map[string]CollectorSetting{"host.diskcpu": {EverySec: 30}}, ""},
		{"odd every", map[string]CollectorSetting{"host.tcp": {EverySec: 7}}, "must be 5, 15, 30"},
		{"faster than the sample", map[string]CollectorSetting{"host.tcp": {EverySec: 5}}, "whole multiple"},
		{"not a multiple", map[string]CollectorSetting{"host.tcp": {EverySec: 900 + 0}}, ""},
	}
	for _, tc := range cases {
		c := good()
		c.Collectors = tc.set
		err := c.Validate()
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: unexpected error %v", tc.name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: want error containing %q, got %v", tc.name, tc.want, err)
		case tc.want != "" && !errors.Is(err, ErrInvalidConfig):
			t.Errorf("%s: error is not ErrInvalidConfig", tc.name)
		}
	}
	c := good()
	c.Collectors = map[string]CollectorSetting{}
	for i := 0; i <= maxCollectorSettings; i++ {
		c.Collectors[string(rune('a'+i%26))+strings.Repeat("x", i)] = CollectorSetting{}
	}
	if err := c.Validate(); err == nil {
		t.Error("too many entries were accepted")
	}
}

func TestCollectorsRoundTripAndUnknownFieldRefused(t *testing.T) {
	c := testConfig(t)
	c.Collectors = map[string]CollectorSetting{"host.tcp": {On: boolp(false)}, "web.iis": {EverySec: 60}}
	data, err := c.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	back, err := ParseConfig(data)
	if err != nil {
		t.Fatal(err)
	}
	if back.CollectorOn("host.tcp") || !back.CollectorOn("web.iis") || back.CollectorEvery("web.iis") != time.Minute {
		t.Errorf("round trip lost the settings: %+v", back.Collectors)
	}
	if _, err := ParseConfig([]byte(`{"name":"bb","folder":` + jsonString(c.Folder) + `,"collectors":{"host.tcp":{"on":true,"command":"calc"}}}`)); err == nil {
		t.Error("an unknown field inside a collector setting was accepted: settings must never carry commands")
	}
	// Default config has no collectors key at all, so old files and new files read the same.
	d := testConfig(t)
	out, _ := d.Marshal()
	if strings.Contains(string(out), "collectors") {
		t.Error("a default config writes a collectors key")
	}
}

func TestCollectorStatesCoverCatalogue(t *testing.T) {
	c := Default()
	c.Name = "bb"
	c.Collectors = map[string]CollectorSetting{"host.tcp": {On: boolp(false)}, "web.iis": {EverySec: 60}}
	st := c.CollectorStates()
	if len(st) != len(Catalogue()) {
		t.Fatalf("states %d, catalogue %d", len(st), len(Catalogue()))
	}
	for _, s := range st {
		switch s.ID {
		case "host.tcp":
			if s.On {
				t.Error("host.tcp should be off")
			}
		case "web.iis":
			if !s.On || s.EverySec != 60 {
				t.Errorf("web.iis: %+v", s)
			}
		default:
			if !s.On || s.EverySec != 15 {
				t.Errorf("%s should be on every 15 s, got %+v", s.ID, s)
			}
		}
	}
}

func TestBuildGroupsSkipsSwitchedOffCollectors(t *testing.T) {
	swapRegistry(t)
	for _, n := range []string{"tcp", "vm", "diskCpu"} {
		name, optional := n, n != "diskCpu"
		RegisterGroup(name, optional, func(GroupDeps) (Group, error) { return &stubGroup{name: name}, nil })
	}
	cfg := Default()
	cfg.Name = "bb"
	// Not validated on purpose: the always-on guard in BuildGroups must hold even for a config that skipped Validate.
	cfg.Collectors = map[string]CollectorSetting{"host.tcp": {On: boolp(false)}, "host.diskcpu": {On: boolp(false)}}
	var got []string
	for _, g := range BuildGroups(GroupDeps{Config: cfg}) {
		got = append(got, g.Name())
	}
	if strings.Join(got, ",") != "diskCpu,vm" {
		t.Errorf("built %v, want diskCpu and vm only", got)
	}
}

func TestEveryGroupRunsOncePerPeriodAndLeavesNoTrace(t *testing.T) {
	inner := &stubGroup{name: "tcp"}
	g := &everyGroup{inner: inner, every: time.Minute}
	t0 := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	ran := 0
	for i := 0; i < 8; i++ { // eight 15 s ticks = two minutes
		_, err := g.Collect(t0.Add(time.Duration(i)*15*time.Second), 15)
		if err == nil {
			ran++
		} else if !errors.Is(err, errNotApplicable) {
			t.Fatalf("tick %d: %v", i, err)
		}
	}
	if ran != 2 || inner.calls != 2 {
		t.Errorf("ran %d times (inner %d), want 2", ran, inner.calls)
	}
	// A tick a little early still counts as the next period.
	g2 := &everyGroup{inner: &stubGroup{name: "tcp"}, every: time.Minute}
	_, _ = g2.Collect(t0, 15)
	if _, err := g2.Collect(t0.Add(58*time.Second), 15); err != nil {
		t.Errorf("a tick 2 s early skipped a whole period: %v", err)
	}
}

func TestBuildGroupsSlowsACollector(t *testing.T) {
	swapRegistry(t)
	stub := &stubGroup{name: "tcp"}
	RegisterGroup("tcp", true, func(GroupDeps) (Group, error) { return stub, nil })
	cfg := Default()
	cfg.Name = "bb"
	cfg.Collectors = map[string]CollectorSetting{"host.tcp": {EverySec: 60}}
	gs := BuildGroups(GroupDeps{Config: cfg})
	if len(gs) != 1 {
		t.Fatalf("built %d groups", len(gs))
	}
	t0 := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 4; i++ {
		_, _ = gs[0].Collect(t0.Add(time.Duration(i)*15*time.Second), 15)
	}
	if stub.calls != 1 {
		t.Errorf("inner ran %d times in one minute, want 1", stub.calls)
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
