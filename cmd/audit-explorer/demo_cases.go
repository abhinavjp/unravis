package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/ledger"
	"api-audit-explorer/internal/masterkey"
	"api-audit-explorer/internal/mcp"
	"api-audit-explorer/internal/server"
)

// Capture-only demo settings (decisions B8 and B9). They exist so the screenshot
// harness can take the same pictures every time. Plain --demo ignores them, and
// without --demo they are ignored altogether.
const (
	envDemoNow   = "UNRAVIS_DEMO_NOW"
	envDemoCases = "UNRAVIS_DEMO_CASES"

	badDemoNowMessage      = "UNRAVIS_DEMO_NOW must look like 2026-10-20T09:00:00Z."
	badDemoCasesMessage    = "UNRAVIS_DEMO_CASES must be 1 to turn it on, or left out."
	demoCasesNeedConfigDir = "UNRAVIS_DEMO_CASES needs AUDIT_CONFIG_DIR so demo cases never touch your real cases."
	demoCasesNeedEmptyDir  = "UNRAVIS_DEMO_CASES needs an AUDIT_CONFIG_DIR with no real cases or master key in it, so demo cases never touch your real cases."

	demoImporter       = "iis-w3c"
	demoAckText        = "Demo data. Nothing here came from a real server."
	demoHost           = "web-a"
	demoEventsPerImort = 24
)

// demoCaseID builds a fixed case ID: the name, padded with "a" to the 26
// characters a case ID has. The first eight characters differ between the demo
// cases, because evidence IDs carry only those eight.
func demoCaseID(name string) casestore.CaseID {
	return casestore.CaseID(name + strings.Repeat("a", 26-len(name)))
}

var (
	demoCaseOpenID      = demoCaseID("demoopen")
	demoCaseClosedID    = demoCaseID("democlosed")
	demoCaseFailedID    = demoCaseID("demofailed")
	demoCaseUntrustedID = demoCaseID("demountrusted")
)

// demoCaseIDs lists the IDs above.
func demoCaseIDs() []casestore.CaseID {
	return []casestore.CaseID{demoCaseOpenID, demoCaseClosedID, demoCaseFailedID, demoCaseUntrustedID}
}

// demoSettings is what the two capture-only variables ask for.
type demoSettings struct {
	now   time.Time // the demo clock; the real time when UNRAVIS_DEMO_NOW is not set
	cases bool      // seed capture-only cases
}

// demoEnv reads and checks UNRAVIS_DEMO_NOW and UNRAVIS_DEMO_CASES. Without
// --demo both are ignored and notes says so, for the log. A bad value is an
// error with a plain message. It runs before the session token, the port and
// every file, so a refusal leaves nothing behind.
func demoEnv(demo bool, getenv func(string) string, realNow time.Time) (settings demoSettings, notes []string, err error) {
	settings.now = realNow
	rawNow := strings.TrimSpace(getenv(envDemoNow))
	rawCases := strings.TrimSpace(getenv(envDemoCases))
	if !demo {
		if rawNow != "" {
			notes = append(notes, envDemoNow+" is ignored without --demo.")
		}
		if rawCases != "" {
			notes = append(notes, envDemoCases+" is ignored without --demo.")
		}
		return settings, notes, nil
	}
	if rawNow != "" {
		at, parseErr := time.Parse(time.RFC3339, rawNow)
		if parseErr != nil {
			return demoSettings{}, nil, errors.New(badDemoNowMessage)
		}
		settings.now = at.UTC()
	}
	switch rawCases {
	case "", "0":
	case "1":
		if getenv("AUDIT_CONFIG_DIR") == "" {
			return demoSettings{}, nil, errors.New(demoCasesNeedConfigDir)
		}
		settings.cases = true
	default:
		return demoSettings{}, nil, errors.New(badDemoCasesMessage)
	}
	return settings, notes, nil
}

// demoClock is the case store's clock while demo cases exist. It is set by hand
// during seeding and then stays where it is, so every time on every screen is
// the same on every run.
type demoClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *demoClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *demoClock) set(t time.Time) {
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}

// checkDemoFolder refuses a folder that already holds anything but the demo's
// own cases. The demo never sits beside real cases or a real master key: they
// would show up in the demo and the demo would write next to them.
func checkDemoFolder(getenv func(string) string) error {
	base, err := mcp.SessionDir(getenv)
	if err != nil {
		return err
	}
	if _, statErr := os.Stat(filepath.Join(base, masterkey.FileName)); statErr == nil {
		return errors.New(demoCasesNeedEmptyDir)
	}
	root, err := casesRoot(getenv)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	own := map[string]bool{}
	for _, id := range demoCaseIDs() {
		own[string(id)] = true
	}
	for _, e := range entries {
		if !own[e.Name()] {
			return errors.New(demoCasesNeedEmptyDir)
		}
	}
	return nil
}

// openDemoCases opens the normal case store under AUDIT_CONFIG_DIR and seeds the
// demo's cases. It fails rather than falling back: a capture run with no cases
// would make wrong pictures without saying so.
func openDemoCases(getenv func(string) string, hypotheses ledger.Store, now time.Time) (*casesWiring, error) {
	if err := checkDemoFolder(getenv); err != nil {
		return nil, err
	}
	clk := &demoClock{t: now}
	var nextID casestore.CaseID
	var untrustedOn atomic.Bool
	seams := caseSeams{
		now:   clk.Now,
		newID: func() (casestore.CaseID, error) { return nextID, nil },
		wrapLegacy: func(inner func(casestore.CaseID) error) func(casestore.CaseID) error {
			return func(id casestore.CaseID) error {
				if untrustedOn.Load() && id == demoCaseUntrustedID {
					return casestore.ErrUntrustedLegacy
				}
				return inner(id)
			}
		},
	}
	wiring, err := openCasesWith(getenv, hypotheses, seams)
	if err != nil {
		return nil, fmt.Errorf("demo cases could not be opened: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	seeder := demoSeeder{store: wiring.store, clock: clk, setID: func(id casestore.CaseID) { nextID = id }, now: now}
	if err := seeder.seed(ctx); err != nil {
		wiring.close()
		return nil, fmt.Errorf("demo cases could not be seeded: %w", err)
	}
	untrustedOn.Store(true)
	clk.set(now)
	// The page hides Home and Case home in the demo. This tells it the demo has
	// cases, so the capture run can reach the case list and a case's home page.
	wiring.options = append(wiring.options, server.WithCaptureCases())
	log.Printf("Demo cases ready (%d), clock fixed at %s.", len(demoCaseIDs()), now.Format(time.RFC3339))
	return wiring, nil
}

// demoStore is the part of the case store the seeder needs.
type demoStore interface {
	Create(ctx context.Context, title string) (casestore.CaseInfo, error)
	Open(ctx context.Context, id casestore.CaseID) (casestore.Case, error)
	CloseCase(ctx context.Context, id casestore.CaseID) error
	List(ctx context.Context) ([]casestore.CaseInfo, error)
}

type demoSeeder struct {
	store demoStore
	clock *demoClock
	setID func(casestore.CaseID)
	now   time.Time
}

const day = 24 * time.Hour

// seed makes the cases. Times count back from now. A case that is already
// there (a second start on the same folder) is left as it is.
func (d demoSeeder) seed(ctx context.Context) error {
	existing, err := d.store.List(ctx)
	if err != nil {
		return err
	}
	have := map[casestore.CaseID]bool{}
	for _, info := range existing {
		have[info.ID] = true
	}
	steps := []struct {
		id    casestore.CaseID
		title string
		age   time.Duration // how long before now the case was created
		fill  func(ctx context.Context, c casestore.Case, created time.Time) error
	}{
		{demoCaseOpenID, "Checkout slowdown, 17 October", 3 * day,
			func(ctx context.Context, c casestore.Case, created time.Time) error {
				return d.importOK(ctx, c, "open-1", created.Add(10*time.Minute))
			}},
		{demoCaseClosedID, "Night batch overrun, 10 October", 10 * day,
			func(ctx context.Context, c casestore.Case, created time.Time) error {
				return d.importOK(ctx, c, "closed-1", created.Add(10*time.Minute))
			}},
		// A good import, then a file the importer refused. The "needs another try" list
		// arrives with the case lifecycle phase; until then the refused file stands in.
		{demoCaseFailedID, "Report export timeouts, 18 October", 2 * day,
			func(ctx context.Context, c casestore.Case, created time.Time) error {
				if err := d.importOK(ctx, c, "failed-1", created.Add(10*time.Minute)); err != nil {
					return err
				}
				return d.importRefused(ctx, c, "failed-2", created.Add(25*time.Minute))
			}},
		// Created like the others; openDemoCases then has the legacy check call it untrusted.
		{demoCaseUntrustedID, "Case from another folder", 6 * day,
			func(context.Context, casestore.Case, time.Time) error { return nil }},
	}
	for _, step := range steps {
		if have[step.id] {
			continue
		}
		created := d.now.Add(-step.age)
		d.clock.set(created)
		d.setID(step.id)
		info, err := d.store.Create(ctx, step.title)
		if err != nil {
			return fmt.Errorf("create %s: %w", step.id, err)
		}
		c, err := d.store.Open(ctx, info.ID)
		if err != nil {
			return fmt.Errorf("open %s: %w", step.id, err)
		}
		fillErr := step.fill(ctx, c, created)
		closeErr := c.Close()
		if fillErr != nil {
			return fmt.Errorf("fill %s: %w", step.id, fillErr)
		}
		if closeErr != nil {
			return closeErr
		}
		if step.id == demoCaseClosedID {
			d.clock.set(d.now.Add(-4 * day))
			if err := d.store.CloseCase(ctx, step.id); err != nil {
				return fmt.Errorf("close %s: %w", step.id, err)
			}
		}
	}
	return nil
}

func demoMeta(file string) casestore.ImportMeta {
	return casestore.ImportMeta{
		Importer: demoImporter, FileName: file, HostLabel: demoHost, TZRule: clock.RuleUTCField,
		AckGating: true, AckText: demoAckText,
	}
}

func demoBody(label string) io.Reader {
	return bytes.NewReader([]byte("synthetic demo body " + label + "\n"))
}

// demoEvents builds a fixed series of request events: no randomness, so the
// same label gives the same events.
func demoEvents(caseID casestore.CaseID, label string, start time.Time) []evidence.Event {
	out := make([]evidence.Event, demoEventsPerImort)
	for i := range out {
		line := int64(i + 2)
		duration := float64(120 + (i%6)*35)
		if i%8 == 7 {
			duration = 1800
		}
		at := start.Add(time.Duration(i) * time.Minute).UTC()
		out[i] = evidence.Event{
			EvidenceID: evidence.EventID(string(caseID), demoImporter, label, line, 0),
			Source:     demoImporter, Kind: "request", Host: demoHost,
			Start: at, End: at.Add(time.Duration(duration) * time.Millisecond),
			DurationMs: &duration, Unit: "ms", Line: line, TZRule: clock.RuleUTCField,
		}
	}
	return out
}

func (d demoSeeder) importOK(ctx context.Context, c casestore.Case, label string, at time.Time) error {
	d.clock.set(at)
	events := demoEvents(c.Info().ID, label, at.Add(-time.Hour))
	_, err := c.Import(ctx, demoMeta("demo-access-web-a.log"), demoBody(label), func(r io.Reader, emit func(evidence.Event) error) (importer.Report, error) {
		rep := importer.Report{Importer: demoImporter, KnownGaps: []string{"synthetic demo data"}}
		if _, err := io.Copy(io.Discard, r); err != nil {
			return rep, err
		}
		for _, e := range events {
			rep.Rows++
			if err := emit(e); err != nil {
				return rep, err
			}
			rep.Emitted++
		}
		return rep, nil
	})
	return err
}

func (d demoSeeder) importRefused(ctx context.Context, c casestore.Case, label string, at time.Time) error {
	d.clock.set(at)
	_, err := c.Import(ctx, demoMeta("demo-access-web-b.log"), demoBody(label), func(r io.Reader, _ func(evidence.Event) error) (importer.Report, error) {
		rep := importer.Report{Importer: demoImporter}
		if _, err := io.Copy(io.Discard, r); err != nil {
			return rep, err
		}
		return rep, &importer.QuarantineError{Reason: importer.ReasonBadTime, Column: "date"}
	})
	if errors.Is(err, importer.ErrQuarantine) {
		return nil
	}
	if err == nil {
		return errors.New("the demo's refused file was accepted")
	}
	return err
}
