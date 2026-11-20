package main

import (
	"fmt"
	"strings"

	"api-audit-explorer/internal/update"
	"api-audit-explorer/internal/version"
)

// runVerify is --verify <folder>: it checks a release carried on a stick, offline, with the same signature, channel and hash checks
// as an online update, and prints what it found. It installs nothing and writes nothing. Exit 0 means the folder holds a good release;
// 1 means do not use it.
func runVerify(o options, e env) int {
	cur, err := update.ParseVersion(strings.TrimPrefix(strings.TrimSpace(version.Version), "v"))
	if err != nil {
		fmt.Fprintln(e.stderr, "unravis-setup: this is a development build, which trusts no release")
		return 1
	}
	keys, err := update.TrustedKeys()
	if err != nil || len(keys) == 0 {
		fmt.Fprintln(e.stderr, "unravis-setup: this build has no trusted update keys, so it cannot check a release")
		return 1
	}
	r, err := update.VerifyFolder(o.verify, update.Trust{Keys: keys, Channel: update.ChannelOf(cur), Want: update.ChannelOf(cur)}, cur.String(), e.now())
	if err != nil {
		fmt.Fprintln(e.stderr, "unravis-setup: NOT VERIFIED:", err)
		return 1
	}
	fmt.Fprintf(e.stdout, "Signature good. Release %s (%s channel), signed and not expired.\n", r.Manifest.Version, r.Manifest.Channel)
	if r.Decision == update.DecisionInstall {
		fmt.Fprintf(e.stdout, "It is newer than this setup (%s).\n", cur.String())
	} else {
		fmt.Fprintf(e.stdout, "It is not an upgrade for this setup (%s): %s\n", cur.String(), r.Why)
	}
	for _, f := range r.Files {
		if f.Found {
			fmt.Fprintf(e.stdout, "  OK       %-9s %s  %d bytes  sha256 %s\n", f.Kind, f.Name, f.Size, f.SHA256)
		} else {
			fmt.Fprintf(e.stdout, "  not here %-9s %s\n", f.Kind, f.Name)
		}
	}
	fmt.Fprintln(e.stdout, "Every file listed as OK matches the signed release byte for byte.")
	return 0
}
