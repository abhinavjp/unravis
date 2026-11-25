package releasedash

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// ReleaseRunning says whether a release is running on this PC now, from lock-info.json alone: the file was written
// within the last day (and not in the future) and the process in it is alive. It is the cheap yes or no that
// other tools (the local CI agent) use to stay out of a release's way. It never opens release.lock or run.lock.
// A release whose launcher window was closed counts as not running here; ReadLocal has the finer picture.
func ReleaseRunning(dir string, now time.Time) bool {
	if dir == "" {
		return false
	}
	sp := filepath.Join(dir, "lock-info.json")
	fi, err := os.Stat(sp)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > 16<<10 {
		return false
	}
	b, err := readSmall(sp, 16<<10)
	if err != nil {
		return false
	}
	var li lockInfo
	if json.Unmarshal(b, &li) != nil {
		return false
	}
	started, err := time.Parse(time.RFC3339Nano, li.Started)
	if err != nil || started.After(now.Add(time.Minute)) || now.Sub(started) > maxLiveAge {
		return false
	}
	return processAlive(li.PID, started)
}

// ProcessAlive says whether pid is a running process that started no later than startedBy (plus a few seconds).
func ProcessAlive(pid int, startedBy time.Time) bool { return processAlive(pid, startedBy) }
