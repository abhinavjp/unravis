package releasedash

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
)

// The release script keeps two kinds of log in <data folder>\logs:
//
//	run-<yyyyMMddTHHmmssZ>.log                       what Release.ps1 printed, one line per row, each starting "<iso time>Z "
//	ci-<tag>-<ci|release|verify>-<runid>.log.gz      a GitHub Actions run log (gzip), saved after the run so the tests can be read later
//
// Both are redacted when written and again when read here.
const (
	logsDir      = "logs"
	maxRunLines  = 20000
	maxCIBytes   = 64 << 20
	maxCILines   = 400000
	liveTailGrab = 4 << 20 // a run log is read from the end when it is bigger than this
)

var (
	runLogRE = regexp.MustCompile(`^run-(\d{8}T\d{6}Z)\.log$`)
	ciLogRE  = regexp.MustCompile(`^ci-(v\d[0-9A-Za-z.+-]{0,40})-(ci|release|verify)-(\d{1,20})\.log\.gz$`)
	stepHdr  = regexp.MustCompile(`^\[(\d)/7\] `)
	stopRE   = regexp.MustCompile(`^STOPPED at (\S+) : (.*)$`)
)

// LogRef names one saved log. Name is the bare file name (the only thing the page can ask for).
type LogRef struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`          // run, ci, release or verify
	Tag   string `json:"tag,omitempty"` // CI logs only
	Run   string `json:"run,omitempty"` // CI logs only
	Start string `json:"start"`         // when the log began
	End   string `json:"end"`           // last time it was written
	Bytes int64  `json:"bytes"`
}

// ListLogs lists the saved logs, newest first. A missing folder is normal.
func ListLogs(dir string) []LogRef {
	ents, err := os.ReadDir(filepath.Join(dir, logsDir))
	if err != nil {
		return nil
	}
	var out []LogRef
	for _, e := range ents {
		if e.IsDir() || !e.Type().IsRegular() {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		n := e.Name()
		ref := LogRef{Name: n, Bytes: fi.Size(), End: fi.ModTime().UTC().Format(time.RFC3339)}
		if m := runLogRE.FindStringSubmatch(n); m != nil {
			t, err := time.Parse("20060102T150405Z", m[1])
			if err != nil {
				continue
			}
			ref.Kind, ref.Start = "run", t.UTC().Format(time.RFC3339)
		} else if m := ciLogRE.FindStringSubmatch(n); m != nil {
			ref.Kind, ref.Tag, ref.Run = m[2], m[1], m[3]
			ref.Start = ref.End
		} else {
			continue
		}
		out = append(out, ref)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start > out[j].Start })
	return out
}

// validLogName: only a name that ListLogs would list is ever opened (no folders, no "..").
func validLogName(n string) bool { return runLogRE.MatchString(n) || ciLogRE.MatchString(n) }

// LogLine is one row of a log. T is the time it was written (run logs; empty for CI logs, which carry their own times in Text).
type LogLine struct {
	T    string `json:"t,omitempty"`
	Text string `json:"x"`
}

// ReadLog reads and redacts a saved log. names are the machine and user names to hide too. For a run log that is too long, the
// newest lines are kept. cut tells the page the log was shortened.
//
// The answer is kept for the next call while the file is unchanged (same size and time), because the page asks for a long log one
// page at a time and every page used to unpack, redact and index the whole log again (a 64 MiB CI log took 22 s and 1.4 GB each time).
// The returned lines are shared: callers must not change them.
func ReadLog(dir, name string, names []string) (lines []LogLine, cut bool, err error) {
	if !validLogName(name) {
		return nil, false, os.ErrNotExist
	}
	path := filepath.Join(dir, logsDir, name)
	fi, err := os.Stat(path)
	if err != nil {
		return nil, false, err
	}
	key := logKey{path: path, names: strings.Join(names, "\x00"), size: fi.Size(), mod: fi.ModTime().UnixNano()}
	logCache.mu.Lock() // one reader at a time: concurrent misses would each hold a whole log
	defer logCache.mu.Unlock()
	for _, e := range logCache.items {
		if e.key == key {
			return e.lines, e.cut, nil
		}
	}
	lines, cut, err = readLogFile(path, name, names)
	if err == nil {
		logCache.items = append(logCache.items, logEntry{key, lines, cut})
		if len(logCache.items) > logCacheEntries {
			logCache.items = logCache.items[1:]
		}
	}
	return lines, cut, err
}

const logCacheEntries = 3

type logKey struct {
	path, names string
	size, mod   int64
}

type logEntry struct {
	key   logKey
	lines []LogLine
	cut   bool
}

var logCache struct {
	mu    sync.Mutex
	items []logEntry
}

func readLogFile(path, name string, names []string) (lines []LogLine, cut bool, err error) {
	f, err := openShared(path)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = f.Close() }()
	var rd io.Reader = f
	limit, maxLines := int64(liveTailGrab), maxRunLines
	if ciLogRE.MatchString(name) {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return nil, false, err
		}
		defer func() { _ = gz.Close() }()
		rd, limit, maxLines = gz, maxCIBytes, maxCILines
	} else if fi, err := f.Stat(); err == nil && fi.Size() > limit {
		if _, err := f.Seek(fi.Size()-limit, io.SeekStart); err == nil {
			cut = true
		}
	}
	rd = io.LimitReader(rd, limit+1)
	red := NewRedactor(names...)
	sc := bufio.NewScanner(rd)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	first := cut // the first line of a read that started mid-file is partial
	for sc.Scan() {
		if first {
			first = false
			continue
		}
		raw := sc.Text()
		ln := LogLine{}
		if runLogRE.MatchString(name) {
			ln.T, raw = splitStamp(raw)
			raw = fixMojibake(raw)
		}
		ln.Text = red.Line(raw)
		lines = append(lines, ln)
		if len(lines) > maxLines {
			lines, cut = lines[len(lines)-maxLines:], true
		}
	}
	if runLogRE.MatchString(name) {
		lines = collapseRefresh(lines)
	}
	if lines == nil {
		lines = []LogLine{} // an empty log is an empty list for the page, not null
	}
	return lines, cut, nil
}

// splitStamp splits "2026-11-22T07:55:28Z text" into its time and text. A line without a time keeps all of its text.
func splitStamp(s string) (string, string) {
	if i := strings.IndexByte(s, ' '); i > 19 && i <= 32 {
		if _, err := time.Parse(time.RFC3339Nano, s[:i]); err == nil {
			return s[:i], s[i+1:]
		}
	}
	return "", s
}

// fixMojibake repairs text that the release script's child process wrote as UTF-8 but the parent console read as the OEM code page
// 437 ("✓" arriving as "Γ£ô", "·" as "┬╖"). It only acts when every character of the line fits code page 437 AND the resulting bytes
// are valid UTF-8, so an honest "café" or "Γ" on its own is left alone.
func fixMojibake(s string) string {
	ascii := true
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii {
		return s
	}
	b, err := charmap.CodePage437.NewEncoder().String(s)
	if err != nil || !utf8.ValidString(b) || b == s {
		return s
	}
	return b
}

var refreshRE = regexp.MustCompile(`^Refreshing run status every \d+ seconds`)

// collapseRefresh folds the repeated status blocks that "gh run watch" redraws every ten seconds (one "Refreshing run status ..."
// line followed by the run and job list, tens of lines each time) into the last one, with a note where the others were. A block
// ends at the next one or at the next "[n/7]" step heading. Done on the lines the page asks for, so line numbers stay consistent.
func collapseRefresh(lines []LogLine) []LogLine {
	var starts []int
	for i, l := range lines {
		if refreshRE.MatchString(l.Text) {
			starts = append(starts, i)
		}
	}
	if len(starts) < 2 {
		return lines
	}
	dropEnd := map[int]int{} // frame start -> end (exclusive) for frames that are folded away
	for k := 0; k+1 < len(starts); k++ {
		heading := false
		for j := starts[k] + 1; j < starts[k+1]; j++ {
			if stepHdr.MatchString(lines[j].Text) {
				heading = true
				break
			}
		}
		if !heading {
			dropEnd[starts[k]] = starts[k+1]
		}
	}
	if len(dropEnd) == 0 {
		return lines
	}
	out := make([]LogLine, 0, len(lines))
	folded := 0
	for i := 0; i < len(lines); {
		if e, ok := dropEnd[i]; ok {
			folded++
			i = e
			continue
		}
		if folded > 0 && refreshRE.MatchString(lines[i].Text) {
			out = append(out, LogLine{T: lines[i].T, Text: fmt.Sprintf("(%d earlier copies of this status block are folded away; the latest one follows)", folded)})
			folded = 0
		}
		out = append(out, lines[i])
		i++
	}
	return out
}

// StepLog is the part of a run log that belongs to one step.
type StepLog struct {
	Stage string `json:"stage"`
	From  int    `json:"from"` // first line (0 based, inclusive)
	To    int    `json:"to"`   // last line (exclusive)
	Start string `json:"start,omitempty"`
	End   string `json:"end,omitempty"`
}

// SliceSteps finds the "[n/7] ..." headings Release.ps1 prints before each step and returns the lines each step owns. Lines
// before the first heading (the bootstrap: locks, pinned commit) are not part of a step.
func SliceSteps(lines []LogLine) []StepLog {
	var out []StepLog
	for i, l := range lines {
		m := stepHdr.FindStringSubmatch(l.Text)
		if m == nil {
			continue
		}
		n := int(m[1][0] - '0')
		if n < 1 || n > len(StageNames) {
			continue
		}
		if k := len(out); k > 0 {
			out[k-1].To = i
		}
		out = append(out, StepLog{Stage: StageNames[n-1], From: i, To: len(lines), Start: l.T})
	}
	for i := range out {
		if out[i].To > out[i].From {
			out[i].End = lines[out[i].To-1].T
		}
	}
	return out
}

// Failure is the line that stopped a release, with a few lines before it.
type Failure struct {
	Stage   string   `json:"stage"`
	Message string   `json:"message"`
	Context []string `json:"context,omitempty"`
	Log     string   `json:"log,omitempty"`  // log file the line is in
	Line    int      `json:"line,omitempty"` // 1 based line number in that log
}

// FindFailure returns the last "STOPPED at <step> : <message>" in a run log, or nil.
func FindFailure(lines []LogLine) *Failure {
	for i := len(lines) - 1; i >= 0; i-- {
		m := stopRE.FindStringSubmatch(lines[i].Text)
		if m == nil {
			continue
		}
		stage := m[1]
		if stage == "Wait" {
			stage = "Build"
		}
		f := &Failure{Stage: stage, Message: m[2], Line: i + 1}
		from := i - 6
		if from < 0 {
			from = 0
		}
		for _, c := range lines[from:i] {
			if strings.TrimSpace(c.Text) != "" {
				f.Context = append(f.Context, c.Text)
			}
		}
		return f
	}
	return nil
}

// openCI opens a saved CI log for reading its raw text (the parser needs the tab separators that ReadLog removes).
func openCI(dir, name string) (io.ReadCloser, error) {
	if !ciLogRE.MatchString(name) {
		return nil, os.ErrNotExist
	}
	f, err := openShared(filepath.Join(dir, logsDir, name))
	if err != nil {
		return nil, err
	}
	gz, err := gzip.NewReader(f)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return &ciReader{io.LimitReader(gz, maxCIBytes), gz, f}, nil
}

type ciReader struct {
	io.Reader
	gz *gzip.Reader
	f  *os.File
}

func (c *ciReader) Close() error { _ = c.gz.Close(); return c.f.Close() }

var foldNoteRE = regexp.MustCompile(`^\((\d+) earlier copies of this status block are folded away`)

// foldRev is the number of status blocks folded away in the lines. While a release is still running every new block folds the
// previous one, which rewrites lines the page already has; the page watches this number and reloads the log from the top when it changes.
func foldRev(lines []LogLine) int {
	n := 0
	for _, l := range lines {
		if m := foldNoteRE.FindStringSubmatch(l.Text); m != nil {
			c, _ := strconv.Atoi(m[1])
			n += c
		}
	}
	return n
}
