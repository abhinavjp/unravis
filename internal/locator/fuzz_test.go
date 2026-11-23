package locator

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// BL-450: any bytes in backend.json must give an error or a record that passes Validate; never a panic, never a file over the size cap.
func FuzzLocatorRead(f *testing.F) {
	good := Record{
		Version: Schema, PID: 4242, StartTime: time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC), Exe: `C:\Program Files\Unravis\unravis.exe`, AppVersion: "0.1.0-rc.59",
		UI: `\\.\pipe\unravis.ui.0123456789abcdef0123456789abcdef`, AG: `\\.\pipe\unravis.ag.0123456789abcdef0123456789abcdef`,
	}
	if b, err := json.Marshal(good); err == nil {
		f.Add(b)
	}
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"version":2,"version":3}`))
	f.Add([]byte(`{"version":1,"pid":-1,"ui":"\\\\.\\pipe\\evil"}`))
	f.Add([]byte(`{"version":2,"pid":1,"ui":"\\\\.\\pipe\\unravis.ui.0123456789abcdef0123456789abcde\n"}`))
	f.Add([]byte(``))
	f.Add([]byte("\x00\xff"))
	dir := f.TempDir()
	f.Fuzz(func(t *testing.T, data []byte) {
		path := filepath.Join(dir, "backend.json")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Skip()
		}
		r, err := Read(path)
		if err != nil {
			if errors.Is(err, ErrNoLocator) {
				t.Fatalf("the file exists, but Read says there is no locator: %v", err)
			}
			return
		}
		if len(data) > maxFileBytes {
			t.Fatalf("accepted a locator file of %d bytes, the cap is %d", len(data), maxFileBytes)
		}
		if err := r.Validate(); err != nil {
			t.Fatalf("Read accepted a record that fails Validate: %v", err)
		}
	})
}
