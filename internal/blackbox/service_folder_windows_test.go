//go:build windows

package blackbox

import (
	"os/exec"
)

// makeJunction makes link point at target without needing administrator rights.
func makeJunction(link, target string) error {
	return exec.Command("cmd", "/c", "mklink", "/J", link, target).Run()
}
