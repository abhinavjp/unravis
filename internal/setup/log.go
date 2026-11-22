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

// quotedOrBraced is a quoted or braced value. A quote inside a quoted value is doubled (as in an apostrophe written twice, the ADO way)
// or escaped with a backslash (JSON and Python repr), and "}}" is an escaped brace. The closing mark is optional: a value that never
// closes runs to the end of the text.
const quotedOrBraced = `"(?:[^"\\]|\\(?s:.)|"")*"?|'(?:[^'\\]|\\(?s:.)|'')*'?|\{(?:[^}]|\}\})*\}?`

// loginValue is the value of a login-password key: a quoted or braced value, else text up to the next ";" or the end of the line, else
// (nothing after the key on its line) the text after one line break (CRLF, LF or a lone CR) and spaces. Blank lines end the search.
const loginValue = `(?:` + quotedOrBraced + `|[^;\r\n]+|(?:(?:\r\n?|\n)[ \t]*(?:` + quotedOrBraced + `|[^;\r\n]+))?)`

// loginKey is a login-password key with any prefix (DB_PASSWORD, MYSQL_PWD, spring.datasource.password). "pass" counts only after a
// separator (db_pass), so "bypass" is left alone.
const loginKey = `[\w.-]*(?:password|passwd|pwd|[_.-]pass)`

// xmlKeyName is an XML element or attribute-value name that holds a password; xmlKeyAttr is a web.config key="..." or name="..." attribute.
const xmlKeyName = `[\w.:-]*(?:password|passwd|pwd)`
const xmlKeyAttr = `\b(?:key|name)\s*=\s*(?:"` + xmlKeyName + `"|'` + xmlKeyName + `')`

// The setup log never holds a secret. Every line goes through Redact, which hides anything shaped like a Blackbox read key, a launch
// token in a link, or a password setting, in case one ever reaches a message by mistake. The read key itself is never given to the log.
var redactions = []struct {
	re   *regexp.Regexp
	with string
}{
	{regexp.MustCompile(`(?i)ubk-[a-z0-9-]*`), "ubk-[hidden]"},
	{regexp.MustCompile(`(?i)([#?&](?:t|k|key|token|launch)=)[^\s&"']+`), "${1}[hidden]"},
	// XML: <password>x y</password> (the tags stay) and web.config pairs <add key="Password" value="x y"/>, the value before or after the key.
	{regexp.MustCompile(`(?is)(<` + xmlKeyName + `(?:\s[^<>]*)?>)(?:<!\[CDATA\[.*?\]\]>|[^<]*)(</` + xmlKeyName + `\s*>)`), "${1}[hidden]${2}"},
	{regexp.MustCompile(`(?i)(<[\w:.-]+\s[^<>]*?` + xmlKeyAttr + `[^<>]*?\bvalue\s*=\s*)(?:"[^"]*"|'[^']*')`), `${1}"[hidden]"`},
	{regexp.MustCompile(`(?i)(<[\w:.-]+\s[^<>]*?\bvalue\s*=\s*)(?:"[^"]*"|'[^']*')([^<>]*?` + xmlKeyAttr + `)`), `${1}"[hidden]"${2}`},
	// A password in a URL query string (?password=x&y=1) ends at "&", ";", "," or a space.
	{regexp.MustCompile(`(?i)([?&]` + loginKey + `[ \t]*[=:][ \t]*)(?:` + quotedOrBraced + `|[^\s&;,]+)`), "${1}[hidden]"},
	// Any other password key: a password may hold spaces, so an unquoted value runs to the next ";" or the end of the line.
	// When nothing follows the key on its line, the value is the text after one line break; blank lines end the search.
	{regexp.MustCompile(`(?im)(^|[^?&\w.-])(` + loginKey + `[ \t]*=[ \t]*)` + loginValue), "${1}${2}[hidden]"},
	// The colon form (password: x, DB_PASSWORD: x, "password": "x", 'pwd': 'x') counts only when the key starts a line or follows a
	// space, quote, "{", "[", "(" or ",". "password policy: 8" and "the password was wrong" have no colon right after the key and stay.
	{regexp.MustCompile(`(?im)(^|[\s"'{\[(,])([\w.-]*(?:password|passwd|pwd)["']?[ \t]*:[ \t]*)` + loginValue), "${1}${2}[hidden]"},
	{regexp.MustCompile(`(?i)\b(secret|readkey)(\s*[=:]\s*)("[^"]*"|'[^']*'|[^\s;,]+)`), "${1}${2}[hidden]"},
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

// DefaultLogPath is where setup keeps its log when no --log is given (installer-and-releases.md section 4):
// %LOCALAPPDATA%\Unravis\setup-logs\setup-<yyyymmdd-hhmmss>.log. The elevated worker writes to its own file next to it
// (worker true), so the two never share one file.
func DefaultLogPath(localAppData string, now time.Time, worker bool) string {
	name := "setup-" + now.Format("20060102-150405")
	if worker {
		name += "-admin"
	}
	return filepath.Join(localAppData, "Unravis", "setup-logs", name+".log")
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
