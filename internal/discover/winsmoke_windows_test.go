//go:build windows

package discover

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"api-audit-explorer/internal/importer/all"
)

// Real-OS smoke checks of the Windows discovery Env. They run only with
// UNRAVIS_WINSMOKE=1 (never in normal CI) and assert on counts and shapes, never
// on the names or values this machine happens to hold.

func winSmoke(t *testing.T) {
	t.Helper()
	if os.Getenv("UNRAVIS_WINSMOKE") != "1" {
		t.Skip("set UNRAVIS_WINSMOKE=1 to run the real-OS smoke checks")
	}
}

func TestWinSmokeServices(t *testing.T) {
	winSmoke(t)
	env, err := NewSystemEnv()
	if err != nil {
		t.Fatal(err)
	}
	list, err := env.Services.List()
	if err != nil {
		t.Fatalf("Services.List: %v", err)
	}
	if len(list) < 10 {
		t.Fatalf("only %d services", len(list))
	}
	var running, withImage, withDir int
	states := map[string]int{}
	seen := map[string]bool{}
	var probe string
	for _, s := range list {
		if s.Name == "" {
			t.Fatal("service with an empty name")
		}
		if seen[strings.ToLower(s.Name)] {
			t.Fatalf("a service is listed twice (paging bug)")
		}
		seen[strings.ToLower(s.Name)] = true
		states[s.State]++
		if s.State == "running" {
			running++
		}
		if s.Image != "" {
			withImage++
			if strings.ContainsAny(s.Image, `\/ "`) {
				t.Fatalf("image is not a bare base name: %d chars", len(s.Image))
			}
			if probe == "" && s.State == "running" {
				probe = s.Name
			}
		}
		if s.BinaryDir != "" {
			withDir++
			if _, err := ValidateAbsPath(s.BinaryDir); err != nil {
				t.Fatalf("BinaryDir fails the path shape check: %v", err)
			}
			if s.Image == "" {
				t.Fatal("BinaryDir without an image")
			}
		}
	}
	for st := range states {
		if st != "running" && st != "stopped" && st != "other" {
			t.Fatalf("unknown state %q", st)
		}
	}
	if running == 0 || withImage == 0 {
		t.Fatalf("running=%d withImage=%d", running, withImage)
	}
	t.Logf("services=%d running=%d withImage=%d withDir=%d states=%v", len(list), running, withImage, withDir, states)

	cl, ok := env.Services.(interface {
		CommandLine(string) (string, error)
	})
	if !ok {
		t.Fatal("the Windows services Env has no CommandLine")
	}
	// Command line of one running service: non-empty, agrees with the listing.
	if probe == "" {
		t.Skip("no running service with a readable image")
	}
	line, err := cl.CommandLine(probe)
	if err != nil {
		t.Fatalf("CommandLine: %v", err)
	}
	if strings.TrimSpace(line) == "" {
		t.Fatal("empty command line")
	}
	dir, img := SplitImagePath(line)
	for _, s := range list {
		if s.Name == probe && (s.Image != img || s.BinaryDir != dir) {
			t.Fatal("CommandLine disagrees with the listing")
		}
	}
	// Every service's command line can be asked for without a panic, and a
	// denied one maps to the contract error.
	denied := 0
	for _, s := range list {
		if _, err := cl.CommandLine(s.Name); err != nil {
			if IsAccessDenied(err) {
				denied++
				continue
			}
			if !IsNotFound(err) {
				t.Fatalf("CommandLine of a listed service: %v", err)
			}
		}
	}
	t.Logf("command lines denied: %d", denied)

	// A service that does not exist is not-found, never a bare system error.
	if _, err := cl.CommandLine("Unravis-No-Such-Service-0"); !IsNotFound(err) {
		t.Fatalf("missing service: %v (reason %s)", err, ReasonFromError(err))
	}
	// A name that cannot be a service name.
	if _, err := cl.CommandLine("bad\x00name"); err == nil {
		t.Fatal("NUL in a service name accepted")
	}
}

func TestWinSmokeProcsAndMachine(t *testing.T) {
	winSmoke(t)
	env, _ := NewSystemEnv()
	procs, err := env.Procs.List()
	if err != nil {
		t.Fatal(err)
	}
	var self bool
	for _, p := range procs {
		if p.PID == uint32(os.Getpid()) {
			self = true
			if p.Start.IsZero() || p.Image == "" {
				t.Fatalf("own process listed without start or image: %+v", p.Start)
			}
		}
		if strings.ContainsAny(p.Image, `\/`) {
			t.Fatal("process image is not a base name")
		}
	}
	if len(procs) < 5 || !self {
		t.Fatalf("%d processes, self listed=%v", len(procs), self)
	}
	m := env.Machine
	if len(m.Names()) == 0 || m.LogicalCores() < 1 || m.TZKey() == "" {
		t.Fatalf("machine: names=%d cores=%d tz-set=%v", len(m.Names()), m.LogicalCores(), m.TZKey() != "")
	}
	t.Logf("processes=%d names=%d cores=%d elevated=%v", len(procs), len(m.Names()), m.LogicalCores(), m.Elevated())
}

func TestWinSmokeChannels(t *testing.T) {
	winSmoke(t)
	env, _ := NewSystemEnv()
	names, err := env.Channels.List()
	if err != nil {
		t.Fatalf("Channels.List: %v", err)
	}
	have := map[string]bool{}
	for _, n := range names {
		have[strings.ToLower(n)] = true
	}
	for _, want := range []string{"application", "system", "microsoft-windows-taskscheduler/operational"} {
		if !have[want] {
			t.Errorf("%d channels listed, one expected channel is missing", len(names))
		}
	}
	for _, name := range []string{"Application", "System", "Microsoft-Windows-TaskScheduler/Operational"} {
		if err := env.Channels.CanRead(name); err != nil && !IsAccessDenied(err) {
			t.Errorf("CanRead of a standard channel: %v", err)
		}
	}
	for _, name := range []string{"Security", "security", "Security/Anything", "", "Bad\x00Name", strings.Repeat("a", 300)} {
		if err := env.Channels.CanRead(name); err == nil {
			t.Errorf("CanRead accepted a bad name of length %d", len(name))
		}
	}
	if err := env.Channels.CanRead("Unravis-No-Such-Channel/Operational"); !IsNotFound(err) {
		t.Errorf("missing channel: %v", err)
	}
	// None of the errors carries a channel name.
	if err := env.Channels.CanRead("Unravis-No-Such-Channel/Operational"); err != nil && strings.Contains(err.Error(), "Unravis") {
		t.Error("channel name leaked into an error")
	}
	// The Security channel is not offered as readable by the detector either.
	for _, n := range names {
		if strings.EqualFold(n, "Security") {
			t.Logf("Security is listed (refused on CanRead)")
		}
	}
	t.Logf("channels=%d elevated=%v", len(names), env.Machine.Elevated())
}

// IIS may not be installed. Either way the detector must return cleanly: with
// IIS, findings or gaps; without it, a not-found gap and no finding.
func TestWinSmokeIISDetectorDefaultRoots(t *testing.T) {
	winSmoke(t)
	env, _ := NewSystemEnv()
	env.Importers = all.Registry()
	finds, gaps := NewIISDetector(DefaultIISRoots()).Detect(context.Background(), env)
	t.Logf("iis: findings=%d gaps=%d", len(finds), len(gaps))
	if _, err := os.Stat(filepath.Join(os.Getenv("SystemRoot"), `System32\inetsrv\config\applicationHost.config`)); err == nil {
		for _, g := range gaps {
			if g.Reason == ReasonUnreadable {
				t.Errorf("IIS installed but a gap says unreadable: %+v", g.Kind)
			}
		}
		return
	}
	if os.Getenv("SystemDrive") != "C:" {
		t.Skip("system drive is not C:, DefaultIISRoots does not apply")
	}
	if _, err := os.Stat(`C:\inetpub\logs\LogFiles`); err == nil {
		t.Skip("default IIS log folder exists without IIS config")
	}
	if len(finds) != 0 {
		t.Fatalf("%d findings without IIS", len(finds))
	}
	if len(gaps) == 0 {
		t.Fatal("no gap for a missing IIS")
	}
	for _, g := range gaps {
		if g.Reason != ReasonNotFound {
			t.Errorf("gap reason %q, want %q", g.Reason, ReasonNotFound)
		}
	}
	// The HTTP.sys detector must be clean too.
	_, hg := NewHTTPErrDetector(DefaultIISRoots()).Detect(context.Background(), env)
	for _, g := range hg {
		if g.Reason == ReasonUnreadable {
			t.Errorf("HTTP.sys detector gap unreadable: %+v", g.Kind)
		}
	}
	// And a full Run with only IIS finishes without a panic gap.
	scan := Run(context.Background(), env, []Detector{NewIISDetector(DefaultIISRoots())})
	if len(scan.Findings) != 0 {
		t.Fatalf("scan findings: %d", len(scan.Findings))
	}
}

var _ = fs.ErrNotExist
