package discover_test

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/discover"
	"api-audit-explorer/internal/discover/fake"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/all"
)

// hostSentinel is a secret marker placed in file content the host detector
// reads but must never echo.
const hostSentinel = "SENTINEL-7f3a"

var hostModTime = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

const hostSite = `C:\inetpub\wwwroot\api`

func hostTestEnv(e *fake.Env) discover.Env { return e.Env(all.Registry()) }

// hostStubImporter is a registry entry with no parsing. It lets a test show that
// a finding is kept when its importer is registered.
type hostStubImporter struct{ name string }

func (s hostStubImporter) Name() string                 { return s.name }
func (hostStubImporter) Detect(_ []byte, _ string) int  { return 0 }
func (hostStubImporter) AllowedAttrs() map[string]bool  { return nil }
func (hostStubImporter) DefaultLimits() importer.Limits { return importer.Limits{} }
func (hostStubImporter) Parse(_ context.Context, _ io.Reader, _ importer.Options, _ func(evidence.Event) error) (importer.Report, error) {
	return importer.Report{}, nil
}

func hostLiveCount(findings []discover.Finding) int {
	n := 0
	for _, f := range findings {
		if f.Kind == discover.KindLiveSeries {
			n++
		}
	}
	return n
}

func hostSiteOpts(dirs ...string) discover.HostOptions {
	return discover.HostOptions{SiteDirs: func(context.Context) []string { return dirs }}
}

func assertHostNoSentinel(t *testing.T, findings []discover.Finding, gaps []discover.Gap) {
	t.Helper()
	b, err := json.Marshal(struct {
		Findings []discover.Finding `json:"findings"`
		Gaps     []discover.Gap     `json:"gaps"`
	}{findings, gaps})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), hostSentinel) {
		t.Errorf("secret marker reached the JSON: %s", b)
	}
	for _, f := range findings {
		for _, p := range f.Paths() {
			if strings.Contains(p, hostSentinel) {
				t.Errorf("secret marker reached a path: %q", p)
			}
		}
	}
}

func TestHostLiveSeriesGate(t *testing.T) {
	ctx := context.Background()
	d := discover.NewHost(discover.HostOptions{})

	// No registry: the finding is offered.
	findings, _ := d.Detect(ctx, fake.New().Env(nil))
	if hostLiveCount(findings) != 1 {
		t.Fatalf("no registry: live findings = %d, want 1", hostLiveCount(findings))
	}
	if f := findings[0]; f.Importer != "win-live" || f.Shows != "discover.live.shows" {
		t.Errorf("live finding importer %q shows %q", f.Importer, f.Shows)
	}

	// A registry without win-live: the finding is skipped without a gap.
	findings, gaps := d.Detect(ctx, hostTestEnv(fake.New()))
	if hostLiveCount(findings) != 0 || len(gaps) != 0 {
		t.Errorf("registry without win-live: live %d gaps %+v, want none", hostLiveCount(findings), gaps)
	}

	// A registry with win-live: the finding is kept and passes validation.
	reg := importer.NewRegistry(hostStubImporter{name: "win-live"})
	findings, _ = d.Detect(ctx, fake.New().Env(reg))
	if hostLiveCount(findings) != 1 {
		t.Errorf("registry with win-live: live findings = %d, want 1", hostLiveCount(findings))
	}
	fake.AssertFindingsValid(t, findings, reg)
}

func TestHostPerfmonCSVLogSet(t *testing.T) {
	e := fake.New()
	csv := loadFixture(t, "../importer/wincounters/testdata/counters_basic.csv")
	e.FS.AddFile(`C:\PerfLogs\Admin\SetA_000001\DataCollector01.csv`, int64(len(csv)), hostModTime, csv)

	findings, gaps := discover.NewHost(discover.HostOptions{}).Detect(context.Background(), hostTestEnv(e))
	if len(gaps) != 0 {
		t.Errorf("gaps = %+v, want none", gaps)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(findings))
	}
	f := findings[0]
	if f.Kind != discover.KindLogSet || f.Importer != "win-counters" || f.Confidence != 70 {
		t.Errorf("finding = kind %q importer %q confidence %d, want log-set win-counters 70", f.Kind, f.Importer, f.Confidence)
	}
	if f.Display != "SetA_000001" || f.Files != 1 || f.Shows != "discover.perfmon.shows" {
		t.Errorf("display %q files %d shows %q", f.Display, f.Files, f.Shows)
	}
	if got := f.Paths(); len(got) != 1 || !strings.HasSuffix(got[0], `\DataCollector01.csv`) {
		t.Errorf("paths = %q", got)
	}
	assertHostNoSentinel(t, findings, gaps)
	fake.AssertFindingsValid(t, findings, all.Registry())
}

func TestHostPerfmonBLGIsExportNeeded(t *testing.T) {
	e := fake.New()
	e.FS.AddFile(`C:\PerfLogs\Admin\SetB\Counters.blg`, 4096, hostModTime, []byte("\x00\x01binary"))

	findings, gaps := discover.NewHost(discover.HostOptions{}).Detect(context.Background(), hostTestEnv(e))
	if len(gaps) != 0 || len(findings) != 1 {
		t.Fatalf("findings %d gaps %+v, want one finding and no gap", len(findings), gaps)
	}
	f := findings[0]
	if f.Kind != discover.KindExportNeeded || f.How != "discover.perfmon.how" {
		t.Errorf("kind %q how %q, want export-needed with a how key", f.Kind, f.How)
	}
	if f.Importer != "" || len(f.Paths()) != 0 {
		t.Errorf("export-needed carries importer %q and %d path(s)", f.Importer, len(f.Paths()))
	}
	fake.AssertFindingsValid(t, findings, all.Registry())
}

func TestHostPerfmonDepthLimit(t *testing.T) {
	e := fake.New()
	csv := loadFixture(t, "../importer/wincounters/testdata/counters_basic.csv")
	// Three folders below Admin is read; a fourth is not.
	e.FS.AddFile(`C:\PerfLogs\Admin\a\b\c\near.csv`, int64(len(csv)), hostModTime, csv)
	e.FS.AddFile(`C:\PerfLogs\Admin\a\b\c\d\deep.csv`, int64(len(csv)), hostModTime, csv)

	findings, _ := discover.NewHost(discover.HostOptions{}).Detect(context.Background(), hostTestEnv(e))
	if len(findings) != 1 || findings[0].Display != "c" {
		t.Fatalf("findings = %+v, want only the folder three levels down", findings)
	}
}

func TestHostPerfmonMissingGivesNothing(t *testing.T) {
	findings, gaps := discover.NewHost(discover.HostOptions{}).Detect(context.Background(), hostTestEnv(fake.New()))
	if len(findings) != 0 || len(gaps) != 0 {
		t.Errorf("empty host: findings %d gaps %+v, want none", len(findings), gaps)
	}
}

func TestHostPerfmonDeniedNeedsElevation(t *testing.T) {
	e := fake.New()
	e.FS.AddFile(`C:\PerfLogs\Admin\SetA\x.csv`, 10, hostModTime, []byte("x"))
	e.DenyAll()

	// Importers are nil, so the live-series finding is offered; the Perfmon walk
	// must add gaps and no finding of its own.
	findings, gaps := discover.NewHost(discover.HostOptions{}).Detect(context.Background(), e.Env(nil))
	for _, f := range findings {
		if f.Kind != discover.KindLiveSeries {
			t.Errorf("denied host produced a %s finding", f.Kind)
		}
	}
	if len(gaps) == 0 {
		t.Fatal("denied Perfmon folder was swallowed, no gap reported")
	}
	for _, g := range gaps {
		if g.Reason != discover.ReasonNeedsElevation {
			t.Errorf("gap %q has reason %q, want needs-elevation", g.Display, g.Reason)
		}
	}
}

func TestHostUnsafeSystemDriveIsGap(t *testing.T) {
	findings, gaps := discover.NewHost(discover.HostOptions{SystemDrive: `\\server\c$`}).Detect(context.Background(), hostTestEnv(fake.New()))
	if len(findings) != 0 {
		t.Errorf("unsafe system drive produced %d finding(s)", len(findings))
	}
	if len(gaps) != 1 || gaps[0].Reason != discover.ReasonUnsafePath {
		t.Errorf("gaps = %+v, want one unsafe-path gap", gaps)
	}
}

func TestHostDotNetCountersInSiteFolder(t *testing.T) {
	e := fake.New()
	counters := loadFixture(t, "../importer/dotnetcounters/testdata/counters_basic.csv")
	e.FS.AddDir(hostSite)
	e.FS.AddFile(hostSite+`\counters.csv`, int64(len(counters)), hostModTime, counters)
	notes := []byte("a,b\n1,2\n" + hostSentinel + "\n")
	e.FS.AddFile(hostSite+`\notes.csv`, int64(len(notes)), hostModTime, notes)

	findings, gaps := discover.NewHost(hostSiteOpts(hostSite)).Detect(context.Background(), hostTestEnv(e))
	if len(gaps) != 0 {
		t.Errorf("gaps = %+v, want none", gaps)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1 (only the dotnet-counters file)", len(findings))
	}
	f := findings[0]
	if f.Kind != discover.KindLogFile || f.Importer != "dotnet-counters" || f.Display != "counters.csv" {
		t.Errorf("finding = kind %q importer %q display %q", f.Kind, f.Importer, f.Display)
	}
	if got := f.Paths(); len(got) != 1 || got[0] != hostSite+`\counters.csv` {
		t.Errorf("paths = %q", got)
	}
	assertHostNoSentinel(t, findings, gaps)
	fake.AssertFindingsValid(t, findings, all.Registry())
}

func TestHostNettraceIsExportNeeded(t *testing.T) {
	e := fake.New()
	e.FS.AddFile(hostSite+`\trace.nettrace`, 2048, hostModTime, []byte("Nettrace\x00binary"))

	findings, gaps := discover.NewHost(hostSiteOpts(hostSite)).Detect(context.Background(), hostTestEnv(e))
	if len(gaps) != 0 || len(findings) != 1 {
		t.Fatalf("findings %d gaps %+v, want one finding and no gap", len(findings), gaps)
	}
	f := findings[0]
	if f.Kind != discover.KindExportNeeded || f.Shows != "discover.nettrace.shows" || f.How != "discover.nettrace.how" {
		t.Errorf("kind %q shows %q how %q", f.Kind, f.Shows, f.How)
	}
	if len(f.Paths()) != 0 || f.Importer != "" {
		t.Errorf("export-needed carries importer %q and %d path(s)", f.Importer, len(f.Paths()))
	}
}

func TestHostSiteFolderMissingGivesNothing(t *testing.T) {
	findings, gaps := discover.NewHost(hostSiteOpts(`C:\nowhere\api`)).Detect(context.Background(), hostTestEnv(fake.New()))
	if len(findings) != 0 || len(gaps) != 0 {
		t.Errorf("missing site folder: findings %d gaps %+v, want none", len(findings), gaps)
	}
}

func TestHostSiteFolderUNCIsGap(t *testing.T) {
	findings, gaps := discover.NewHost(hostSiteOpts(`\\fileserver\share\api`)).Detect(context.Background(), hostTestEnv(fake.New()))
	if len(findings) != 0 {
		t.Errorf("UNC site folder produced %d finding(s)", len(findings))
	}
	if len(gaps) != 1 || gaps[0].Reason != discover.ReasonUnsafePath {
		t.Errorf("gaps = %+v, want one unsafe-path gap", gaps)
	}
}

func TestHostSiteFolderDeniedNeedsElevation(t *testing.T) {
	e := fake.New()
	counters := loadFixture(t, "../importer/dotnetcounters/testdata/counters_basic.csv")
	e.FS.AddFile(hostSite+`\counters.csv`, int64(len(counters)), hostModTime, counters)
	e.FS.AddFile(hostSite+`\trace.nettrace`, 10, hostModTime, []byte("x"))
	e.DenyAll()

	findings, gaps := discover.NewHost(hostSiteOpts(hostSite)).Detect(context.Background(), e.Env(all.Registry()))
	for _, f := range findings {
		if f.Kind != discover.KindLiveSeries {
			t.Errorf("denied site folder produced a %s finding", f.Kind)
		}
	}
	if len(gaps) == 0 {
		t.Fatal("denied site folder was swallowed, no gap reported")
	}
	for _, g := range gaps {
		if g.Reason != discover.ReasonNeedsElevation {
			t.Errorf("gap %q has reason %q, want needs-elevation", g.Display, g.Reason)
		}
	}
}
