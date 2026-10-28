package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// The setup log never holds a secret. Every line goes through Redact, which hides anything shaped like a Blackbox read key, a launch
// token in a link, or a password setting, in case one ever reaches a message by mistake. The read key itself is never given to the log.
var redactions = []struct {
	re   *regexp.Regexp
	with string
}{
	{regexp.MustCompile(`(?i)ubk-[a-z0-9-]*`), "ubk-[hidden]"},
	{regexp.MustCompile(`(?i)([#?&](?:t|k|key|token|launch)=)[^\s&"']+`), "${1}[hidden]"},
	{regexp.MustCompile(`(?i)\b(password|pwd|secret|readkey)(\s*[=:]\s*)("[^"]*"|[^\s;,]+)`), "${1}${2}[hidden]"},
}

// Redact hides secrets in a log line.
func Redact(s string) string {
	for _, r := range redactions {
		s = r.re.ReplaceAllString(s, r.with)
	}
	return s
}

// FileLog writes one line per action to a file: the time, then the action and its result.
type FileLog struct {
	mu  sync.Mutex
	f   *os.File
	now func() time.Time
}

// DefaultLogPath is where setup keeps its log when no --log is given: %TEMP%\Unravis-setup-<time>.log.
func DefaultLogPath(now time.Time) string {
	return filepath.Join(os.TempDir(), "Unravis-setup-"+now.Format("20060102-150405")+".log")
}

// OpenFileLog opens (appends to) a log file.
func OpenFileLog(path string) (*FileLog, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return &FileLog{f: f, now: time.Now}, nil
}

// Line writes one line. It is safe to use from several goroutines.
func (l *FileLog) Line(s string) {
	if l == nil {
		return
	}
	s = strings.ReplaceAll(strings.ReplaceAll(Redact(s), "\r", " "), "\n", " ")
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.f, "%s %s\r\n", l.now().UTC().Format(time.RFC3339), s)
}

// Path is the log file's path.
func (l *FileLog) Path() string { return l.f.Name() }

// Close closes the file.
func (l *FileLog) Close() error {
	if l == nil {
		return nil
	}
	return l.f.Close()
}
