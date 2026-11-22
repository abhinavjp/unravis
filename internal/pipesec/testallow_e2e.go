//go:build e2e

package pipesec

import (
	"os"
	"strings"
	"sync"
)

// This file exists only under the `e2e` build tag (PLAN section 1 renames spec 02's `citest`). The release scan fails the build if the
// symbol TestAllow appears in a release binary.

var (
	allowMu sync.Mutex
	allowed []string
)

// TestAllow adds one full image path that every peer check accepts.
func TestAllow(path string) {
	allowMu.Lock()
	defer allowMu.Unlock()
	allowed = append(allowed, winClean(path))
}

func extraAllowed(image string) bool {
	allowMu.Lock()
	defer allowMu.Unlock()
	for _, a := range allowed {
		if strings.EqualFold(a, winClean(image)) {
			return true
		}
	}
	return false
}

// A CI run that cannot call TestAllow (the shell's Rust test is a separate process) names the image in the environment.
func init() {
	if p := os.Getenv("UNRAVIS_E2E_ALLOW_IMAGE"); p != "" {
		TestAllow(p)
	}
}
