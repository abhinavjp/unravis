package update

// reopenMarker is text only a setup program that knows --reopen contains (the flag's help line in cmd/unravis-setup).
// Never remove or reword it without changing the help line with it: the marker check tests for this flag, and an older setup program
// that does not know --reopen refuses it. A test in cmd/unravis-setup keeps the help line and this text equal.
const reopenMarker = "the person pressed Apply now"

// silentReopenArgs is the argument list for an update the person started with Apply now: the plain silent one, plus --reopen only
// when the setup file carries the flag's help line. Otherwise it is exactly what LaunchSilent passes.
func silentReopenArgs(path string) []string {
	args := []string{"--silent", "--close-app"}
	if FileHasAll(path, reopenMarker) {
		args = append(args, "--reopen")
	}
	return args
}
