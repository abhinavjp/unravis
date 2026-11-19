//go:build !windows

package blackbox

import "os"

func makeJunction(link, target string) error { return os.Symlink(target, link) }
