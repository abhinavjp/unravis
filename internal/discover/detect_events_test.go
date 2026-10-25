package discover_test

import (
	"context"
	"io/fs"
	"strings"
	"sync"
	"testing"

	"api-audit-explorer/internal/discover"
	"api-audit-explorer/internal/discover/fake"
)

var wantChannels = []string{"Application", "System", "Microsoft-Windows-TaskScheduler/Operational"}

func eventsEnv(names ...string) (*fake.Env, discover.Env) {
	e := fake.New()
	e.Channels.Names = names
	return e, envWith(e)
}

func TestEventsDetectorOffersThreeChannels(t *testing.T) {
	_, env := eventsEnv("Application", "System", "Microsoft-Windows-TaskScheduler/Operational", "Setup")
	scan := discover.Run(context.Background(), env, []discover.Detector{discover.NewEventsDetector()})
	if len(scan.Gaps) != 0 || len(scan.Findings) != 3 {
		t.Fatalf("findings %d gaps %v", len(scan.Findings), scan.Gaps)
	}
	got := map[string]discover.Finding{}
	for _, f := range scan.Findings {
		got[f.Display] = f
		if f.Detector != "events" || f.Kind != discover.KindEventChannel || f.Importer != "win-events" || f.NeedsAdmin {
			t.Errorf("finding %+v", f)
		}
		if f.Cost == discover.CostLarge || f.Confidence <= 0 || f.ID == "" {
			t.Errorf("cost/confidence/id %+v", f)
		}
		if len(f.Paths()) != 0 {
			t.Errorf("a channel finding has paths: %v", f.Paths())
		}
		text, ok := discover.ShowsText(f.Shows)
		if !ok || text == "" {
			t.Errorf("%s: shows key %q has no text", f.Display, f.Shows)
		}
	}
	for _, n := range wantChannels {
		if _, ok := got[n]; !ok {
			t.Errorf("no finding for %s", n)
		}
	}
	if got["Application"].Shows == got["System"].Shows {
		t.Error("Application and System share one sentence")
	}
}

func TestEventsDetectorMatchesChannelNamesIgnoringCase(t *testing.T) {
	e, env := eventsEnv("APPLICATION", "system", "microsoft-windows-taskscheduler/operational")
	var asked []string
	env.Channels = &recordingChannels{inner: e.Channels, asked: &asked}
	fs, gs := discover.NewEventsDetector().Detect(context.Background(), env)
	if len(fs) != 3 || len(gs) != 0 {
		t.Fatalf("findings %d gaps %v", len(fs), gs)
	}
	// The probe uses the name as the host spells it.
	if strings.Join(asked, "|") != "APPLICATION|system|microsoft-windows-taskscheduler/operational" {
		t.Fatalf("asked %v", asked)
	}
}

type recordingChannels struct {
	inner discover.EventChannels
	mu    sync.Mutex
	asked *[]string
}

func (r *recordingChannels) List() ([]string, error) { return r.inner.List() }
func (r *recordingChannels) CanRead(name string) error {
	r.mu.Lock()
	*r.asked = append(*r.asked, name)
	r.mu.Unlock()
	return r.inner.CanRead(name)
}

func TestEventsDetectorNeverOffersSecurity(t *testing.T) {
	e, env := eventsEnv("Security", "Application", "System", "Microsoft-Windows-TaskScheduler/Operational", "security/Archive")
	var asked []string
	env.Channels = &recordingChannels{inner: e.Channels, asked: &asked}
	scan := discover.Run(context.Background(), env, []discover.Detector{discover.NewEventsDetector()})
	for _, f := range scan.Findings {
		if strings.Contains(strings.ToLower(f.Display), "security") {
			t.Fatalf("security offered: %+v", f)
		}
	}
	for _, g := range scan.Gaps {
		if strings.Contains(strings.ToLower(g.Display), "security") {
			t.Fatalf("security in a gap: %+v", g)
		}
	}
	for _, n := range asked {
		if strings.Contains(strings.ToLower(n), "security") {
			t.Fatalf("security probed: %q", n)
		}
	}
	if len(scan.Findings) != 3 {
		t.Fatalf("findings %d", len(scan.Findings))
	}
}

func TestEventsDetectorOnlyTheFixedChannels(t *testing.T) {
	_, env := eventsEnv("Application", "System", "Setup", "HardwareEvents", "Microsoft-Windows-Kernel-Power/Thermal-Operational")
	fs, gs := discover.NewEventsDetector().Detect(context.Background(), env)
	if len(fs) != 2 {
		t.Fatalf("findings %v", fs)
	}
	if len(gs) != 1 || gs[0].Display != "Microsoft-Windows-TaskScheduler/Operational" || gs[0].Reason != discover.ReasonNotFound {
		t.Fatalf("gaps %v", gs)
	}
}

func TestEventsDetectorDeniedChannelIsNeedsElevationGap(t *testing.T) {
	e, env := eventsEnv(wantChannels...)
	e.Channels.Denied = map[string]error{"System": discover.ErrAccessDenied}
	fs, gs := discover.NewEventsDetector().Detect(context.Background(), env)
	if len(fs) != 2 {
		t.Fatalf("findings %d", len(fs))
	}
	if len(gs) != 1 || gs[0].Display != "System" || gs[0].Reason != discover.ReasonNeedsElevation || gs[0].Kind != discover.KindEventChannel || gs[0].Detector != "events" {
		t.Fatalf("gaps %v", gs)
	}
	for _, f := range fs {
		if f.Display == "System" {
			t.Fatal("denied channel was also offered")
		}
	}
}

func TestEventsDetectorDeniedByWrappedPermissionError(t *testing.T) {
	// A real Env wraps the denial in a path error; the contract is errors.Is.
	e, env := eventsEnv(wantChannels...)
	e.Channels.Denied = map[string]error{"Application": &fs.PathError{Op: "channel", Path: "(channel)", Err: discover.ErrAccessDenied}}
	_, gs := discover.NewEventsDetector().Detect(context.Background(), env)
	if len(gs) != 1 || gs[0].Reason != discover.ReasonNeedsElevation {
		t.Fatalf("gaps %v", gs)
	}
}

func TestEventsDetectorDenyAllGapsEveryChannel(t *testing.T) {
	e, env := eventsEnv(wantChannels...)
	e.DenyAll()
	scan := discover.Run(context.Background(), env, []discover.Detector{discover.NewEventsDetector()})
	if len(scan.Findings) != 0 || len(scan.Gaps) != 3 {
		t.Fatalf("findings %d gaps %v", len(scan.Findings), scan.Gaps)
	}
	for _, g := range scan.Gaps {
		if g.Reason != discover.ReasonNeedsElevation {
			t.Errorf("gap %+v", g)
		}
	}
}

func TestEventsDetectorListDeniedOnlyListFails(t *testing.T) {
	e, env := eventsEnv(wantChannels...)
	e.Channels.ListErr = discover.ErrAccessDenied
	fs, gs := discover.NewEventsDetector().Detect(context.Background(), env)
	if len(fs) != 0 || len(gs) != 3 {
		t.Fatalf("findings %d gaps %v", len(fs), gs)
	}
	for _, g := range gs {
		if g.Reason != discover.ReasonNeedsElevation {
			t.Errorf("gap %+v", g)
		}
	}
}

func TestEventsDetectorOtherErrorsNeverLookLikeSuccess(t *testing.T) {
	e, env := eventsEnv(wantChannels...)
	e.Channels.Denied = map[string]error{"Application": fs.ErrInvalid}
	fs, gs := discover.NewEventsDetector().Detect(context.Background(), env)
	if len(fs) != 2 || len(gs) != 1 || gs[0].Reason != discover.ReasonUnreadable {
		t.Fatalf("findings %d gaps %v", len(fs), gs)
	}
}

func TestEventsDetectorEmptyHost(t *testing.T) {
	_, env := eventsEnv()
	fs, gs := discover.NewEventsDetector().Detect(context.Background(), env)
	if len(fs) != 0 || len(gs) != 3 {
		t.Fatalf("findings %d gaps %v", len(fs), gs)
	}
	for _, g := range gs {
		if g.Reason != discover.ReasonNotFound {
			t.Errorf("gap %+v", g)
		}
	}
}

func TestEventsDetectorCancelled(t *testing.T) {
	_, env := eventsEnv(wantChannels...)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fs, gs := discover.NewEventsDetector().Detect(ctx, env)
	if len(fs) != 0 || len(gs) != 3 {
		t.Fatalf("findings %d gaps %v", len(fs), gs)
	}
}

func TestEventsDetectorNoChannelsInterface(t *testing.T) {
	fs, gs := discover.NewEventsDetector().Detect(context.Background(), discover.Env{})
	if len(fs) != 0 || len(gs) != 1 || gs[0].Reason != discover.ReasonNotSupportedOS {
		t.Fatalf("findings %d gaps %v", len(fs), gs)
	}
}

func TestEventsWordingKeysAreRegistered(t *testing.T) {
	for _, k := range []string{"discover.eventsapp.shows", "discover.events.shows", "discover.taskscheduler.shows"} {
		if _, ok := discover.ShowsText(k); !ok {
			t.Errorf("missing %s", k)
		}
	}
}
