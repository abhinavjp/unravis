// Package releasedash is the read-only data behind the Unravis releases dashboard. It reads three things and changes none:
// the release manager's files on this PC (state.json, audit.jsonl), the public GitHub release list, and the signed update feed
// (checked with the same code and keys the app uses). It never signs, never reads a key or passphrase, and sends nothing.
package releasedash

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const (
	auditTailBytes = 1 << 20 // the newest megabyte of the audit log is plenty for the last releases
	maxReason      = 300
)

// DataDir is where Release.ps1 keeps its files: UNRAVIS_RELEASE_DATA, else the local app data folder, as the script does.
func DataDir(getenv func(string) string) string {
	if v := strings.TrimSpace(getenv("UNRAVIS_RELEASE_DATA")); v != "" {
		return v
	}
	if runtime.GOOS == "windows" {
		if c, err := os.UserCacheDir(); err == nil { // %LocalAppData% on Windows
			return filepath.Join(c, "Unravis", "release")
		}
		return ""
	}
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".local", "share", "unravis-release")
	}
	return ""
}

// Run is the state file of the release manager: where the last run got to.
type Run struct {
	Version string `json:"version"`
	Stage   string `json:"stage"`  // last stage that finished
	Status  string `json:"status"` // running, stopped or done
	Reason  string `json:"reason,omitempty"`
	Updated string `json:"updated,omitempty"`
}

// Event is one audit line, cut down to what the screen shows. The machine, user and key name are never put on the page; they are
// kept only to hide them from the logs (see Local.Names). Commit, KeyPub and Manifest are public values (a hash, a public key).
type Event struct {
	At       string
	Name     string
	Version  string
	Stage    string
	Reason   string
	Run      string // CI run id, digits only
	From     string // run-start: the step this run began at
	Commit   string // 7 to 40 hex characters, else empty
	KeyPub   string // preflight-ok: the signing key's public half, 64 hex characters, else empty
	Manifest string // feed-updated: sha256 of the signed manifest, 64 hex characters, else empty
}

// Local is what was found in the release data folder.
type Local struct {
	Found  bool
	Dir    string
	Run    *Run
	Events []Event
	Live   *Live    // the release process on this PC right now, from the very start of Release.ps1
	Names  []string // machine and user names found in the audit log: hidden from every log line the page shows
}

// pathRE hides file paths (they can hold a user name or a key file name) in free text from the log. A Windows or share path
// may hold spaces, so it is cut to the end of the sentence part: hiding too much is better than leaking a tail.
var pathRE = regexp.MustCompile(`[A-Za-z]:[\\/][^"'<>|*?\r\n]*|\\\\[^"'<>|*?\r\n]*|\.{1,2}[\\/][^\s"']*|(?:^|\s)/[\w.~-]+(?:/[\w.~-]*)+|[\w.-]*\.key\.json|[\w.-]*\.pub\b`)

func clip(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return ' '
		}
		return r
	}, s)
	s = pathRE.ReplaceAllStringFunc(s, func(m string) string {
		if m != "" && (m[0] == ' ' || m[0] == '\t') { // a leading space belongs to the text, not to the path
			return m[:1] + "<path>"
		}
		return "<path>"
	})
	if r := []rune(s); len(r) > maxReason {
		s = string(r[:maxReason])
	}
	return strings.TrimSpace(s)
}

func digits(s string) string {
	if s == "" || len(s) > 20 {
		return ""
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return ""
		}
	}
	return s
}

func str(m map[string]any, k string) string { s, _ := m[k].(string); return s }

// hexOnly returns s (lower case) when it is hex of one of the given lengths, else "".
func hexOnly(s string, lens ...int) string {
	s = strings.ToLower(strings.TrimSpace(s))
	ok := false
	for _, n := range lens {
		ok = ok || len(s) == n
	}
	if !ok {
		return ""
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return ""
		}
	}
	return s
}

// ReadLocal reads state.json and the tail of audit.jsonl. A missing folder is normal (this is not the release PC).
func ReadLocal(dir string) Local {
	l := Local{Dir: dir}
	if dir == "" {
		return l
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return l
	}
	l.Found = true
	sp := filepath.Join(dir, "state.json")
	if fi, err := os.Stat(sp); err == nil && fi.Mode().IsRegular() && fi.Size() <= 64<<10 {
		if b, err := readSmall(sp, 64<<10); err == nil {
			var r Run
			if json.Unmarshal(b, &r) == nil && r.Version != "" {
				r.Stage, r.Status = clip(r.Stage), clip(r.Status) // r.Reason is cleaned by hideSecrets: it must be redacted before it is cut
				l.Run = &r
			}
		}
	}
	l.Events, l.Names = readAudit(filepath.Join(dir, "audit.jsonl"))
	l.hideSecrets()
	l.Live = readLive(dir, l.Events, time.Now())
	return l
}

// readSmall reads a whole small file without blocking the release script from replacing it.
func readSmall(path string, max int64) ([]byte, error) {
	f, err := openShared(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(io.LimitReader(f, max))
}

func readAudit(path string) ([]Event, []string) {
	f, err := openShared(path)
	if err != nil {
		return nil, nil
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		return nil, nil
	}
	off := int64(0)
	if fi.Size() > auditTailBytes {
		off = fi.Size() - auditTailBytes
	}
	if _, err := f.Seek(off, 0); err != nil {
		return nil, nil
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 256<<10)
	var out []Event
	seen := map[string]bool{}
	var names []string
	first := off > 0 // the first line of a cut read is partial
	for sc.Scan() {
		if first {
			first = false
			continue
		}
		line := bytes.TrimSpace(sc.Bytes())
		var m map[string]any
		if len(line) == 0 || json.Unmarshal(line, &m) != nil {
			continue
		}
		v := str(m, "version")
		if v == "" {
			v = strings.TrimPrefix(str(m, "tag"), "v")
		}
		if _, err := time.Parse(time.RFC3339Nano, str(m, "ts")); err != nil {
			continue
		}
		for _, k := range []string{"machine", "user"} {
			if n := strings.TrimSpace(str(m, k)); n != "" && !seen[strings.ToLower(n)] && len(names) < 8 {
				seen[strings.ToLower(n)] = true
				names = append(names, n)
			}
		}
		out = append(out, Event{At: str(m, "ts"), Name: clip(str(m, "event")), Version: clip(v), Stage: clip(str(m, "stage")), Reason: str(m, "reason"), Run: digits(str(m, "run")), From: clip(str(m, "from")),
			Commit: hexOnly(str(m, "commit"), 7, 40), KeyPub: hexOnly(str(m, "key_pub"), 64), Manifest: hexOnly(str(m, "manifest_sha256"), 64)})
	}
	return out, names
}

// hideSecrets cleans the two free text fields read from state.json and audit.jsonl: the reason a run stopped. The release script writes
// it as it got it, and that can be the output of a failed command, so a token or passphrase printed by a tool must not reach the page
// by this road (a run log is redacted when it is read; these fields were not). Only the reason is touched: the event name, stage and
// "from" are fixed words the program matches on, and a user or machine name equal to one of them must not change them. The text is
// redacted first and cut second, so a secret that straddles the length limit is not left half showing.
func (l *Local) hideSecrets() {
	if l.Run != nil {
		l.Run.Reason = clip(redactText(l.Run.Reason, l.Names))
	}
	for i := range l.Events {
		l.Events[i].Reason = clip(redactText(l.Events[i].Reason, l.Names))
	}
}

// redactText redacts a text that can run over several lines: each line goes through one Redactor (so a key block that spans lines is
// hidden whole, and a hidden line hides only itself), empty lines are dropped and the rest is joined with a space.
func redactText(s string, names []string) string {
	if s == "" {
		return ""
	}
	r := NewRedactor(names...)
	var parts []string
	for _, ln := range strings.FieldsFunc(s, func(c rune) bool { return c == '\n' || c == '\r' }) {
		t := strings.TrimSpace(r.Line(ln))
		if t == "" || (t == keyHidden && len(parts) > 0 && parts[len(parts)-1] == t) { // one marker for a whole key block
			continue
		}
		parts = append(parts, t)
	}
	return strings.Join(parts, " ")
}
