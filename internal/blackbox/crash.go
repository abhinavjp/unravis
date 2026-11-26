package blackbox

import (
	"fmt"
	"path/filepath"
	"time"

	"api-audit-explorer/internal/crashlog"
)

// CrashDirName is the folder inside the recordings folder that holds the
// recorder's crash files (RV-BB-10). It sits beside the recordings, so removing
// the recordings removes it too (RemoveRecordings). Its rights are NOT those of
// the recordings folder: a raw dump holds the panic text before it is cleaned, so
// the folder and every file in it are for SYSTEM and administrators only (see
// crashSecurity).
const CrashDirName = "crash"

// CrashDir is the crash folder of a recordings folder.
func CrashDir(folder string) string { return filepath.Join(folder, CrashDirName) }

// crashGrace is how young a raw dump must be to be left alone by the next start.
// The crash library waits 30 s by default (another copy may be writing); the
// recorder is the only copy of itself in its folder, and the service manager
// restarts it within seconds, so 5 s is enough and the crash is reported at that
// start, not the one after.
const crashGrace = 5 * time.Second

// crashSecurity makes the crash folder and its files private. On Windows the
// folder gets a protected permission list for SYSTEM and administrators with no
// entry for ordinary users (an elevated process; any other process gets itself
// and SYSTEM), and every dump and cleaned file gets the same, so a local user
// cannot read a panic text that may carry a secret. Elsewhere it is the 0700 /
// 0600 modes. If securing fails, capture is not started: no dump is better than
// a readable one. A test replaces it to see what is asked for.
type crashSecurity struct {
	dir  func(string) error
	file func(string) error
}

// osCrashSecurity is the real one (crash_secure_windows.go, crash_secure_other.go).
var osCrashSecurity = crashSecurity{dir: secureCrashDir, file: secureCrashFile}

// startCrashCapture turns on the local crash record for this process: an
// unhandled panic or fatal runtime error writes its goroutine dump into the
// crash folder, and the next start turns it into a cleaned crash file. It never
// stops the recorder: a recorder that cannot record its crash must still
// record, so trouble is only logged. Nothing is sent anywhere. The returned
// capture may be nil (every method is nil-safe).
func startCrashCapture(folder string, logf func(string, ...any)) *crashlog.Capture {
	return startCrashCaptureWith(folder, logf, osCrashSecurity)
}

func startCrashCaptureWith(folder string, logf func(string, ...any), sec crashSecurity) *crashlog.Capture {
	c, err := crashlog.Start(crashlog.Options{Dir: CrashDir(folder), Grace: crashGrace, SecureDir: sec.dir, SecureFile: sec.file})
	if err != nil {
		logf("crash record: %v", err)
	}
	return c
}

// eventWriter is the part of the store reportPreviousCrashes needs.
type eventWriter interface {
	EventDurable(kind, detail string) error
}

// reportPreviousCrashes tells the recording about crash files an earlier run
// left that were not reported yet: ONE event per start, with fixed keys only
// ("crashes=<n> at=<UTC time of the newest>"), so a crash loop cannot fill the
// event list. The files are marked as reported only after the event reached the
// manifest on disk; if that write failed they stay unmarked and the next start
// tries again (a stop between the two shows the crash twice, never not at all).
func reportPreviousCrashes(s eventWriter, folder string, logf func(string, ...any)) {
	files, err := crashlog.Unreported(CrashDir(folder))
	if err != nil {
		logf("crash record: %v", err)
		return
	}
	if len(files) == 0 {
		return
	}
	newest := files[len(files)-1].Time
	if err := s.EventDurable(EventCrashed, fmt.Sprintf("crashes=%d at=%s", len(files), newest.Format(time.RFC3339))); err != nil {
		logf("crash record: could not store the crash event, will try again at the next start: %v", err)
		return
	}
	for _, f := range files {
		if _, err := crashlog.MarkReported(f.Path); err != nil {
			logf("crash record: %v", err)
		}
	}
}
