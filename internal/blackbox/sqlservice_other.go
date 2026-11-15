//go:build !windows

package blackbox

// SecureSQLDir is not available off Windows: the SQL service only exists there.
func (OSEnv) SecureSQLDir(string, string) error { return errNoService }

// secureSQLDir is not available off Windows.
func secureSQLDir(string, string) error { return errNoService }

// GrantSQLLaunchAccess is not available off Windows: the SQL service only
// exists there.
func (OSEnv) GrantSQLLaunchAccess(string, string, string) error { return errNoService }

// grantSQLLaunchAccess is not available off Windows.
func grantSQLLaunchAccess(string, string, string) error { return errNoService }

// VerifyAdminOnlyFile is not available off Windows: there are no Windows ACLs
// to prove, so the proof fails closed (SEC-1). The pure rule (adminOnlyDACL)
// still lives in sqlservice.go and is tested on every platform.
func (OSEnv) VerifyAdminOnlyFile(string) error { return errNoService }

// verifyAdminOnlyFile is not available off Windows.
func verifyAdminOnlyFile(string) error { return errNoService }

// LockSQLInstallMarker is not available off Windows: there are no Windows ACLs
// to apply, so the lock fails closed (SEC-3). The record write/read helpers
// stay pure Go in sqlservice.go and are tested on every platform.
func (OSEnv) LockSQLInstallMarker(string) error { return errNoService }

// lockSQLInstallMarker is not available off Windows.
func lockSQLInstallMarker(string) error { return errNoService }
