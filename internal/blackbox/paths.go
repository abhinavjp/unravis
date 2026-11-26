package blackbox

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// B7: path-only collector. It records WHERE the logs and settings files of the stack are: path, whether it exists,
// whether it is a folder, size and last write time. Never a file's content and never a folder listing. This is the
// "at least the path of everything" kind (PLAN 4.1); the timeline and the pull-a-bundle step use it to know what to
// ask for. It runs once an hour by default (DefaultEverySec in the catalogue).

func init() { RegisterGroup("paths", true, newPathsGroup) }

const maxExtraPaths = 20

// PathSettings is the "paths" block of blackbox.json.
type PathSettings struct {
	// Extra are further files or folders to report, on top of the fixed list. Full local paths only.
	Extra []string `json:"extra,omitempty"`
}

func (s PathSettings) validate() error {
	if len(s.Extra) > maxExtraPaths {
		return invalidf(`"paths.extra" holds at most %d paths`, maxExtraPaths)
	}
	seen := map[string]bool{}
	for _, p := range s.Extra {
		if p == "" || len(p) > 260 || !filepath.IsAbs(p) {
			return invalidf(`"paths.extra" entry %q must be a full path`, p)
		}
		// The recorder runs as SYSTEM: a network share would make this PC's account sign in to another machine (SEC-07).
		if strings.HasPrefix(strings.ReplaceAll(p, "/", `\`), `\\`) {
			return invalidf(`"paths.extra" entry %q is a network share: list a path on this PC instead`, p)
		}
		for _, r := range p {
			if r < 0x20 || strings.ContainsRune(`*?"<>|`, r) {
				return invalidf(`"paths.extra" entry %q has characters a path cannot have`, p)
			}
		}
		key := strings.ToLower(p)
		if seen[key] {
			return invalidf(`"paths.extra" lists %q twice`, p)
		}
		seen[key] = true
	}
	return nil
}

// FixedPaths are the places the stack keeps its logs and settings on a standard install. They are fixed in code:
// settings can add paths (paths.extra) but not change these.
func FixedPaths() []string {
	return []string{
		`C:\Windows\System32\LogFiles\HTTPERR`,
		`C:\inetpub\logs\FailedReqLogFiles`,
		`C:\Windows\System32\inetsrv\config\applicationHost.config`,
		`C:\Program Files\Microsoft SQL Server`,
		`C:\Program Files\Redis\redis.windows.conf`,
		`C:\Program Files\Redis\Logs`,
		`C:\ProgramData\Salt Project\Salt\var\log\salt\minion`,
		`C:\ProgramData\Salt Project\Salt\conf\minion`,
	}
}

// PathFact is one reported path.
type PathFact struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
	Dir    bool   `json:"dir,omitempty"`
	Size   int64  `json:"size,omitempty"`    // bytes; 0 for a folder
	ModT   int64  `json:"modUnix,omitempty"` // last write, Unix seconds
}

// PathsGroup is the sample block of the path collector.
type PathsGroup struct {
	Paths []PathFact `json:"paths"`
}

type pathsGroup struct {
	list []string
	stat func(string) (os.FileInfo, error)
}

func newPathsGroup(d GroupDeps) (Group, error) {
	list := append([]string(nil), d.Config.LogPaths...)
	list = append(list, FixedPaths()...)
	if d.Config.Paths != nil {
		list = append(list, d.Config.Paths.Extra...)
	}
	// One entry per path, in a stable order; case differences are one path on Windows.
	seen := map[string]bool{}
	var uniq []string
	for _, p := range list {
		k := strings.ToLower(p)
		if !seen[k] {
			seen[k] = true
			uniq = append(uniq, p)
		}
	}
	sort.Strings(uniq)
	return &pathsGroup{list: uniq, stat: os.Stat}, nil
}

func (g *pathsGroup) Name() string   { return "paths" }
func (g *pathsGroup) Optional() bool { return true }

func (g *pathsGroup) Collect(time.Time, float64) (any, error) {
	out := PathsGroup{Paths: make([]PathFact, 0, len(g.list))}
	for _, p := range g.list {
		f := PathFact{Path: p}
		if fi, err := g.stat(p); err == nil {
			f.Exists = true
			f.Dir = fi.IsDir()
			if !f.Dir {
				f.Size = fi.Size()
			}
			f.ModT = fi.ModTime().Unix()
		}
		out.Paths = append(out.Paths, f)
	}
	return &out, nil
}
