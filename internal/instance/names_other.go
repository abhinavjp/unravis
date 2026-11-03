//go:build !windows

package instance

// CurrentNames returns the names for app. Other systems have no token SID, so
// a fixed placeholder SID is used.
func CurrentNames(app string) (Names, error) {
	return NewNames(app, "S-1-0-0")
}
