// Package instance names the kernel objects that let one copy of Unravis per
// user notice another: one mutex, one "open" event and one "quit" event.
//
// All names live in the per-session Local\ namespace and carry the current
// user's SID, so different accounts' copies stay apart. A SID is public, so
// the name alone does not stop another account in the same sign-in session
// from creating the objects first. Squatting is detected, not prevented: the
// guard and the signal functions check OwnedByCurrentUser and refuse an
// object whose owner is somebody else.
package instance

import (
	"errors"
	"fmt"
	"regexp"
)

// DefaultApp is the application name of the installed app.
const DefaultApp = "UnravisApp"

// DemoApp is the application name of a demo run, so a demo never collides
// with the installed app.
const DemoApp = "UnravisDemoApp"

// Kind is a signal one copy can send to the running copy. The zero value is
// invalid.
type Kind int

const (
	// Open asks the running copy to open a browser tab.
	Open Kind = iota + 1
	// Quit asks the running copy to shut down.
	Quit
)

// String returns "open", "quit", or "unknown" for any other value.
func (k Kind) String() string {
	switch k {
	case Open:
		return "open"
	case Quit:
		return "quit"
	}
	return "unknown"
}

var (
	// ErrNoInstance means no copy is running.
	ErrNoInstance = errors.New("instance: no running instance")
	// ErrAccessDenied means the object exists but this process may not use it.
	ErrAccessDenied = errors.New("instance: access denied")
	// ErrEventsTaken means the signal events already exist.
	ErrEventsTaken = errors.New("instance: events already exist")
	// ErrNameHeldByOther means the named object exists but its owner is not
	// the current user.
	ErrNameHeldByOther = errors.New("instance: another process holds Unravis's name")
)

// Names are the three kernel object names of one app for one user.
type Names struct {
	Mutex, Open, Quit string
}

const maxSIDLen = 184

var (
	appPattern = regexp.MustCompile(`^[A-Za-z0-9]{1,32}$`)
	sidPattern = regexp.MustCompile(`^S-1-[0-9]+(-[0-9]+)+$`)
)

// NewNames builds the names for app and userSID. app must be 1 to 32 ASCII
// letters or digits; userSID must be a SID string of at most 184 characters.
func NewNames(app, userSID string) (Names, error) {
	if !appPattern.MatchString(app) {
		return Names{}, errors.New("instance: app name must be 1 to 32 ASCII letters or digits")
	}
	if len(userSID) > maxSIDLen || !sidPattern.MatchString(userSID) {
		return Names{}, fmt.Errorf("instance: user SID is not a valid SID string")
	}
	base := `Local\` + app + "." + userSID
	return Names{Mutex: base, Open: base + ".Open", Quit: base + ".Quit"}, nil
}
