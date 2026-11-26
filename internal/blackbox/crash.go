package blackbox

import (
	"fmt"
	"path/filepath"
	"time"

	"api-audit-explorer/internal/crashlog"
)

// CrashDirName is the folder inside the recordings folder that holds the
// recorder's crash files (RV-BB-10). It sits beside the recordings, so it is
// kept and sent together with them, and the service account can always write
// there (it writes the recordings).
const CrashDirName = "crash"

// CrashDir is the crash folder of a recordings folder.
func CrashDir(folder string) string { return filepath.Join(folder, CrashDirName) }

// crashGrace is how young a raw dump must be to be left alone by the next start.
// The crash library waits 30 s by default (another copy may be writing); the
// recorder is the only copy of itself in its folder, and the service manager
// restarts it within seconds, so 5 s is enough and the crash is reported at that
// start, not the one after.
const crashGrace = 5 * time.Second

// startCrashCapture turns on the local crash record for this process: an
// unhandled panic or fatal runtime error writes its goroutine dump into the
// crash folder, and the next start turns it into a cleaned crash file. It never
// stops the recorder: a recorder that cannot record its crash must still
// record, so trouble is only logged. Nothing is sent anywhere. The returned
// capture may be nil (every method is nil-safe).
func startCrashCapture(folder string, logf func(string, ...any)) *crashlog.Capture {
	c, err := crashlog.Start(crashlog.Options{Dir: CrashDir(folder), Grace: crashGrace})
	if err != nil {
		logf("crash record: %v", err)
	}
	return c
}

// reportPreviousCrashes tells the recording about crash files an earlier run
// left that were not reported yet: ONE event per start, with fixed keys only
// ("crashes=<n> at=<UTC time of the newest>"), so a crash loop cannot fill the
// event list. The files are then marked, so each is reported once. The event
// comes first: a stop between the two shows the crash twice, never not at all.
func reportPreviousCrashes(s *Store, folder string, logf func(string, ...any)) {
	files, err := crashlog.Unreported(CrashDir(folder))
	if err != nil {
		logf("crash record: %v", err)
		return
	}
	if len(files) == 0 {
		return
	}
	newest := files[len(files)-1].Time
	s.Event(EventCrashed, fmt.Sprintf("crashes=%d at=%s", len(files), newest.Format(time.RFC3339)))
	for _, f := range files {
		if _, err := crashlog.MarkReported(f.Path); err != nil {
			logf("crash record: %v", err)
		}
	}
}
