package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"api-audit-explorer/internal/server"
	"api-audit-explorer/internal/update"
)

// checkUpdateTimeout bounds the whole --check-update run (the fetch has its own shorter limits).
const checkUpdateTimeout = 60 * time.Second

// runCheckUpdate is --check-update: one check of the signed feed with the keys built into this program, the answer printed as one
// line of JSON, then exit. It is for scripts and for the release verify test (docs/auto-updater/OTA-RESEARCH-AND-PLAN.md section 7).
// It uses a throw-away state folder, so it never changes the anti-rollback state or the settings of a real install, and it downloads
// nothing. Exit codes: 0 the answer is "latest" or "available"; 2 anything else (dev build, no network, a feed that did not verify).
func runCheckUpdate(out, errOut io.Writer, timeout time.Duration) int {
	keys, err := update.TrustedKeys()
	if err != nil {
		fmt.Fprintf(errOut, "Cannot check for updates: %v\n", err)
		return 2
	}
	dir, err := os.MkdirTemp("", "unravis-check-update-")
	if err != nil {
		fmt.Fprintf(errOut, "Cannot check for updates: %v\n", err)
		return 2
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	st := server.NewUpdater(dir, keys).CheckNow(ctx)
	b, err := json.Marshal(st)
	if err != nil {
		fmt.Fprintf(errOut, "Cannot check for updates: %v\n", err)
		return 2
	}
	fmt.Fprintln(out, string(b))
	if st.Status == "latest" || st.Status == "available" {
		return 0
	}
	return 2
}
