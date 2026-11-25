package releasedash

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReleaseRunning(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name    string
		started time.Time
		alive   bool
		want    bool
	}{
		{"live release", now.Add(-5 * time.Minute), true, true},
		{"process gone", now.Add(-5 * time.Minute), false, false},
		{"older than a day", now.Add(-30 * time.Hour), true, false},
		{"in the future", now.Add(3 * time.Hour), true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			withAlive(t, c.alive)
			dir := liveDir(t, c.started, "")
			if got := ReleaseRunning(dir, now); got != c.want {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}
	withAlive(t, true)
	if ReleaseRunning("", now) || ReleaseRunning(t.TempDir(), now) {
		t.Fatal("no lock-info.json must mean no release")
	}
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "lock-info.json"), []byte("{not json"), 0o600)
	if ReleaseRunning(dir, now) {
		t.Fatal("a broken file must mean no release")
	}
}
