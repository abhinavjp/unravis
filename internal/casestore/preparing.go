package casestore

// Preparer is implemented by a case that builds helper indexes in the background
// after it is opened (an old case, written before the indexes existed). While it
// builds, the first charts and lists of a large case are slow, so the case page
// says so ("Preparing this case so its charts open quickly") instead of looking stuck.
// A case that does not implement it, or that has nothing to build, is simply not
// preparing.
type Preparer interface {
	// Preparing is true from the moment the case is opened until the background
	// build ends, whether it succeeds or not. It is cheap: it reads two flags.
	Preparing() bool
}
