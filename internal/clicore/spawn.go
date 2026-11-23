package clicore

import "context"

// Child is an owner process that the command line started (`app start --headless`).
type Child struct {
	PID  int
	Exit <-chan int // gets the exit code once, when the process ends before the command line is done with it
}

// Starter starts the app program as a headless owner: detached, with no window and no browser (spec 05 D20). A test passes its own.
type Starter func(ctx context.Context) (Child, *Error)
