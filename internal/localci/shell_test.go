package localci

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseCommandFile(t *testing.T) {
	cases := []struct {
		name, in string
		want     []kvPair
		bad      bool
	}{
		{"simple", "a=1\nb=two words\n", []kvPair{{"a", "1"}, {"b", "two words"}}, false},
		{"equals in value", "a=x=y\n", []kvPair{{"a", "x=y"}}, false},
		{"empty value", "a=\n", []kvPair{{"a", ""}}, false},
		{"multi-line", "m<<EOF\nline1\nline2\nEOF\nz=9\n", []kvPair{{"m", "line1\nline2"}, {"z", "9"}}, false},
		{"crlf", "a=1\r\nm<<X\r\nq\r\nX\r\n", []kvPair{{"a", "1"}, {"m", "q"}}, false},
		{"blank lines", "\n\na=1\n\n", []kvPair{{"a", "1"}}, false},
		{"multi-line containing =", "m<<D\nk=v\nD\n", []kvPair{{"m", "k=v"}}, false},
		{"unterminated", "m<<EOF\nx\n", nil, true},
		{"no equals", "garbage\n", nil, true},
		{"empty delimiter", "m<<\nx\n", nil, true},
	}
	for _, c := range cases {
		got, err := parseCommandFile(c.in)
		if c.bad {
			if err == nil {
				t.Errorf("%s: want error, got %v", c.name, got)
			}
			continue
		}
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, %v want %v", c.name, got, err, c.want)
		}
	}
}

func TestEnvList(t *testing.T) {
	e := newEnvList([]string{"A=1", "B=2", "=C:=C:\\x", "bad"})
	e.set("A", "3")
	e.set("D", "4")
	if v, _ := e.get("A"); v != "3" {
		t.Fatalf("A=%q", v)
	}
	if v, ok := e.get("D"); !ok || v != "4" {
		t.Fatalf("D=%q", v)
	}
	if v, _ := e.get("=C:"); v != `C:\x` {
		t.Fatalf("=C: %q", v)
	}
	if len(e.items) != 4 {
		t.Fatalf("items %v", e.items)
	}
}

func TestMaskerAndLineWriter(t *testing.T) {
	var m masker
	m.add("secret")
	m.add("secret-long")
	m.add("  ")
	if got := m.apply("a secret-long b secret"); got != "a *** b ***" {
		t.Fatalf("got %q", got)
	}
	var lines []string
	w := &lineWriter{emit: func(s string) { lines = append(lines, s) }}
	_, _ = w.Write([]byte("one\r\ntw"))
	_, _ = w.Write([]byte("o\nthree"))
	w.Flush()
	if !reflect.DeepEqual(lines, []string{"one", "two", "three"}) {
		t.Fatalf("lines %q", lines)
	}
}

func TestWriteScriptBashAndSh(t *testing.T) {
	if _, err := findBash(); err != nil {
		t.Skip("bash not found:", err)
	}
	dir := t.TempDir()
	sc, err := writeScript("bash", "echo hi", dir, "step-01")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(sc.Args[:4], " "); got != "--noprofile --norc -eo pipefail" {
		t.Fatalf("args %v", sc.Args)
	}
	if b, _ := os.ReadFile(sc.File); string(b) != "echo hi" {
		t.Fatalf("script %q", b)
	}
	sh, err := writeScript("sh", "echo hi", dir, "step-02")
	if err != nil || sh.Args[0] != "-e" {
		t.Fatalf("sh: %v %v", sh, err)
	}
	if _, err := writeScript("zsh", "x", dir, "s"); err == nil {
		t.Fatal("want error for unknown shell")
	}
}

func TestWriteScriptPwsh(t *testing.T) {
	if _, err := exec.LookPath("pwsh"); err != nil {
		t.Skip("pwsh not installed:", err)
	}
	dir := t.TempDir()
	sc, err := writeScript("pwsh", "Write-Host hi", dir, "step-01")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(sc.File)
	s := string(b)
	if !strings.HasPrefix(s, "$ErrorActionPreference = 'Stop'\n") || !strings.Contains(s, `exit $LASTEXITCODE`) {
		t.Fatalf("script %q", s)
	}
	if sc.Args[3] != "-Command" || !strings.HasPrefix(sc.Args[4], ". '") || filepath.Ext(sc.File) != ".ps1" {
		t.Fatalf("args %v", sc.Args)
	}
}

func TestLookPathEnv(t *testing.T) {
	dir := t.TempDir()
	name := "tool-x"
	p := filepath.Join(dir, name)
	if runtimeIsWindows() {
		p += ".exe"
	}
	if err := os.WriteFile(p, []byte("x"), 0o755); err != nil { //nolint:gosec // executable file the test needs to run
		t.Fatal(err)
	}
	got, err := lookPathEnv(name, []string{"PATH=" + dir})
	if err != nil || got != p {
		t.Fatalf("got %q %v", got, err)
	}
	if _, err := lookPathEnv(name, []string{"PATH=" + t.TempDir()}); err == nil {
		t.Fatal("want not found")
	}
}
