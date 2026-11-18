package blackbox

import (
	"context"
	"os"
	"strings"
	"sync"
	"time"
)

// ProgramVersioner reports the version of the unravis-blackbox.exe that ships
// with Unravis, so the app can say whether the installed recorder is older. The
// program is asked once per file (cached by size and modified time) by running
// "unravis-blackbox version" as the person (no administrator rights). When a
// Guard is set the program is checked first, exactly as before an elevated call,
// and a program that fails the check is never run: the answer is "".
type ProgramVersioner struct {
	Elev    Elevator
	Program string
	Guard   func(program string) (release func(), err error)

	mu    sync.Mutex
	size  int64
	mtime time.Time
	value string
	have  bool
}

// Version is the version the shipped program reports, or "" when it cannot be
// told (a missing or untrusted program, or one that does not answer).
func (p *ProgramVersioner) Version() string {
	fi, err := os.Stat(p.Program)
	if err != nil {
		return ""
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.have && p.size == fi.Size() && p.mtime.Equal(fi.ModTime()) {
		return p.value
	}
	v := p.ask()
	p.size, p.mtime, p.value, p.have = fi.Size(), fi.ModTime(), v, true
	return v
}

func (p *ProgramVersioner) ask() string {
	if p.Guard != nil {
		release, err := p.Guard(p.Program)
		if err != nil {
			return ""
		}
		if release != nil {
			defer release()
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := p.Elev.Run(ctx, p.Program, []string{"version"}, false)
	if err != nil {
		return ""
	}
	return ParseVersionLine(string(out))
}

// ParseVersionLine reads the version out of "unravis-blackbox <version>".
func ParseVersionLine(s string) string {
	line := strings.TrimSpace(strings.SplitN(strings.TrimSpace(s), "\n", 2)[0])
	const prefix = "unravis-blackbox "
	if !strings.HasPrefix(line, prefix) {
		return ""
	}
	v := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	if v == "" || len(v) > 64 || strings.ContainsAny(v, " \t\r") {
		return ""
	}
	return v
}

// UpdateAvailable says whether the installed recorder is older than the one that
// ships with Unravis. recorder is the version the installed recorder reports
// and program the shipped one (empty: no claim). A recorder that reports no
// version is an older build or one that is stopped, so an update is offered. A
// local build reports "dev", which names no release, so two versions cannot be
// compared and an update is offered too: the helper compares the programs
// themselves and answers "already current" when they are the same.
func UpdateAvailable(recorder, program string) bool {
	if program == "" {
		return false
	}
	if recorder == "" || recorder == "dev" || program == "dev" {
		return true
	}
	return recorder != program
}
