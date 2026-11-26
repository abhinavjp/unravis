package blackbox

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
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

// pathEntryProblem says why p cannot be reported ("" when it can): it must be a full local path with no folder jumps,
// no wildcard, no stream name and no control character. Network shares are refused because the recorder runs as SYSTEM
// and would sign in to another machine (SEC-07). A drive letter that is mapped to a share cannot be seen here.
func pathEntryProblem(p string) string {
	if p == "" || utf8.RuneCountInString(p) > 260 {
		return "must be a full path of at most 260 characters"
	}
	if !filepath.IsAbs(p) {
		return "must be a full path"
	}
	if strings.HasPrefix(strings.ReplaceAll(p, "/", `\`), `\\`) {
		return "is a network share: list a path on this PC instead"
	}
	for _, seg := range strings.FieldsFunc(p, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg == ".." {
			return "must not use .."
		}
	}
	if i := strings.Index(p, ":"); i >= 0 && strings.Contains(p[i+1:], ":") {
		return "must not name a stream"
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(`*?"<>|`, r) {
			return "has characters a path cannot have"
		}
	}
	return ""
}

func (s PathSettings) validate() error {
	if len(s.Extra) > maxExtraPaths {
		return invalidf(`"paths.extra" holds at most %d paths`, maxExtraPaths)
	}
	seen := map[string]bool{}
	for _, p := range s.Extra {
		if why := pathEntryProblem(p); why != "" {
			return invalidf(`"paths.extra" entry %q %s`, p, why)
		}
		key := strings.ToLower(filepath.Clean(p))
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
		`C:\Program Files\Memurai\memurai.conf`,
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
	// Err is set when the path could not be looked at for a reason other than "it is not there" (access denied and so
	// on), so a path that exists but is unreadable is never reported as missing.
	Err string `json:"err,omitempty"`
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
	// The fixed list is trusted code; the settings entries (logPaths only get the folder checks in Validate) are held to
	// the same rules as paths.extra here.
	var list []string
	for _, p := range d.Config.LogPaths {
		if pathEntryProblem(p) == "" {
			list = append(list, p)
		}
	}
	if d.Config.Paths != nil {
		for _, p := range d.Config.Paths.Extra {
			if pathEntryProblem(p) == "" {
				list = append(list, p)
			}
		}
	}
	list = append(list, FixedPaths()...)
	// One entry per path, in a stable order; case differences and trailing slashes are one path on Windows.
	seen := map[string]bool{}
	var uniq []string
	for _, p := range list {
		k := strings.ToLower(filepath.Clean(p))
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
		fi, err := g.stat(p)
		switch {
		case err == nil:
			f.Exists = true
			f.Dir = fi.IsDir()
			if !f.Dir {
				f.Size = fi.Size()
			}
			f.ModT = fi.ModTime().Unix()
		case errors.Is(err, fs.ErrNotExist):
			// absent: Exists stays false
		case errors.Is(err, fs.ErrPermission):
			f.Err = "denied"
		default:
			f.Err = "error"
		}
		out.Paths = append(out.Paths, f)
	}
	return &out, nil
}
