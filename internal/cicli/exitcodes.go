package cicli

// Exit codes of unravis-ci.
//
// 0 to 3 and 5 (`hold check`) are in use. 4 is reserved for the stop control a later round adds: nothing
// returns it yet, and no other meaning may be given to it, so a script that checks for it keeps working
// when the command arrives.
const (
	exitOK          = 0
	exitFailed      = 1 // the workflow failed, or the thing asked for could not be done
	exitUsage       = 2 // wrong arguments, or an I/O error
	exitUnsupported = 3 // the workflow uses something this runner does not support
	// exitStopped is reserved: the work was not started because a stop state is on (a person stopped the
	// queue or a project on purpose).
	exitStopped = 4
	// exitHeld: the work was not started because a freeze (a hold) is active. `hold check` returns it.
	exitHeld = 5
)
