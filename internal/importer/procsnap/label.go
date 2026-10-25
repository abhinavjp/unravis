package procsnap

import "api-audit-explorer/internal/importer/wincsv"

// IsServerImage reports whether name (an image name with ".exe" removed, lower
// case) is one of the server images that are stored by name.
func IsServerImage(name string) bool { return serverImages[name] }

// ProcessLabel is the stored name of a process, shared by every source that
// lists processes. name is an image name with or without ".exe". A server image
// (w3wp, dotnet, node, redis-server, memurai, mongod, sqlservr) keeps its name.
// Any other name that has the form of an image name becomes
// pseudonymize("instance", name), clipped to the attribute size, so the label
// is "instance-<hash>". A name of another form (a path, spaces, a command
// line), or one the pseudonymizer maps to an empty string, becomes "other".
func ProcessLabel(name string, pseudonymize func(kind, value string) string) string {
	n, ok := imageName(name)
	if !ok {
		return "other"
	}
	if serverImages[n] {
		return n
	}
	if pseudonymize != nil {
		if p := pseudonymize("instance", n); p != "" {
			return wincsv.ClipAttr(p)
		}
	}
	return "other"
}
