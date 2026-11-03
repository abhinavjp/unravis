package autostart

import "errors"

// ErrNoStartupDir means the platform has no per-user Startup folder.
var ErrNoStartupDir = errors.New("autostart: no Startup folder on this platform")
