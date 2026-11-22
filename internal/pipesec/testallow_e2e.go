//go:build e2e

package pipesec

import (
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
