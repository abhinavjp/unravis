package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"api-audit-explorer/internal/dotnetdiag"
)

const diagProbeUsage = `Usage: unravis-blackbox diag-probe --pid <number> [--seconds 5-60] [--start-ticks <number>] [--json]

Test, read-only. Asks ONE running .NET process (5 or newer) for runtime counters
for 20 seconds (or --seconds), then stops the session. It writes no file and
prints nothing that came from the process. It sends only two fixed messages:
start and stop. It does not change the Blackbox settings or service.

For an IIS application pool process (w3wp), run it as SYSTEM. An administrator who
is not SYSTEM may be refused (reported as denied).
`

// realDiagProbe runs the probe over this platform's real transport.
func realDiagProbe(ctx context.Context, t dotnetdiag.Target, o dotnetdiag.Options) (dotnetdiag.Result, error) {
	return dotnetdiag.Probe(ctx, dotnetdiag.CLIDialer(), t, o)
}

// runDiagProbe is the diag-probe command. It is dispatched from run() before
// any flag, config or manager work, and the service entry points never call it.
func runDiagProbe(args []string, stdout, stderr io.Writer, d deps) int {
	usage := func(msg string) int {
		if msg != "" {
			_, _ = fmt.Fprintln(stderr, "diag-probe: "+msg)
		}
		_, _ = fmt.Fprint(stderr, diagProbeUsage)
		return exitUsage
	}
	// Own flag set: --pid, --seconds, --start-ticks, --json. No flag takes a
	// provider, keyword, command or payload.
	fs := flag.NewFlagSet("diag-probe", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	pidArg := fs.String("pid", "", "process id of the .NET process")
	seconds := fs.Int("seconds", 20, "how long to read counters, 5 to 60")
	startTicks := fs.Int64("start-ticks", 0, "expected creation time of the process, as the probe prints it")
	asJSON := fs.Bool("json", false, "answer as one JSON line")
	if err := fs.Parse(args); err != nil {
		return usage("unknown or malformed option")
	}
	if fs.NArg() > 0 {
		return usage("unexpected argument")
	}
	if *pidArg == "" {
		return usage("--pid is required")
	}
	pid, err := strconv.ParseUint(*pidArg, 10, 32)
	if err != nil || pid == 0 {
		return usage("--pid must be a process id from 1 to 4294967295")
	}
	if pid == uint64(os.Getpid()) { //nolint:gosec // a process id is never negative
		return usage("--pid must not be this program")
	}
	if *seconds < 5 || *seconds > 60 {
		return usage("--seconds must be from 5 to 60")
	}
	if *startTicks < 0 {
		return usage("--start-ticks must not be negative")
	}

	probe := d.diagProbe
	if probe == nil {
		probe = realDiagProbe
	}
	// Ctrl+C cancels the context; Probe still runs its stop path (fresh context).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	o := dotnetdiag.DefaultOptions()
	o.Duration = time.Duration(*seconds) * time.Second
	res, perr := probe(ctx, dotnetdiag.Target{PID: uint32(pid), StartTicks: *startTicks}, o)
	if errors.Is(perr, dotnetdiag.ErrBadTarget) {
		return usage("--pid is not a process this probe accepts")
	}
	if res.RunAs == "" {
		res.RunAs = dotnetdiag.RunAs()
	}
	reason := res.Reason
	if reason == "" && perr != nil {
		reason = dotnetdiag.ReasonOther
		var de *dotnetdiag.Error
		if errors.As(perr, &de) {
			reason, res.Code = de.Reason, de.Code
		}
		res.Reason = reason
	}
	if *asJSON {
		b, err := json.Marshal(res)
		if err != nil {
			return exitFail
		}
		_, _ = fmt.Fprintln(stdout, string(b))
	} else {
		printDiagProbe(stdout, res)
	}
	return dotnetdiag.ExitCode(reason)
}

func yn(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// printDiagProbe writes one labelled line per Result field and one fixed
// sentence. Only numbers, yes/no and fixed words: no OS text, no path, no bytes.
func printDiagProbe(w io.Writer, r dotnetdiag.Result) {
	line := func(label string, v any) { _, _ = fmt.Fprintf(w, "%-22s %v\n", label+":", v) }
	line("Operating system", r.OS)
	line("Transport", r.Transport)
	line("Run as", r.RunAs)
	line("Process id", r.PID)
	line("Port opened", yn(r.PipeOpened))
	line("Server process id", r.ServerPID)
	line("Server start ticks", r.ServerStart)
	if r.StartMatch == nil {
		line("Start ticks match", "not asked")
	} else {
		line("Start ticks match", yn(*r.StartMatch))
	}
	line("Session id", r.SessionID)
	line("Stream header ok", yn(r.PreambleOK))
	line("Trace version", r.TraceVersion)
	line("Trace process id", r.TracePID)
	line("Pointer size", r.PointerSize)
	line("Trace pid matches", yn(r.TracePIDMatch))
	line("Bytes before stop", r.BytesBefore)
	line("Bytes after stop", r.BytesAfter)
	line("Ended early", yn(r.EndedEarly))
	line("Stop acknowledged", yn(r.StopAck))
	line("Stop same session", yn(r.StopSession))
	line("Elapsed ms", r.ElapsedMs)
	reason := string(r.Reason)
	if reason == "" {
		reason = "ok"
	}
	line("Result", reason)
	line("Code", r.Code)
	_, _ = fmt.Fprintln(w, dotnetdiag.Sentence(r.Reason))
}
