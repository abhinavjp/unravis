package main

import (
	"bytes"
	"context"
	"encoding/json"
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
	"api-audit-explorer/internal/dbhealth"
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
	// A Closed case kept apart for the p2-changed-elsewhere picture, which reopens and closes it from a
	// second tab. No other picture shows it, so its history never leaks into theirs.
	demoCaseStaleID     = demoCaseID("demostale")
	demoCaseUntrustedID = demoCaseID("demountrusted")
	// P3 seeds: an archive whose working copy is kept, one whose working copy was
	// freed, and a case whose folder is gone.
	demoCaseArchivedID = demoCaseID("demoarchived")
	demoCaseFreedID    = demoCaseID("demofreed")
	demoCaseMissingID  = demoCaseID("demomissing")
)

// demoCaseIDs lists the IDs above.
func demoCaseIDs() []casestore.CaseID {
	return []casestore.CaseID{demoCaseOpenID, demoCaseClosedID, demoCaseFailedID, demoCaseStaleID, demoCaseUntrustedID,
		demoCaseArchivedID, demoCaseFreedID, demoCaseMissingID}
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
		if strings.HasPrefix(e.Name(), ".") {
			continue // the store's own .index folder is not a case
		}
		// A case made in an earlier capture run on this folder has a demonew ID.
		if !own[e.Name()] && !isDemoNewID(e.Name()) {
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
	root, err := casesRoot(getenv)
	if err != nil {
		return nil, err
	}
	ids := &demoIDSource{exists: func(id casestore.CaseID) bool {
		_, statErr := os.Stat(filepath.Join(root, string(id)))
		return statErr == nil
	}}
	var untrustedOn atomic.Bool
	seams := caseSeams{
		now:   clk.Now,
		newID: ids.next,
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
	seeder := demoSeeder{store: wiring.store, ar: wiring.store, root: root, clock: clk, setID: ids.force, now: now}
	seedErr := seeder.seed(ctx)
	ids.force("") // nothing forced is left over: a case made after seeding gets a fresh ID
	if seedErr != nil {
		wiring.close()
		return nil, fmt.Errorf("demo cases could not be seeded: %w", seedErr)
	}
	untrustedOn.Store(true)
	clk.set(now)
	// The page hides Home and Case home in the demo. This tells it the demo has
	// cases, so the capture run can reach the case list and a case's home page.
	wiring.options = append(wiring.options, server.WithCaptureCases())
	log.Printf("Demo cases ready (%d), clock fixed at %s.", len(demoCaseIDs()), now.Format(time.RFC3339))
	return wiring, nil
}

// demoIDSource gives the case store its IDs. While seeding, force names the
// next ID, used once. After that every new case gets the next ID from a counter:
// fixed, valid and never one that is already taken, so a capture run that
// presses "Create case" gets the same ID on every run. Server goroutines call
// next at the same time, so it holds a lock.
type demoIDSource struct {
	mu     sync.Mutex
	forced casestore.CaseID
	count  int
	exists func(casestore.CaseID) bool // true if a case folder with this ID is already there
}

func (s *demoIDSource) force(id casestore.CaseID) {
	s.mu.Lock()
	s.forced = id
	s.mu.Unlock()
}

func (s *demoIDSource) next() (casestore.CaseID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.forced != "" {
		id := s.forced
		s.forced = ""
		return id, nil
	}
	for s.count < demoNewIDLimit {
		id := demoNewID(s.count)
		s.count++
		if s.exists == nil || !s.exists(id) {
			return id, nil
		}
	}
	return "", errors.New("the demo has no more case IDs to give")
}

// demoNewIDLimit is how many IDs the counter has: three base32 digits.
const demoNewIDLimit = 32 * 32 * 32

// demoNewID is "demonew", three base32 digits (lowest first, so the eighth
// character already differs between cases) and padding: 26 characters of a-z and 2-7.
func demoNewID(n int) casestore.CaseID {
	const digits = "abcdefghijklmnopqrstuvwxyz234567"
	suffix := string([]byte{digits[n%32], digits[(n/32)%32], digits[(n/1024)%32]})
	return demoCaseID("demonew" + suffix)
}

// isDemoNewID reports whether name is an ID demoNewID can make.
func isDemoNewID(name string) bool {
	id := casestore.CaseID(name)
	return id.Valid() && strings.HasPrefix(name, "demonew")
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
	ar    casestore.Archiver // nil: no archive seeds
	root  string
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
		// A good import, then a file the importer refused, and the two-item "needs another try"
		// list they left: a file that did not import, a database not added to the analysis (C19).
		{demoCaseFailedID, "Report export timeouts, 18 October", 2 * day,
			func(ctx context.Context, c casestore.Case, created time.Time) error {
				if err := d.importOK(ctx, c, "failed-1", created.Add(10*time.Minute)); err != nil {
					return err
				}
				if err := d.importRefused(ctx, c, "failed-2", created.Add(25*time.Minute)); err != nil {
					return err
				}
				if err := d.retryItems(ctx, c, created.Add(30*time.Minute)); err != nil {
					return err
				}
				// A SQL Server day whose hours held nothing to read: it stands for screen p7-nothing-read.
				return d.laneDayNothingRead(ctx, c, created.Add(40*time.Minute))
			}},
		{demoCaseStaleID, "Ledger posting lag, 12 October", 8 * day,
			func(ctx context.Context, c casestore.Case, created time.Time) error {
				return d.importOK(ctx, c, "stale-1", created.Add(10*time.Minute))
			}},
		{demoCaseArchivedID, "Month-end batch stall, 2 October", 26 * day,
			func(ctx context.Context, c casestore.Case, created time.Time) error {
				return d.importOK(ctx, c, "archived-1", created.Add(10*time.Minute))
			}},
		{demoCaseFreedID, "Payment gateway retries, 28 September", 30 * day,
			func(ctx context.Context, c casestore.Case, created time.Time) error {
				return d.importOK(ctx, c, "freed-1", created.Add(10*time.Minute))
			}},
		{demoCaseMissingID, "Search index rebuild, 9 October", 19 * day,
			func(ctx context.Context, c casestore.Case, created time.Time) error {
				return d.importOK(ctx, c, "missing-1", created.Add(10*time.Minute))
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
		if step.id == demoCaseClosedID || step.id == demoCaseStaleID {
			closedAgo := 4 * day
			if step.id == demoCaseStaleID {
				closedAgo = 3 * day
			}
			d.clock.set(d.now.Add(-closedAgo))
			if err := d.store.CloseCase(ctx, step.id); err != nil {
				return fmt.Errorf("close %s: %w", step.id, err)
			}
		}
		if err := d.afterCreate(ctx, step.id, step.title); err != nil {
			return fmt.Errorf("finish %s: %w", step.id, err)
		}
	}
	return nil
}

// laneDayNothingRead keeps one SQL Server day in which every check came back Cannot test yet and none has a permission to fix, so the capture
// run opens straight onto that screen. (The demo's own fake server gives the same answer for a database named EmptyDb.) Nothing here came from a server.
func (d demoSeeder) laneDayNothingRead(ctx context.Context, c casestore.Case, at time.Time) error {
	hs, ok := c.(casestore.HealthStore)
	if !ok {
		return errors.New("the demo case cannot keep a SQL Server day")
	}
	d.clock.set(at)
	hour := func(daysBack int) dbhealth.Window {
		start := d.now.Add(-2 * day).Truncate(24 * time.Hour).Add(13 * time.Hour).Add(-time.Duration(daysBack) * day)
		return dbhealth.Window{Start: start, End: start.Add(time.Hour)}
	}
	const why = "The server answered, but it holds nothing for these hours."
	lane := dbhealth.LaneResult{Incident: hour(0), Reference: hour(1), Era: "2022", Sources: []string{"SQL Server"}}
	rep := dbhealth.Report{Kind: "sql", Label: "SQL Server", Database: "AuditDb", Version: "16.0", StartedAt: at, FinishedAt: at, Overall: dbhealth.Unavailable, Lane: &lane}
	for _, ck := range []struct{ id, title string }{
		{dbhealth.CheckServer, "Server start and thread pool"}, {dbhealth.CheckErrorLog, "Error log"},
		{dbhealth.CheckQueryStore, "Query Store"}, {dbhealth.CheckAgentJobs, "Agent jobs and backups"},
	} {
		lane.Checks = append(lane.Checks, dbhealth.LaneCheck{ID: ck.id, Title: ck.title, State: dbhealth.StateNotYet, Summary: why})
		rep.Findings = append(rep.Findings, dbhealth.Finding{ID: ck.id, Title: ck.title, Status: dbhealth.Unavailable, Summary: why})
	}
	rep.Lane = &lane
	enc, err := json.Marshal(rep)
	if err != nil {
		return err
	}
	_, err = hs.AddHealthRun(ctx, casestore.HealthRun{Kind: "sql", Label: rep.Label, Status: string(rep.Overall), Report: enc, Key: server.LaneDayKey("sql", rep)})
	return err
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

// retryItems writes the two items the demo's "needs another try" list holds. They
// carry words and ids only, as the store requires.
func (d demoSeeder) retryItems(ctx context.Context, c casestore.Case, at time.Time) error {
	rs, ok := c.(casestore.RetryStore)
	if !ok {
		return errors.New("the demo case store keeps no retry list")
	}
	d.clock.set(at)
	_, err := rs.ReplaceRetries(ctx, []casestore.RetryItem{
		{Kind: casestore.RetryImport, Key: "file:demo-access-web-b.log", Label: "demo-access-web-b.log", Reason: "bad-time"},
		{Kind: casestore.RetryAudit, Key: "audit:demo-reports", Label: "Reports database", Reason: "unreachable"},
	})
	return err
}

// afterCreate takes the three P3 seeds to their states. The archives carry the
// record a real archive writes (the file, its check value, its counts) but no file
// on disk: the pictures show the lists and the case home, and a case file made by
// hand is what the Verify and Restore pictures use.
func (d demoSeeder) afterCreate(ctx context.Context, id casestore.CaseID, title string) error {
	if d.ar == nil {
		return nil
	}
	switch id {
	case demoCaseArchivedID, demoCaseFreedID:
		closedAgo, archivedAgo := 22*day, 14*day
		if id == demoCaseFreedID {
			closedAgo, archivedAgo = 27*day, 21*day
		}
		d.clock.set(d.now.Add(-closedAgo))
		if err := d.store.CloseCase(ctx, id); err != nil {
			return err
		}
		at := d.now.Add(-archivedAgo)
		d.clock.set(at)
		folder := `C:\Unravis case files`
		name := demoArchiveName(title, id, at)
		if _, err := d.ar.BeginArchive(ctx, id, "", casestore.ArchivePending{Folder: folder, Name: name, Started: at.UTC()}); err != nil {
			return err
		}
		sum := strings.Repeat(string(id)[4:8], 16) // fixed text: 64 hex-looking characters
		file := casestore.ArchiveFile{
			Name: name, Folder: folder, Bytes: 4_718_592, SHA256: sum, KeyKind: casestore.KeyKindMaster,
			KeyCheck: strings.Repeat("0", 60) + "7c3e", Made: at.UTC(),
			Counts: casestore.Counts{Imports: 1, Sources: 0, Events: demoEventsPerImort},
		}
		if err := d.ar.CommitArchive(ctx, id, file); err != nil {
			return err
		}
		if id == demoCaseFreedID {
			d.clock.set(d.now.Add(-14 * day))
			if err := d.ar.BeginFree(ctx, id, ""); err != nil {
				return err
			}
			return d.ar.FinishFree(ctx, id)
		}
	case demoCaseMissingID:
		d.clock.set(d.now.Add(-12 * day))
		if err := d.store.CloseCase(ctx, id); err != nil {
			return err
		}
		// The folder is removed behind the application's back: Unravis lists the case
		// as "Working copy missing" from its record outside the folder.
		return os.RemoveAll(filepath.Join(d.root, string(id)))
	}
	return nil
}

// demoArchiveName spells a case file name the way Archive does.
func demoArchiveName(title string, id casestore.CaseID, at time.Time) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	slug := strings.TrimRight(b.String(), "-")
	return slug + "-" + string(id)[:8] + "-" + at.UTC().Format("20060102-150405") + ".unravis-case"
}
