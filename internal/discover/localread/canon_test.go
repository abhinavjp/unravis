package localread

import (
	"errors"
	"strings"
	"testing"
)

func TestCheckShape(t *testing.T) {
	ok := []string{
		`C:\inetpub\logs\LogFiles\W3SVC1\u_ex010911.log`,
		`d:\logs\app-2001-09-11.txt`,
		`C:\Program Files\Redis\redis.log`,
		`C:\a\b.c.d`,
		`C:\caf\u00e9\x.log`,
		"C:\\r\u00e9sum\u00e9\\log.txt",
	}
	for _, p := range ok {
		if err := CheckShape(p); err != nil {
			t.Errorf("%q refused: %v", p, err)
		}
	}
	bad := map[string]string{
		"empty":            ``,
		"drive only":       `C:\`,
		"relative":         `logs\a.log`,
		"no drive sep":     `C:logs\a.log`,
		"dot dot":          `C:\logs\..\Windows\win.ini`,
		"dot dot leaf":     `C:\logs\..`,
		"dot":              `C:\logs\.\a.log`,
		"unc":              `\\web-a.example.test\share\a.log`,
		"long unc":         `\\?\UNC\web-a\share\a.log`,
		"long prefix":      `\\?\C:\logs\a.log`,
		"device":           `\\.\C:\logs\a.log`,
		"device name":      `\\.\PhysicalDrive0`,
		"ads":              `C:\logs\a.log:hidden`,
		"ads default":      `C:\logs\a.log::$DATA`,
		"drive colon":      `C::\logs\a.log`,
		"forward slash":    `C:/logs/a.log`,
		"double backslash": `C:\logs\\a.log`,
		"trailing slash":   `C:\logs\`,
		"trailing dot":     `C:\logs\a.log.`,
		"trailing space":   `C:\logs\a.log `,
		"wildcard":         `C:\logs\*.log`,
		"question":         `C:\logs\a?.log`,
		"pipe":             `C:\logs\a|b.log`,
		"control char":     "C:\\logs\\a\x01.log",
		"nul":              "C:\\logs\\a\x00.log",
		"newline":          "C:\\logs\\a\n.log",
		"too long":         `C:\` + strings.Repeat(`a\`, 200) + `x`,
	}
	for name, p := range bad {
		err := CheckShape(p)
		if err == nil {
			t.Errorf("%s: %q accepted", name, p)
		} else if !errors.Is(err, ErrUnsafePath) {
			t.Errorf("%s: error %v does not wrap ErrUnsafePath", name, err)
		}
	}
}

func TestErrorsNeverCarryThePath(t *testing.T) {
	p := `C:\Users\carol.example\secret-project\..\x.log`
	err := CheckShape(p)
	if err == nil {
		t.Fatal("accepted")
	}
	if strings.Contains(err.Error(), "carol") || strings.Contains(err.Error(), "secret-project") {
		t.Fatalf("error leaks the path: %v", err)
	}
	op := newMemOpener()
	_, _, err = OpenWith(op, testPath, FileID{VolumeSerial: 1, FileIndex: 1})
	if err != nil && strings.Contains(err.Error(), "inetpub") {
		t.Fatalf("error leaks the path: %v", err)
	}
}

func TestBaseName(t *testing.T) {
	if got := baseName(`C:\a\b\u_ex010911.log`); got != "u_ex010911.log" {
		t.Fatal(got)
	}
}
