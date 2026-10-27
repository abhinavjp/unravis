// Package version holds the build's version. The values are stamped at build
// time (build.ps1 passes -ldflags -X for each); an unstamped build says "dev".
package version

import (
	"strings"
	"unicode"
)

// Version, Commit and Date are set with -ldflags "-X api-audit-explorer/internal/version.Version=...".
var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

// Short names the build in one phrase: "Unravis v1.2.3", "Unravis ci-abc1234" or "Unravis dev".
// A numeric version gets a leading v; anything else (a tag that already has one, a commit hash, "dev") is shown as written.
func Short() string {
	v := strings.TrimSpace(Version)
	if v == "" {
		v = "dev"
	}
	if r := []rune(v)[0]; unicode.IsDigit(r) {
		v = "v" + v
	}
	return "Unravis " + v
}

// String is the full line printed by --version: "Unravis v1.2.3 (commit abc1234, built 2026-10-27)".
// Parts that are not stamped are left out, and no brackets are printed when there is nothing to put in them.
func String() string {
	var parts []string
	if commit := strings.TrimSpace(Commit); commit != "" {
		if len(commit) > 7 {
			commit = commit[:7]
		}
		parts = append(parts, "commit "+commit)
	}
	if date := strings.TrimSpace(Date); date != "" {
		parts = append(parts, "built "+date)
	}
	if len(parts) == 0 {
		return Short()
	}
	return Short() + " (" + strings.Join(parts, ", ") + ")"
}
