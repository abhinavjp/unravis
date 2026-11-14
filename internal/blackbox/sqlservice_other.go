//go:build !windows

package blackbox

// SecureSQLDir is not available off Windows: the SQL service only exists there.
func (OSEnv) SecureSQLDir(string, string) error { return errNoService }

// secureSQLDir is not available off Windows.
func secureSQLDir(string, string) error { return errNoService }
