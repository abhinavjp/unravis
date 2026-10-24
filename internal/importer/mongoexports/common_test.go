package mongoexports

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/importertest"
	"api-audit-explorer/internal/importer/mongodlog"
)

// dropped marks the cells of the good fixtures that must never reach an event (filter
// values, user, appName, client). The harness sentinel lives in the bad fixtures only.
const dropped = "DROPPED-4d2e"

type cfg struct {
	name string
	mk   func() importer.Importer
	good string
	bad  string
	opts importer.Options
}

func hostA(loc *time.Location) *importer.HostHint {
	return &importer.HostHint{Label: "mongo-a", Role: "mongo", TZ: loc}
}

func baseOpts() importer.Options {
	return importer.Options{CaseID: "caseabcdefgh", ImportNonce: "nonce-1", FileName: "export.jsonl", Host: hostA(time.UTC)}
}

func statOpts() importer.Options {
	o := baseOpts()
	o.Supplied = map[string]string{"date": "2001-09-09"}
	return o
}

var configs = []cfg{
	{NameProfile, NewProfile, "profile-good.jsonl", "profile-bad.jsonl", baseOpts()},
	{NameCurrentOp, NewCurrentOp, "currentop-good.jsonl", "currentop-bad.jsonl", baseOpts()},
	{NameServerStatus, NewServerStatus, "serverstatus-good.jsonl", "serverstatus-bad.jsonl", baseOpts()},
	{NameMongostat, NewMongostat, "mongostat-good.jsonl", "mongostat-bad.jsonl", statOpts()},
}

func readFixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (c cfg) fixtures(t testing.TB) importertest.Fixtures {
	t.Helper()
	return importertest.Fixtures{
		Good:       readFixture(t, c.good),
		Bad:        readFixture(t, c.bad),
		NoFreeText: true, // events carry fixed words, counts and hex hashes only
		Options:    c.opts,
	}
}

func parse(t testing.TB, imp importer.Importer, data []byte, o importer.Options) ([]evidence.Event, importer.Report, error) {
	t.Helper()
	return importer.Collect(context.Background(), imp, bytes.NewReader(data), o)
}

func mustParse(t testing.TB, imp importer.Importer, data []byte, o importer.Options) ([]evidence.Event, importer.Report) {
	t.Helper()
	evs, rep, err := parse(t, imp, data, o)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return evs, rep
}

func TestConformance(t *testing.T) {
	for _, c := range configs {
		t.Run(c.name, func(t *testing.T) { importertest.Run(t, c.mk(), c.fixtures(t)) })
	}
}

func FuzzProfile(f *testing.F)   { importertest.FuzzParse(f, NewProfile(), configs[0].fixtures(f)) }
func FuzzCurrentOp(f *testing.F) { importertest.FuzzParse(f, NewCurrentOp(), configs[1].fixtures(f)) }
func FuzzServerStatus(f *testing.F) {
	importertest.FuzzParse(f, NewServerStatus(), configs[2].fixtures(f))
}
func FuzzMongostat(f *testing.F) { importertest.FuzzParse(f, NewMongostat(), configs[3].fixtures(f)) }

// The attribute allowlist of each importer is the case-export policy's attribute set.
func TestAllowedAttrsMatchExportPolicy(t *testing.T) {
	for _, c := range configs {
		imp := c.mk()
		want := evidence.ExportAttrNames(imp.Name())
		if want == nil {
			t.Fatalf("%s: no export policy", imp.Name())
		}
		if !reflect.DeepEqual(imp.AllowedAttrs(), want) {
			t.Errorf("%s: AllowedAttrs %v, export policy %v", imp.Name(), imp.AllowedAttrs(), want)
		}
	}
}

// Every event of every good fixture validates and carries the host label, never a name
// from the file; evidence ids are unique within the file.
func TestGoodFixturesYieldValidEvents(t *testing.T) {
	for _, c := range configs {
		imp := c.mk()
		evs, rep := mustParse(t, imp, readFixture(t, c.good), c.opts)
		if len(evs) == 0 || rep.Rejected != 0 {
			t.Fatalf("%s: %d events, %d rejected", c.name, len(evs), rep.Rejected)
		}
		ids := map[string]bool{}
		for i, ev := range evs {
			if err := ev.ValidateFor(c.opts.CaseID, imp.Name(), imp.AllowedAttrs()); err != nil {
				t.Fatalf("%s event %d: %v", c.name, i, err)
			}
			if ev.Host != "mongo-a" {
				t.Errorf("%s event %d: host %q", c.name, i, ev.Host)
			}
			if ids[ev.EvidenceID] {
				t.Errorf("%s event %d: evidence id repeats", c.name, i)
			}
			ids[ev.EvidenceID] = true
		}
		for _, g := range rep.KnownGaps {
			if err := evidence.LintCausal(g); err != nil {
				t.Errorf("%s gap %q: %v", c.name, g, err)
			}
		}
		if len(rep.KnownGaps) == 0 {
			t.Errorf("%s: no known gaps stated", c.name)
		}
	}
}

// Privacy: query text, namespaces, users, application names, client addresses and host
// names from the file never reach an event, the report, a gap or an error.
func TestDroppedCellsNeverSurface(t *testing.T) {
	for _, c := range configs {
		imp := c.mk()
		data := readFixture(t, c.good)
		if !bytes.Contains(data, []byte(dropped)) && (c.name == NameProfile || c.name == NameCurrentOp) {
			t.Fatalf("%s: fixture has no marker", c.name)
		}
		evs, rep := mustParse(t, imp, data, c.opts)
		var all []string
		all = append(all, fmt.Sprintf("%#v", evs), fmt.Sprintf("%+v", rep.KnownGaps), fmt.Sprintf("%+v", rep.FieldRules),
			fmt.Sprintf("%+v", rep.Quality), fmt.Sprintf("%+v", rep.Errors))
		for _, ev := range evs {
			if _, ok := ev.Attrs["ns"]; ok {
				t.Errorf("%s: an event carries ns", c.name)
			}
			for k, v := range ev.Attrs {
				all = append(all, k, v)
			}
		}
		for _, s := range all {
			for _, bad := range []string{dropped, "app.orders", "app.events", "admin", "192.0.2.", "example.test", "reporting", "svc", "customer"} {
				if strings.Contains(s, bad) {
					t.Errorf("%s: %q reached the output: %.120s", c.name, bad, s)
				}
			}
		}
	}
}

// The importer reads query text into a throwaway buffer: a line made of nothing but
// client text still gives fixed-category events only.
func TestProfileKeepsOnlyFixedWords(t *testing.T) {
	line := `{"op":"` + importertest.Sentinel + `","ns":"` + importertest.Sentinel + `","command":{"` + importertest.Sentinel +
		`":"x","filter":{"a":"` + importertest.Sentinel + `"}},"planSummary":"` + importertest.Sentinel +
		` { a: 1 }","queryHash":"` + importertest.Sentinel + `","user":"` + importertest.Sentinel + `","appName":"` +
		importertest.Sentinel + `","client":"` + importertest.Sentinel + `","ts":{"$date":"2001-09-09T01:00:05.000Z"},"millis":5}` + "\n"
	evs, rep := mustParse(t, NewProfile(), []byte(line), baseOpts())
	if len(evs) != 1 {
		t.Fatalf("%d events", len(evs))
	}
	if len(evs[0].Attrs) != 0 {
		t.Fatalf("unlisted words were kept: %v", evs[0].Attrs)
	}
	if s := fmt.Sprintf("%#v %+v", evs, rep); strings.Contains(s, importertest.Sentinel) {
		t.Fatal("the sentinel reached the output")
	}
}

// FTDC diagnostic.data is binary BSON: refused as an unsupported format by every importer.
func TestFTDCRefused(t *testing.T) {
	// int32 length, 0x09 (date element), "_id\0", int64 millis, then more bytes.
	ftdc := []byte{0x40, 0x00, 0x00, 0x00, 0x09, '_', 'i', 'd', 0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x03, 'd', 'o', 'c'}
	for _, c := range configs {
		evs, _, err := parse(t, c.mk(), ftdc, c.opts)
		var q *importer.QuarantineError
		if !errors.As(err, &q) || q.Reason != importer.ReasonUnsupportedFormat || len(evs) != 0 {
			t.Errorf("%s: FTDC head: %d events, %v", c.name, len(evs), err)
		}
		if got := c.mk().Detect(ftdc, "metrics.2001-09-09T01-00-00Z-00000"); got != 0 {
			t.Errorf("%s: Detect scored FTDC %d", c.name, got)
		}
	}
}

// Detect keeps its distance between the four importers and from the mongod log importer.
func TestDetectMargins(t *testing.T) {
	logHead, err := os.ReadFile("../mongodlog/testdata/good.log")
	if err != nil {
		t.Fatal(err)
	}
	if len(logHead) > importer.HeadSize {
		logHead = logHead[:importer.HeadSize]
	}
	for i, c := range configs {
		head := readFixture(t, c.good)
		if len(head) > importer.HeadSize {
			head = head[:importer.HeadSize]
		}
		own := c.mk().Detect(head, "")
		if own < 85 {
			t.Errorf("%s: own score %d", c.name, own)
		}
		for j, o := range configs {
			if i != j {
				if s := o.mk().Detect(head, ""); s != 0 {
					t.Errorf("%s scores the %s fixture %d", o.name, c.name, s)
				}
			}
		}
		if s := mongodlog.New().Detect(head, ""); s > 0 {
			t.Errorf("mongod-json scores the %s fixture %d", c.name, s)
		}
		if s := c.mk().Detect(logHead, ""); s != 0 {
			t.Errorf("%s scores a mongod.log head %d", c.name, s)
		}
		// A file name that fits adds to the score; a name that does not changes nothing.
		hinted := c.mk().Detect(head, map[string]string{NameProfile: "system.profile.jsonl", NameCurrentOp: "currentOp.jsonl",
			NameServerStatus: "serverStatus.jsonl", NameMongostat: "mongostat.jsonl"}[c.name])
		if hinted <= own {
			t.Errorf("%s: hint lowered the score (%d < %d)", c.name, hinted, own)
		}
	}
	for _, c := range configs {
		for _, junk := range []string{"", "plain text\n", "a,b,c\n1,2,3\n", "[1,2,3]\n", "{}\n", "{\"x\":1}\n"} {
			if s := c.mk().Detect([]byte(junk), "x.txt"); s != 0 {
				t.Errorf("%s scores %q %d", c.name, junk, s)
			}
		}
	}
}

// The card's caps are ceilings: a caller limit can lower them and never raise them.
func TestCapsAreCeilings(t *testing.T) {
	for _, c := range configs {
		imp := c.mk()
		dl := imp.DefaultLimits()
		if dl.MaxBytes == 0 || dl.MaxRows == 0 || dl.MaxLineBytes == 0 || dl.MaxEvents == 0 {
			t.Errorf("%s: a default limit is unset: %+v", c.name, dl)
		}
		s := imp.(impl).sp
		s.def.MaxEvents = s.maxEvents // as run does
		eff := s.effective(importer.Limits{MaxBytes: 1 << 50, MaxRows: 1 << 50, MaxLineBytes: 1 << 40, MaxEvents: 1 << 50})
		if eff.MaxBytes != s.def.MaxBytes || eff.MaxRows != s.def.MaxRows || eff.MaxLineBytes != s.def.MaxLineBytes || eff.MaxEvents != s.maxEvents {
			t.Errorf("%s: caps raised: %+v", c.name, eff)
		}
		eff = s.effective(importer.Limits{MaxBytes: 1000, MaxRows: 7, MaxLineBytes: 99})
		if eff.MaxBytes != 1000 || eff.MaxRows != 7 || eff.MaxLineBytes != 99 {
			t.Errorf("%s: caps not lowered: %+v", c.name, eff)
		}
	}
}

// A line that is not strict UTF-8 is one rejected row and the rest is read.
func TestInvalidUTF8LineIsOneRejectedRow(t *testing.T) {
	good := string(readFixture(t, "profile-good.jsonl"))
	lines := strings.Split(strings.TrimSpace(good), "\n")
	lines = append(lines[:2], append([]string{"\xff\xfe not text"}, lines[2:]...)...)
	o := baseOpts()
	o.Limits.MaxRejectFraction = 1
	evs, rep, err := parse(t, NewProfile(), []byte(strings.Join(lines, "\n")+"\n"), o)
	if err != nil || len(evs) != 6 || rep.Rejected != 1 || len(rep.Errors) != 1 ||
		rep.Errors[0].Reason != importer.ReasonEncoding || rep.Errors[0].Line != 3 {
		t.Fatalf("%v events %d report %+v", err, len(evs), rep)
	}
}

// Windows keep the events that overlap them.
func TestWindowFilter(t *testing.T) {
	o := baseOpts()
	o.Window = importer.Window{From: time.Date(2001, 9, 9, 1, 0, 10, 0, time.UTC), To: time.Date(2001, 9, 9, 1, 0, 20, 0, time.UTC)}
	evs, _ := mustParse(t, NewProfile(), readFixture(t, "profile-good.jsonl"), o)
	if len(evs) == 0 || len(evs) >= 6 {
		t.Fatalf("window kept %d of 6", len(evs))
	}
	for _, ev := range evs {
		if ev.End.Before(o.Window.From) || ev.Start.After(o.Window.To) {
			t.Errorf("event outside window: %v-%v", ev.Start, ev.End)
		}
	}
}

// No picked host and no map: the file is refused, and the raw name stays out of the error.
func TestUnknownHostRefused(t *testing.T) {
	o := baseOpts()
	o.Host = nil
	for _, c := range configs[1:3] {
		evs, _, err := parse(t, c.mk(), readFixture(t, c.good), o)
		if !errors.Is(err, importer.ErrUnknownHost) || len(evs) != 0 {
			t.Errorf("%s: %d events, %v", c.name, len(evs), err)
		}
		if err != nil && strings.Contains(err.Error(), "example.test") {
			t.Errorf("%s: raw host in the error", c.name)
		}
	}
	// The profile has no host name at all: it needs the picked host.
	if _, _, err := parse(t, NewProfile(), readFixture(t, "profile-good.jsonl"), o); !errors.Is(err, importer.ErrUnknownHost) {
		t.Errorf("profile without a host: %v", err)
	}
}

// With a host map the names in the file reach labels only through the map.
func TestHostMapUsed(t *testing.T) {
	o := baseOpts()
	o.Host = nil
	o.Hosts = func(name string) (importer.HostHint, bool) {
		if name == "mongo-a.example.test" {
			return importer.HostHint{Label: "mongo-a", Role: "mongo"}, true
		}
		return importer.HostHint{}, false
	}
	evs, rep := mustParse(t, NewServerStatus(), readFixture(t, "serverstatus-good.jsonl"), o)
	if len(evs) == 0 || evs[0].Host != "mongo-a" {
		t.Fatalf("%d events", len(evs))
	}
	if !reflect.DeepEqual(rep.ObservedHosts, []string{"mongo-a.example.test"}) {
		t.Errorf("observed hosts %v", rep.ObservedHosts)
	}
}
