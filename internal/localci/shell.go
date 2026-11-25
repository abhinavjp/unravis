package localci

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

// knownShell reports whether the runner can run a step with this shell.
func knownShell(s string) bool {
	switch s {
	case "bash", "sh", "pwsh", "powershell", "cmd":
		return true
	}
	return false
}

// gitBashDir is the Git for Windows bin directory.
const gitBashDir = `C:\Program Files\Git\bin`

// findBash returns the bash to use. On Windows Git Bash wins over the WSL launcher in System32.
func findBash() (string, error) {
	if runtime.GOOS == "windows" {
		p := filepath.Join(gitBashDir, "bash.exe")
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return exec.LookPath("bash")
}

func findSh() (string, error) {
	if runtime.GOOS == "windows" {
		p := filepath.Join(gitBashDir, "sh.exe")
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return exec.LookPath("sh")
}

// shellCmd is a ready-to-start command line for a script.
type shellCmd struct {
	Path    string
	Args    []string
	RawLine string // Windows cmd.exe only
	File    string // the script file
}

// writeScript writes the script for a shell under dir (base name stem) and returns the command to run it.
func writeScript(shell, script, dir, stem string) (*shellCmd, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	write := func(ext, text string) (string, error) {
		p := filepath.Join(dir, stem+ext)
		return p, os.WriteFile(p, []byte(text), 0o700) //nolint:gosec // the generated script must be executable by the user
	}
	switch shell {
	case "bash":
		exe, err := findBash()
		if err != nil {
			return nil, fmt.Errorf("shell bash: bash not found (Git Bash is expected at %s): %w", gitBashDir, err)
		}
		f, err := write(".sh", script)
		if err != nil {
			return nil, err
		}
		return &shellCmd{Path: exe, Args: []string{"--noprofile", "--norc", "-eo", "pipefail", filepath.ToSlash(f)}, File: f}, nil
	case "sh":
		exe, err := findSh()
		if err != nil {
			return nil, fmt.Errorf("shell sh: sh not found: %w", err)
		}
		f, err := write(".sh", script)
		if err != nil {
			return nil, err
		}
		return &shellCmd{Path: exe, Args: []string{"-e", filepath.ToSlash(f)}, File: f}, nil
	case "pwsh", "powershell":
		exe, err := exec.LookPath(shell)
		if err != nil {
			return nil, fmt.Errorf("shell %s: %s not found on PATH: %w", shell, shell, err)
		}
		text := "$ErrorActionPreference = 'Stop'\n" + script +
			"\nif ((Test-Path -LiteralPath variable:\\LASTEXITCODE)) { exit $LASTEXITCODE }\n"
		f, err := write(".ps1", text)
		if err != nil {
			return nil, err
		}
		quoted := strings.ReplaceAll(f, "'", "''")
		return &shellCmd{Path: exe, Args: []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", ". '" + quoted + "'"}, File: f}, nil
	case "cmd":
		exe, err := exec.LookPath("cmd")
		if err != nil {
			return nil, fmt.Errorf("shell cmd: cmd not found: %w", err)
		}
		f, err := write(".cmd", strings.ReplaceAll(script, "\n", "\r\n"))
		if err != nil {
			return nil, err
		}
		// with /S cmd strips the outer quotes and keeps CALL "script", so a path with spaces or & works
		line := `"CALL "` + f + `""`
		return &shellCmd{Path: exe, Args: []string{"/D", "/E:ON", "/V:OFF", "/S", "/C", line},
			RawLine: `"` + exe + `" /D /E:ON /V:OFF /S /C ` + line, File: f}, nil
	}
	return nil, fmt.Errorf("unsupported: shell %s", shell)
}

// ---------------------------------------------------------------- special files

type kvPair struct{ Key, Value string }

// parseCommandFile parses a GITHUB_OUTPUT / GITHUB_ENV file: name=value lines and the
// name<<DELIM ... DELIM multi-line form.
func parseCommandFile(data string) ([]kvPair, error) {
	var out []kvPair
	lines := strings.Split(data, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimSuffix(lines[i], "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		eq := strings.Index(line, "=")
		hd := strings.Index(line, "<<")
		if hd > 0 && (eq < 0 || hd < eq) {
			key, delim := line[:hd], line[hd+2:]
			if delim == "" {
				return nil, fmt.Errorf("invalid format %q: empty delimiter", line)
			}
			var val []string
			closed := false
			for i++; i < len(lines); i++ {
				l := strings.TrimSuffix(lines[i], "\r")
				if l == delim {
					closed = true
					break
				}
				val = append(val, l)
			}
			if !closed {
				return nil, fmt.Errorf("matching delimiter %q not found for %q", delim, key)
			}
			out = append(out, kvPair{key, strings.Join(val, "\n")})
			continue
		}
		if eq <= 0 {
			return nil, fmt.Errorf("invalid format %q", line)
		}
		out = append(out, kvPair{line[:eq], line[eq+1:]})
	}
	return out, nil
}

// ---------------------------------------------------------------- masks and lines

// masker replaces registered secret values by ***.
type masker struct {
	mu   sync.RWMutex
	vals []string
}

func (m *masker) add(v string) {
	v = strings.TrimSpace(v)
	if v == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, x := range m.vals {
		if x == v {
			return
		}
	}
	m.vals = append(m.vals, v)
	sort.SliceStable(m.vals, func(i, j int) bool { return len(m.vals[i]) > len(m.vals[j]) })
}

func (m *masker) apply(s string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, v := range m.vals {
		s = strings.ReplaceAll(s, v, "***")
	}
	return s
}

// maxLineBytes caps one emitted line: a program that prints without newlines cannot grow the buffer.
const maxLineBytes = 64 * 1024

// lineWriter splits written bytes into lines and calls emit for each complete line. A line longer than
// maxLineBytes is emitted in pieces of at most that size.
type lineWriter struct {
	mu   sync.Mutex
	buf  []byte
	emit func(string)
}

// cutAt returns where to cut a line of n bytes at the start of b: n, or maxLineBytes moved back to a rune start.
func cutAt(b []byte, n int) int {
	if n <= maxLineBytes {
		return n
	}
	c := maxLineBytes
	for c > 0 && !utf8.RuneStart(b[c]) {
		c--
	}
	if c == 0 {
		return maxLineBytes
	}
	return c
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			if len(w.buf) <= maxLineBytes {
				break
			}
			c := cutAt(w.buf, len(w.buf))
			w.emit(string(w.buf[:c]))
			w.buf = w.buf[c:]
			continue
		}
		if i > maxLineBytes {
			c := cutAt(w.buf, i)
			w.emit(string(w.buf[:c]))
			w.buf = w.buf[c:]
			continue
		}
		w.emit(strings.TrimSuffix(string(w.buf[:i]), "\r"))
		w.buf = w.buf[i+1:]
	}
	if len(w.buf) == 0 {
		w.buf = nil // do not keep a big backing array alive
	}
	return len(p), nil
}

// Flush emits a trailing partial line.
func (w *lineWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.buf) > 0 {
		w.emit(strings.TrimSuffix(string(w.buf), "\r"))
		w.buf = nil
	}
}

// ---------------------------------------------------------------- environment lists

// envList is an ordered KEY=VALUE list with replace-on-set semantics (case-insensitive on Windows).
type envList struct{ items []string }

func splitKV(kv string) (k, v string, ok bool) {
	if kv == "" {
		return "", "", false
	}
	i := strings.Index(kv[1:], "=")
	if i < 0 {
		return "", "", false
	}
	i++
	return kv[:i], kv[i+1:], true
}

func newEnvList(base []string) *envList {
	e := &envList{}
	for _, kv := range base {
		if k, v, ok := splitKV(kv); ok {
			e.set(k, v)
		}
	}
	return e
}

func sameKey(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func (e *envList) get(k string) (string, bool) {
	for _, kv := range e.items {
		if ek, ev, ok := splitKV(kv); ok && sameKey(ek, k) {
			return ev, true
		}
	}
	return "", false
}

func (e *envList) set(k, v string) {
	for n, kv := range e.items {
		if ek, _, ok := splitKV(kv); ok && sameKey(ek, k) {
			e.items[n] = ek + "=" + v
			return
		}
	}
	e.items = append(e.items, k+"="+v)
}

// lookPathEnv resolves an executable name against the PATH of an environment list.
func lookPathEnv(name string, env []string) (string, error) {
	if strings.ContainsAny(name, `/\`) {
		return name, nil
	}
	pathVal, _ := newEnvList(env).get("PATH")
	exts := []string{""}
	if runtime.GOOS == "windows" {
		pe, _ := newEnvList(env).get("PATHEXT")
		if pe == "" {
			pe = ".COM;.EXE;.BAT;.CMD"
		}
		exts = append(exts, strings.Split(strings.ToLower(pe), ";")...)
	}
	for _, dir := range filepath.SplitList(pathVal) {
		if dir == "" {
			continue
		}
		for _, x := range exts {
			p := filepath.Join(dir, name+x)
			if st, err := os.Stat(p); err == nil && !st.IsDir() && (runtime.GOOS == "windows" || st.Mode()&0o111 != 0) {
				return p, nil
			}
		}
	}
	return "", errors.New("executable file not found in PATH: " + name)
}
