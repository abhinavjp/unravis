package applog

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type failWriter struct{ err error }

func (f failWriter) Write([]byte) (int, error) { return 0, f.err }

func TestTeeWritesToEveryWriter(t *testing.T) {
	var a, b bytes.Buffer
	p := []byte("hello")
	n, err := Tee(&a, &b).Write(p)
	if n != len(p) || err != nil {
		t.Fatalf("Write = %d, %v; want %d, nil", n, err, len(p))
	}
	if a.String() != "hello" || b.String() != "hello" {
		t.Errorf("buffers = %q, %q", a.String(), b.String())
	}
}

func TestTeeKeepsWritingWhenOneWriterFails(t *testing.T) {
	var buf bytes.Buffer
	p := []byte("still logged")
	n, err := Tee(failWriter{errors.New("boom")}, &buf).Write(p)
	if n != len(p) || err != nil {
		t.Fatalf("Write = %d, %v; want %d, nil", n, err, len(p))
	}
	if buf.String() != "still logged" {
		t.Errorf("buffer = %q", buf.String())
	}
}

func TestTeeReportsTheFirstErrorWhenEveryWriterFails(t *testing.T) {
	first, second := errors.New("first"), errors.New("second")
	n, err := Tee(failWriter{first}, failWriter{second}).Write([]byte("x"))
	if n != 0 || err != first {
		t.Fatalf("Write = %d, %v; want 0, %v", n, err, first)
	}
}

func TestTeeIgnoresANilWriter(t *testing.T) {
	var buf bytes.Buffer
	if n, err := Tee(nil, &buf).Write([]byte("abc")); n != 3 || err != nil {
		t.Fatalf("Write = %d, %v", n, err)
	}
	if buf.String() != "abc" {
		t.Errorf("buffer = %q", buf.String())
	}
	for name, w := range map[string]io.Writer{"none": Tee(), "nil": Tee(nil)} {
		if w != io.Discard {
			t.Errorf("Tee(%s) is not io.Discard", name)
		}
		if n, err := w.Write([]byte("abc")); n != 3 || err != nil {
			t.Errorf("Tee(%s).Write = %d, %v", name, n, err)
		}
	}
}

func TestTeeSurvivesATypedNilFile(t *testing.T) {
	var f *os.File
	var buf bytes.Buffer
	if n, err := Tee(f, &buf).Write([]byte("abc")); n != 3 || err != nil {
		t.Fatalf("Write = %d, %v", n, err)
	}
	if buf.String() != "abc" {
		t.Errorf("buffer = %q", buf.String())
	}
}

func TestOpenStillLogsWhenStderrIsBroken(t *testing.T) {
	broken, err := os.Create(filepath.Join(t.TempDir(), "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	broken.Close()
	old := os.Stderr
	os.Stderr = broken
	t.Cleanup(func() { os.Stderr = old })

	dir := filepath.Join(t.TempDir(), "logs")
	f, err := Open(dir, ParseLevel("info"), func(string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	f.Logger.Info("windowless event", "rows", 3)
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "windowless event") {
		t.Errorf("log file missed the line: %q", data)
	}
}
