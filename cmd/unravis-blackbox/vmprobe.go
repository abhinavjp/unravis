package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"time"

	"api-audit-explorer/internal/blackbox"
)

// vm-probe (BL-113): a read-only capture of which VMware performance counters
// this host has and what they read. It needs no administrator rights, changes
// nothing and reads a fixed list of counter names (internal/blackbox/vmprobe.go).
// The owner runs it once on a VMware guest and sends the output; that capture
// decides what the vm group may grow into.

// realVMProbe opens each fixed counter on its own query, so a missing one keeps
// its own PDH status code.
func realVMProbe(n int) blackbox.VMProbeResult {
	open := func(path string) (blackbox.CounterSource, error) {
		s, err := blackbox.NewPDHSource([]string{path})
		if err != nil {
			return nil, err // never a typed-nil source
		}
		return s, nil
	}
	return blackbox.RunVMProbe(open, n, time.Sleep)
}

// runVMProbe is the whole command: flags, one probe, one print. probe is nil in
// production (the real PDH probe) and a fake in tests.
func runVMProbe(args []string, stdout, stderr io.Writer, probe func(n int) blackbox.VMProbeResult) int {
	fs := flag.NewFlagSet("vm-probe", flag.ContinueOnError)
	fs.SetOutput(stderr)
	samples := fs.Int("samples", blackbox.VMProbeDefaultSamples, "readings per counter, 1 to 10")
	asJSON := fs.Bool("json", false, "answer as one JSON line")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "unexpected argument %q\n", fs.Arg(0))
		return exitUsage
	}
	if *samples < 1 || *samples > blackbox.VMProbeMaxSamples {
		fmt.Fprintf(stderr, "--samples must be from 1 to %d\n", blackbox.VMProbeMaxSamples)
		return exitUsage
	}
	if probe == nil {
		probe = realVMProbe
	}
	res := probe(*samples)
	if *asJSON {
		_ = json.NewEncoder(stdout).Encode(res)
	} else {
		fmt.Fprint(stdout, blackbox.FormatVMProbeText(res))
	}
	if res.PDHUnavailable {
		return exitFail // it could not run at all
	}
	return exitOK
}
