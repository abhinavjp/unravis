package discover

import "io/fs"

func errPerm() error { return fs.ErrPermission }
