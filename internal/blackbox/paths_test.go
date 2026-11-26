package blackbox

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPathSettingsValidation(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "app.log")
	cases := []struct {
		name string
		p    PathSettings
		ok   bool
	}{
		{"full path", PathSettings{Extra: []string{good}}, true},
		{"relative", PathSettings{Extra: []string{"logs/app.log"}}, false},
		{"empty", PathSettings{Extra: []string{""}}, false},
		{"share", PathSettings{Extra: []string{`\\server\share\x.log`}}, false},
		{"share with slashes", PathSettings{Extra: []string{"//server/share/x.log"}}, false},
		{"folder jump", PathSettings{Extra: []string{filepath.Join(dir, "a") + string(filepath.Separator) + ".." + string(filepath.Separator) + "b"}}, false},
		{"stream name", PathSettings{Extra: []string{good + ":hidden:$DATA"}}, false},
		{"delete char", PathSettings{Extra: []string{good + "\x7f"}}, false},
		{"same path with a trailing slash", PathSettings{Extra: []string{good, good + string(filepath.Separator)}}, false},
		{"wildcard", PathSettings{Extra: []string{filepath.Join(dir, "*.log")}}, false},
		{"control char", PathSettings{Extra: []string{good + "\n"}}, false},
		{"twice", PathSettings{Extra: []string{good, strings.ToUpper(good)}}, false},
		{"too long", PathSettings{Extra: []string{filepath.Join(dir, strings.Repeat("a", 300))}}, false},
	}
	for _, tc := range cases {
		if err := tc.p.validate(); (err == nil) != tc.ok {
			t.Errorf("%s: err=%v, want ok=%v", tc.name, err, tc.ok)
		}
	}
	many := PathSettings{}
	for i := 0; i <= maxExtraPaths; i++ {
		many.Extra = append(many.Extra, filepath.Join(dir, strings.Repeat("a", i+1)))
	}
	if many.validate() == nil {
		t.Error("too many paths were accepted")
	}
	c := testConfig(t)
	c.Paths = &PathSettings{Extra: []string{"relative"}}
	if c.Validate() == nil {
		t.Error("Config.Validate does not check the paths block")
	}
}

// The group reports path, existence, kind, size and time, and never opens a file.
func TestPathsGroupReportsMetadataOnly(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "redis.conf")
	secret := "requirepass hunter2"
	if err := os.WriteFile(file, []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "nope.log")
	cfg := testConfig(t)
	cfg.LogPaths = []string{dir}
	cfg.Paths = &PathSettings{Extra: []string{file, missing}}
	g, err := newPathsGroup(GroupDeps{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	v, err := g.Collect(time.Now(), 15)
	if err != nil {
		t.Fatal(err)
	}
	grp := v.(*PathsGroup)
	byPath := map[string]PathFact{}
	for _, f := range grp.Paths {
		byPath[f.Path] = f
	}
	if f := byPath[file]; !f.Exists || f.Dir || f.Size != int64(len(secret)) || f.ModT == 0 {
		t.Errorf("file fact %+v", f)
	}
	if f := byPath[dir]; !f.Exists || !f.Dir || f.Size != 0 {
		t.Errorf("folder fact %+v", f)
	}
	if f := byPath[missing]; f.Exists || f.Size != 0 {
		t.Errorf("missing fact %+v", f)
	}
	if f := byPath[missing]; f.Err != "" {
		t.Errorf("a path that is not there carries an error: %+v", f)
	}
	for _, fixed := range FixedPaths() {
		if _, ok := byPath[fixed]; !ok {
			t.Errorf("fixed path %q is not reported", fixed)
		}
	}
	var s Sample
	applyGroup(&s, "paths", grp)
	if s.Paths != grp {
		t.Error("applyGroup does not store the paths group")
	}
	if b, _ := marshalForTest(s); strings.Contains(string(b), "hunter2") {
		t.Error("file content reached the sample")
	}
}

func TestPathsListIsStableAndDeduplicated(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig(t)
	cfg.LogPaths = []string{dir, strings.ToUpper(dir)}
	cfg.Paths = &PathSettings{Extra: []string{dir}}
	g, _ := newPathsGroup(GroupDeps{Config: cfg})
	list := g.(*pathsGroup).list
	seen := map[string]bool{}
	for i, p := range list {
		k := strings.ToLower(p)
		if seen[k] {
			t.Errorf("%q appears twice", p)
		}
		seen[k] = true
		if i > 0 && list[i-1] > p {
			t.Errorf("not sorted at %q", p)
		}
	}
}

func TestCollectorEveryUsesTheCollectorsDefault(t *testing.T) {
	c := testConfig(t)
	if got := c.CollectorEvery("paths.files"); got != time.Hour {
		t.Errorf("default %v, want 1h", got)
	}
	c.Collectors = map[string]CollectorSetting{"paths.files": {EverySec: 300}}
	if got := c.CollectorEvery("paths.files"); got != 5*time.Minute {
		t.Errorf("set to 300 s: %v", got)
	}
	if got := c.CollectorEvery("host.tcp"); got != 15*time.Second {
		t.Errorf("tcp %v, want the sample interval", got)
	}
	// An explicit "every sample" setting beats the collector's default.
	c.Collectors = map[string]CollectorSetting{"paths.files": {EverySec: 15}}
	if got := c.CollectorEvery("paths.files"); got != 15*time.Second {
		t.Errorf("explicit 15 s: %v", got)
	}
}

func marshalForTest(s Sample) ([]byte, error) { return json.Marshal(s) }

// A path that exists but cannot be looked at is never reported as missing.
func TestPathsGroupSaysWhenAPathCouldNotBeRead(t *testing.T) {
	g, _ := newPathsGroup(GroupDeps{Config: testConfig(t)})
	pg := g.(*pathsGroup)
	pg.list = []string{"a", "b", "c"}
	pg.stat = func(p string) (os.FileInfo, error) {
		switch p {
		case "a":
			return nil, fs.ErrPermission
		case "b":
			return nil, fs.ErrNotExist
		}
		return nil, errors.New("device not ready")
	}
	v, _ := g.Collect(time.Now(), 15)
	got := v.(*PathsGroup).Paths
	if got[0].Err != "denied" || got[0].Exists {
		t.Errorf("denied: %+v", got[0])
	}
	if got[1].Err != "" || got[1].Exists {
		t.Errorf("absent: %+v", got[1])
	}
	if got[2].Err != "error" {
		t.Errorf("other error: %+v", got[2])
	}
}

// logPaths entries only get the folder checks in Validate; the paths group holds them to the same rules.
func TestPathsGroupSkipsBadLogPathEntries(t *testing.T) {
	cfg := testConfig(t)
	cfg.LogPaths = []string{cfg.Folder, "relative", "//server/share", cfg.Folder + "\x01"}
	g, _ := newPathsGroup(GroupDeps{Config: cfg})
	fixed := map[string]bool{}
	for _, p := range FixedPaths() {
		fixed[p] = true
	}
	for _, p := range g.(*pathsGroup).list {
		if !fixed[p] && pathEntryProblem(p) != "" {
			t.Errorf("%q should have been skipped", p)
		}
	}
}
