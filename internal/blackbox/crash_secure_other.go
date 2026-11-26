//go:build !windows

package blackbox

import "os"

// secureCrashDir limits the crash folder to its owner.
func secureCrashDir(dir string) error { return os.Chmod(dir, 0o700) } //nolint:gosec // 0700 is the point: a folder only its owner may enter

// secureCrashFile limits one crash file to its owner.
func secureCrashFile(path string) error { return os.Chmod(path, 0o600) }
