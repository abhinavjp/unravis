package main

// Exit codes of unravis-ci.
//
// 0 to 3 are in use. 4 and 5 are reserved for the stop and freeze controls that later rounds add: nothing
// returns them yet, and no other meaning may be given to them, so a script that checks for them keeps working
// when the commands arrive.
const (
	exitOK          = 0
	exitFailed      = 1 // the workflow failed, or the thing asked for could not be done
	exitUsage       = 2 // wrong arguments, or an I/O error
	exitUnsupported = 3 // the workflow uses something this runner does not support
	// exitStopped is reserved: the work was not started because a stop state is on (a person stopped the
	// queue or a project on purpose).
	exitStopped = 4
	// exitHeld is reserved: the work was not started because a freeze (a hold) is active.
	exitHeld = 5
)
