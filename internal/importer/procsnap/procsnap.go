// Package procsnap imports process snapshots: Get-Process piped through
// Export-Csv, or tasklist /fo csv, each with a SampleUtc column that the
// documented recipe adds. Every snapshot lists the processes running at one
// sample time.
//
// Get-Process columns: SampleUtc, ProcessName and Id are required; CPU
// (cumulative processor seconds), WorkingSet64, PrivateMemorySize64,
// HandleCount, Threads (a count) and StartTimeUtc are read when present.
// tasklist columns: SampleUtc, Image Name and PID are required; Mem Usage
// (kilobytes, "1,887,436 K") is read when present. Every other column is dropped
// at parse. A header with a column that can hold a user, a window title, a
// command line or a path (UserName, User Name, Window Title, MainWindowTitle,
// CommandLine, Path, Company, Description and the like) refuses the file: the
// importer never reads such a cell, and the file is not stored with it.
//
// Process names: a name is the image name with ".exe" removed. Server images
// (w3wp, dotnet, node, redis-server, memurai, mongod, sqlservr) are kept. Any
// other name that has the form of an image name goes through
// Options.Pseudonymize("instance", name) and is stored as "instance-<hash>"; a
// name with another form (a path, spaces, a command line) is stored as "other".
//
// Events: "metric" (one per metric and process per snapshot; cpu is the change in
// cumulative CPU seconds over the change in sample time between two snapshots of
// the same process, as percent of the host's logical cores, or percent of one core
// with a gap when the core count is unknown), "process-start" (a point at
// StartTimeUtc, the first time a process is listed; it does not widen coverage)
// and "process-gone" (an interval from the last snapshot that listed a process to
// the first snapshot that did not). A process is the triple process id, start
// time and name, so a process id reused with another start time is a new process.
// tasklist has no start time, so a reused process id with one name is not told
// apart. A blank cell is missing, never zero. Times are UTC ("o" format, with a
// Z); a culture-format first time refuses the file with a hint. The host is the
// one picked for the file; the file holds no machine name.
package procsnap

import (
	"context"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/wincsv"
)

// Name is the importer name.
const Name = "win-procsnap"

// GapNoCores is the gap for cpu values that are percent of one core.
const GapNoCores = "per-process CPU is percent of one core; logical core count unknown"

// Fixed gap sentences.
const (
	GapSnapshotsOnly = "snapshots list the processes running at each sample time; a process that started and ended between two snapshots is not listed"
	GapGoneWindow    = "a process exit time lies between the last snapshot that listed the process and the first snapshot that did not"
	GapNoStartTime   = "tasklist output has no process start time; a process id reused with the same image name between snapshots is not told apart"
	GapNotTracked    = "the file lists more distinct processes than are followed; CPU, start and exit are not reported for the rest"
	GapCPUSkipped    = "some CPU values were skipped; the cumulative CPU time went down between two snapshots"
	GapTimeFormat    = "SampleUtc is not in ISO 8601 form; write it with (Get-Date).ToUniversalTime().ToString('o')"
)

// maxTracked bounds the processes followed at one time (a variable for tests).
var maxTracked = 100_000

// Ordinals of the events of one row, for stable evidence ids.
const (
	ordCPU = iota
	ordWorkingSet
	ordPrivate
	ordHandles
	ordThreads
	ordStart
	ordGone
)

// New returns the process snapshot importer.
func New() importer.Importer { return &imp{} }

type imp struct{}

func (m *imp) Name() string { return Name }

// AllowedAttrs: metric names the measure, process is a server image or a derived
// label, pid is a number.
func (m *imp) AllowedAttrs() map[string]bool {
	return map[string]bool{"metric": true, "process": true, "pid": true}
}

func (m *imp) DefaultLimits() importer.Limits {
	return importer.Limits{
		MaxBytes: 256 << 20, MaxRows: 5_000_000, MaxEvents: 10_000_000,
		MaxLineBytes: wincsv.DefaultMaxLineBytes, MaxFields: wincsv.DefaultMaxFields,
		MaxRejectFraction: wincsv.DefaultRejectFrac,
	}
}

// norm folds a header cell: lower case, no spaces, no underscores.
func norm(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.NewReplacer(" ", "", "_", "", " ", "").Replace(s)
}

// Detect scores a header with SampleUtc and either ProcessName and Id (Get-Process)
// or Image Name and PID (tasklist): 90. Otherwise 0.
func (m *imp) Detect(head []byte, _ string) int {
	s := wincsv.HeadText(head)
	if i := strings.IndexByte(s, '\n'); i >= 0 && strings.HasPrefix(s, "#") { // "#TYPE ..." line
		s = s[i+1:]
	}
	if j := strings.IndexByte(s, '\n'); j >= 0 {
		s = s[:j]
	}
	set := map[string]bool{}
	for _, c := range strings.Split(s, ",") {
		set[norm(strings.Trim(strings.TrimSpace(c), `"`))] = true
	}
	if set["sampleutc"] && ((set["processname"] && set["id"]) || (set["imagename"] && set["pid"])) {
		return 90
	}
	return 0
}

// quarantined lists the header columns, folded, that can hold a user name, a
// window title, a command line or a path, with the fixed name reported for each.
var quarantined = map[string]string{
	"username": "UserName", "user": "UserName", "windowtitle": "WindowTitle",
	"mainwindowtitle": "MainWindowTitle", "commandline": "CommandLine", "path": "Path",
	"company": "Company", "description": "Description", "services": "Services", "modules": "Modules",
}

// serverImages are kept by name; every other image is pseudonymised.
var serverImages = map[string]bool{
	"w3wp": true, "dotnet": true, "node": true, "redis-server": true,
	"memurai": true, "mongod": true, "sqlservr": true,
}

// imageName returns the lower-case image name of a cell with ".exe" removed, and
// whether it has the form of an image name (1 to 64 of A-Z a-z 0-9 _ . # -).
func imageName(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if n := len(s); n >= 4 && strings.EqualFold(s[n-4:], ".exe") {
		s = s[:n-4]
	}
	if len(s) == 0 || len(s) > 64 {
		return "", false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '_', c == '.', c == '#', c == '-':
		default:
			return "", false
		}
	}
	return strings.ToLower(s), true
}

// ident identifies one process: id, start time (unix nanoseconds, 0 when unknown)
// and the stored name.
type ident struct {
	pid   uint32
	start int64
	label string
}

// proc is the state kept for a followed process.
type proc struct {
	seen      int       // index of the last snapshot that listed it
	seenT     time.Time // that snapshot's time
	lastLine  int64     // line of its last row
	label     string
	pid       string
	startDone bool // process-start emitted
	hasCPU    bool
	cpu       float64
	cpuT      time.Time
}

// columns holds the positions of the columns that are read, -1 when absent.
type columns struct {
	tasklist                                  bool
	time, name, id                            int
	cpu, ws, private, handles, threads, start int
	mem                                       int
}

func (m *imp) Parse(ctx context.Context, r io.Reader, opts importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
	lim := wincsv.MergeLimits(opts.Limits, m.DefaultLimits())
	acc := wincsv.NewAcc(Name, lim)
	if err := ctx.Err(); err != nil {
		return acc.Fail(err)
	}
	cr, err := wincsv.OpenCSVSkipTypeLine(r, lim)
	if err != nil {
		return acc.Fail(err)
	}
	hdr := cr.Header()
	idx := map[string]int{}
	dups := map[string]bool{}
	for i, h := range hdr {
		k := norm(h)
		if fixed, bad := quarantined[k]; bad {
			return acc.Fail(&importer.QuarantineError{Reason: importer.ReasonUnscrubbedColumn, Column: fixed})
		}
		if _, dup := idx[k]; dup {
			dups[k] = true // a repeated column we read is refused below, by its fixed name
			continue
		}
		idx[k] = i
	}
	// A column we read must appear exactly once: a second copy could hold other data.
	pos := func(name string, required bool) (int, error) {
		k := norm(name)
		i, ok := idx[k]
		switch {
		case !ok && !required:
			return -1, nil
		case !ok || dups[k]:
			return -1, wincsv.MissingColumn(name)
		}
		return i, nil
	}
	var c columns
	c.tasklist = !has(idx, "processname") && has(idx, "imagename")
	type colSpec struct {
		dst      *int
		name     string
		required bool
	}
	specs := []colSpec{{&c.time, "SampleUtc", true}}
	if c.tasklist {
		specs = append(specs, colSpec{&c.name, "Image Name", true}, colSpec{&c.id, "PID", true},
			colSpec{&c.mem, "Mem Usage", false})
		c.cpu, c.ws, c.private, c.handles, c.threads, c.start = -1, -1, -1, -1, -1, -1
	} else {
		specs = append(specs, colSpec{&c.name, "ProcessName", true}, colSpec{&c.id, "Id", true},
			colSpec{&c.cpu, "CPU", false}, colSpec{&c.ws, "WorkingSet64", false},
			colSpec{&c.private, "PrivateMemorySize64", false}, colSpec{&c.handles, "HandleCount", false},
			colSpec{&c.threads, "Threads", false}, colSpec{&c.start, "StartTimeUtc", false})
		c.mem = -1
	}
	for _, s := range specs {
		if *s.dst, err = pos(s.name, s.required); err != nil {
			return acc.Fail(err)
		}
	}
	acc.Rep.Rule("SampleUtc", clock.RuleUTCField)
	acc.Rep.Quality.TimezoneRule = clock.RuleUTCField
	if opts.Host == nil || opts.Host.Label == "" {
		return acc.Fail(importer.ErrUnknownHost)
	}
	host := *opts.Host
	acc.Gap(GapSnapshotsOnly)
	if c.tasklist {
		acc.Gap(GapNoStartTime)
	}

	s := &state{
		acc: acc, opts: opts, emit: emit, host: host, c: c,
		procs: map[ident]*proc{}, names: map[string]string{},
	}
	return s.run(ctx, cr, len(hdr))
}

func has(m map[string]int, k string) bool { _, ok := m[k]; return ok }

// state is one parse in progress.
type state struct {
	acc   *wincsv.Acc
	opts  importer.Options
	emit  func(evidence.Event) error
	host  importer.HostHint
	c     columns
	procs map[ident]*proc
	names map[string]string // lower-case image name -> stored name

	snap int       // index of the current snapshot, 0 before the first
	curT time.Time // its time
}

// label returns the stored name for an image-name cell.
func (s *state) label(cell string) string {
	n, ok := imageName(cell)
	if !ok {
		return "other"
	}
	if l, ok := s.names[n]; ok {
		return l
	}
	l := ProcessLabel(n, s.opts.Pseudonymize)
	if len(s.names) < 4096 {
		s.names[n] = l
	}
	return l
}

func (s *state) run(ctx context.Context, cr interface {
	Next() ([]string, int64, error)
}, nHdr int) (importer.Report, error) {
	acc, c := s.acc, s.c
	anyTime := false
	for {
		if err := ctx.Err(); err != nil {
			return acc.Fail(err)
		}
		rec, line, err := cr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			reason, ok := wincsv.RowReason(err)
			if !ok {
				return acc.Fail(err)
			}
			if e := acc.Row(line); e != nil {
				return acc.Fail(e)
			}
			acc.Reject(line, reason)
			continue
		}
		if e := acc.Row(line); e != nil {
			return acc.Fail(e)
		}
		if len(rec) != nHdr {
			acc.Reject(line, importer.ReasonFieldCount)
			continue
		}

		cell := strings.TrimSpace(rec[c.time])
		t, perr := time.Parse(time.RFC3339Nano, cell)
		if perr != nil || !(strings.HasSuffix(cell, "Z") || strings.HasSuffix(cell, "z")) {
			if !anyTime && perr != nil && wincsv.LooksLikeCultureTime(cell) {
				acc.Gap(GapTimeFormat)
				return acc.Fail(&importer.QuarantineError{Reason: importer.ReasonBadTime, Column: "SampleUtc"})
			}
			acc.Reject(line, importer.ReasonBadTime)
			continue
		}
		utc := t.UTC()
		if !wincsv.SaneTime(utc) {
			acc.Reject(line, importer.ReasonBadTime)
			continue
		}
		anyTime = true
		if s.snap > 0 && utc.Before(s.curT) { // rows are in time order
			acc.Reject(line, importer.ReasonBadTime)
			continue
		}
		if s.snap == 0 || utc.After(s.curT) {
			if s.snap > 0 {
				if err := s.finish(); err != nil {
					return acc.Fail(err)
				}
			}
			s.snap++
			s.curT = utc
		}
		if reason, err := s.row(rec, line, utc); err != nil {
			return acc.Fail(err)
		} else if reason != "" {
			acc.Reject(line, reason)
		}
	}
	if s.snap > 0 {
		if err := s.finish(); err != nil {
			return acc.Fail(err)
		}
	}
	return acc.Finish()
}

// parseUint reads a whole number of at most bits bits.
func parseUint(cell string, bits int) (uint64, bool) {
	v, err := strconv.ParseUint(strings.TrimSpace(cell), 10, bits)
	return v, err == nil
}

// memUsageBytes reads a tasklist "Mem Usage" cell ("1,887,436 K"): kilobytes with
// a thousands separator of the exporting machine's culture. "N/A" is missing.
func memUsageBytes(cell string) (v float64, present, ok bool) {
	cell = strings.TrimSpace(cell)
	if cell == "" || strings.EqualFold(cell, "n/a") {
		return 0, false, true
	}
	if n := len(cell); n > 0 && (cell[n-1] == 'K' || cell[n-1] == 'k') {
		cell = cell[:n-1]
	} else {
		return 0, false, false
	}
	var kb uint64
	digits := 0
	for _, r := range cell {
		switch {
		case r >= '0' && r <= '9':
			kb = kb*10 + uint64(r-'0')
			digits++
			if digits > 15 {
				return 0, false, false
			}
		case r == ',' || r == '.' || r == ' ' || r == ' ' || r == ' ':
		default:
			return 0, false, false
		}
	}
	if digits == 0 {
		return 0, false, false
	}
	return float64(kb) * 1024, true, true
}

// row handles one row of the current snapshot. It returns a reject reason for the
// row (events already emitted for it stay), or an error that ends the parse.
func (s *state) row(rec []string, line int64, utc time.Time) (importer.Reason, error) {
	acc, c := s.acc, s.c
	pidN, ok := parseUint(rec[c.id], 32)
	if !ok {
		return importer.ReasonBadNumber, nil
	}
	var startT time.Time
	if c.start >= 0 {
		if sc := strings.TrimSpace(rec[c.start]); sc != "" {
			t, err := time.Parse(time.RFC3339Nano, sc)
			if err != nil || !(strings.HasSuffix(sc, "Z") || strings.HasSuffix(sc, "z")) || !wincsv.SaneTime(t) {
				return importer.ReasonBadTime, nil
			}
			startT = t.UTC()
		}
	}
	label := s.label(rec[c.name])
	key := ident{pid: uint32(pidN), label: label}
	if !startT.IsZero() {
		key.start = startT.UnixNano()
	}
	pidStr := strconv.FormatUint(pidN, 10)

	p := s.procs[key]
	switch {
	case p != nil && p.seen == s.snap:
		return importer.ReasonUnsupportedFormat, nil // the same process twice in one snapshot
	case p != nil:
	case len(s.procs) < maxTracked:
		p = &proc{label: label, pid: pidStr}
		s.procs[key] = p
	default:
		acc.Gap(GapNotTracked)
	}
	if p != nil { // listed, whether or not its numbers parse
		p.seen, p.seenT, p.lastLine = s.snap, utc, line
	}

	// Parse every number before emitting any event of the row.
	var cpu, ws, priv, handles, threads float64
	var haveCPU, haveWS, havePriv, haveHandles, haveThreads bool
	num := func(i int, dst *float64, have *bool, bits int) bool {
		if i < 0 || strings.TrimSpace(rec[i]) == "" {
			return true
		}
		v, ok := parseUint(rec[i], bits)
		*dst, *have = float64(v), true
		return ok
	}
	if c.cpu >= 0 && strings.TrimSpace(rec[c.cpu]) != "" {
		v, err := strconv.ParseFloat(strings.TrimSpace(rec[c.cpu]), 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			return importer.ReasonBadNumber, nil
		}
		cpu, haveCPU = v, true
	}
	if c.mem >= 0 {
		v, present, ok := memUsageBytes(rec[c.mem])
		if !ok {
			return importer.ReasonBadNumber, nil
		}
		ws, haveWS = v, present
	}
	if !num(c.ws, &ws, &haveWS, 62) || !num(c.private, &priv, &havePriv, 62) ||
		!num(c.handles, &handles, &haveHandles, 32) || !num(c.threads, &threads, &haveThreads, 32) {
		return importer.ReasonBadNumber, nil
	}

	var out []evidence.Event
	metric := func(ord int, name string, v float64, unit string) {
		e := wincsv.NewEvent(s.opts, Name, line, ord)
		val := v
		e.Kind, e.Host, e.Start, e.End = "metric", s.host.Label, utc, utc
		e.Value, e.Unit, e.TZRule = &val, unit, clock.RuleUTCField
		e.Attrs = map[string]string{"metric": name, "process": label, "pid": pidStr}
		out = append(out, e)
	}
	if haveCPU && p != nil {
		if p.hasCPU {
			dt := utc.Sub(p.cpuT).Seconds()
			switch d := cpu - p.cpu; {
			case d < 0:
				acc.Gap(GapCPUSkipped)
			case dt > 0:
				pct := d / dt * 100
				unit := "percent-one-core"
				if s.host.LogicalCores > 0 {
					pct /= float64(s.host.LogicalCores)
					unit = "percent-host"
				} else {
					acc.Gap(GapNoCores)
				}
				metric(ordCPU, "cpu", pct, unit)
			}
		}
		p.cpu, p.cpuT, p.hasCPU = cpu, utc, true
	}
	if haveWS {
		metric(ordWorkingSet, "working-set", ws, "bytes")
	}
	if havePriv {
		metric(ordPrivate, "private-bytes", priv, "bytes")
	}
	if haveHandles {
		metric(ordHandles, "handles", handles, "count")
	}
	if haveThreads {
		metric(ordThreads, "threads", threads, "count")
	}
	if p != nil && !p.startDone {
		p.startDone = true
		if !startT.IsZero() {
			e := wincsv.NewEvent(s.opts, Name, line, ordStart)
			e.Kind, e.Host, e.Start, e.End, e.TZRule = "process-start", s.host.Label, startT, startT, clock.RuleUTCField
			e.Attrs = map[string]string{"process": label, "pid": pidStr}
			out = append(out, e)
		}
	}
	if len(out) == 0 {
		return "", nil
	}
	if err := acc.CheckEmit(len(out)); err != nil {
		return "", err
	}
	for _, e := range out {
		if err := s.emit(e); err != nil {
			return "", err
		}
		// Coverage is the span of sample times: a start time before the first
		// snapshot does not widen it.
		acc.Rep.Emitted++
		acc.Rep.Cover(utc)
	}
	return "", nil
}

// finish closes the current snapshot: every followed process that it did not list
// is gone, with its exit time between its last listing and this snapshot. Gone
// processes are no longer followed.
func (s *state) finish() error {
	var gone []*proc
	for k, p := range s.procs {
		if p.seen < s.snap {
			gone = append(gone, p)
			delete(s.procs, k)
		}
	}
	if len(gone) == 0 {
		return nil
	}
	sort.Slice(gone, func(i, j int) bool { return gone[i].lastLine < gone[j].lastLine })
	s.acc.Gap(GapGoneWindow)
	for _, p := range gone {
		if err := s.acc.CheckEmit(1); err != nil {
			return err
		}
		e := wincsv.NewEvent(s.opts, Name, p.lastLine, ordGone)
		e.Kind, e.Host, e.Start, e.End, e.TZRule = "process-gone", s.host.Label, p.seenT, s.curT, clock.RuleUTCField
		e.Attrs = map[string]string{"process": p.label, "pid": p.pid}
		if err := s.emit(e); err != nil {
			return err
		}
		s.acc.Rep.Emitted++
		s.acc.Rep.Cover(s.curT)
	}
	return nil
}
