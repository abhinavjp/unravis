// Package linuxperf imports Linux host CPU, memory and per-process samples from
// three text tools that sysstat and procps write:
//
//	linux-sadf    sadf -d (semicolon-delimited): host CPU (-u) and memory (-r),
//	              alone or both in one file
//	linux-pidstat pidstat -h -u -r: per-process CPU and memory, one epoch time
//	              column
//	linux-vmstat  vmstat -t (or -a -t): host CPU and free memory with a trailing
//	              date and time
//
// They feed the resource catalogue as win-counters and win-procsnap do: host
// CPU (host.cpu), available memory (host.mem.available) and per-process CPU and
// resident memory (proc.cpu, proc.ws).
//
// Three importers, not one with sub-modes: the three files have different
// headers, so each Detect is exact and a wrong pick is impossible to confuse;
// each tool has its own time form, host rule and set of columns that map to
// metrics; and the case export keeps one policy and one kind list per importer
// name, so a vmstat file that cannot say "available memory" or a CPU number is
// not forced into the attribute set of the others. They share this file: line
// loop, host mapping, number reading, process labels and event shape.
//
// Assumptions, each checked where the file allows it and stated otherwise:
//
//   - Locale: numbers use "." as the decimal point (run the tools with
//     LC_ALL=C). A number that does not read rejects its row (bad-number).
//   - Sizes: sadf kb* columns, pidstat VSZ and RSS and vmstat memory columns are
//     KiB (vmstat -S k, the default; vmstat does not print its unit). They are
//     stored in bytes.
//   - Time (sadf): "YYYY-MM-DD HH:MM:SS UTC" (sadf -d without -T) and a nine to
//     eleven digit epoch (sadf -U) are UTC. A time with no zone (sadf -T) is
//     host-local: it needs the host time zone the user chose (else the file is
//     refused, tz-unlabelled) and a time inside a clock change is rejected, never
//     guessed. A file whose first time cannot be read is refused (bad-time).
//   - Time (pidstat): the "Time" column of -h is epoch seconds, UTC. Without -h
//     the time is a clock string with no date, and the file is not read.
//   - Time (vmstat): the last header cell names the zone. "UTC" is UTC; any
//     other name is host-local and has to match the host time zone the user
//     chose at the first row (else the file is refused, tz-mismatch). vmstat
//     without -t has no time and is refused (bad-time).
//   - pidstat CPU percentages are of one core (the default; -I divides them by
//     the core count already and must not be used). They are divided by the
//     logical cores of the host the user chose and stored as percent of the host,
//     or kept as percent of one core, with a gap, when the core count is unknown.
//   - vmstat's first sample is an average since boot: its CPU values are not used.
//
// Host: the file is for one host. The user must pick it (no host refuses the
// file, unknown-host). A host name in the file (the sadf hostname column, the
// pidstat banner) is mapped as win-counters maps a counter path: with a host
// map it is the only mapping and an unknown name refuses the file; without one
// the picked host stands for exactly one name (any letter case) and a second name
// refuses the file. Names go to Report.ObservedHosts only. vmstat has none.
//
// Privacy: events carry a metric word from a fixed list, numbers and, for
// pidstat, a process label and a process id. A process label is the executable
// base name only: of the Command field the first word is read, a path is cut to
// its last part and nothing else (no argument, no later word) is kept or looked
// at. A name from the fixed server list is kept, any other name goes through
// Options.Pseudonymize("instance", name), a name that is not a plain image name
// is "other". The gap sentences are fixed.
package linuxperf

import (
	"context"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/textio"
	"api-audit-explorer/internal/importer/wincsv"
)

// Importer names, and the keys of the export policy.
const (
	NameSadf    = "linux-sadf"
	NamePidstat = "linux-pidstat"
	NameVmstat  = "linux-vmstat"
)

// Fixed gap sentences. Each passes evidence.LintCausal.
const (
	GapLocalTime   = "Times carry no zone in the file; the host time zone chosen by the user is applied."
	GapNoCores     = "per-process CPU is percent of one core; logical core count unknown"
	GapRestart     = "The file marks a restart of the host's counters; values on either side of it are not comparable."
	GapIntervalAvg = "Each value is an average over the sampling interval, not an instant."
	GapPidstatSeen = "pidstat lists the processes it sampled in each interval; a process that started and ended between two samples is not listed."
	GapNoStartTime = "pidstat has no process start time; a process id reused with the same name is not told apart."
	GapNoColumns   = "A block of the file has no CPU or memory column and was skipped."
	GapNoAvail     = "vmstat has no available-memory figure; only free memory is reported."
	GapFirstSample = "The first vmstat sample is an average since boot; its CPU values were not used."
	GapMemoryUnit  = "vmstat does not print its memory unit; KiB (the default) is assumed."
)

const (
	ctxCheckEvery = 256
	maxHeadLine   = 8192 // longest line Detect looks at
	maxHostName   = 255  // longest host name read from a file
)

// defaultLimits is the limit set of all three importers.
func defaultLimits() importer.Limits {
	return importer.Limits{
		MaxBytes: 256 << 20, MaxRows: 5_000_000, MaxEvents: 10_000_000,
		MaxLineBytes: 64 << 10, MaxFields: wincsv.DefaultMaxFields,
		MaxRejectFraction: wincsv.DefaultRejectFrac,
	}
}

// run is one parse in progress.
type run struct {
	ctx  context.Context
	opts importer.Options
	emit func(evidence.Event) error
	acc  *wincsv.Acc
	lr   *textio.LineReader
	n    int
	zone string // clock rule of the file's times, "" until the first time is read
}

// begin opens the line reader. rn is never nil: on an error the caller returns
// rn.acc.Fail(err).
func begin(ctx context.Context, r io.Reader, name string, opts importer.Options, emit func(evidence.Event) error) (*run, error) {
	lim := wincsv.MergeLimits(opts.Limits, defaultLimits())
	opts.Limits = lim
	rn := &run{ctx: ctx, opts: opts, emit: emit, acc: wincsv.NewAcc(name, lim)}
	if err := ctx.Err(); err != nil {
		return rn, err
	}
	pr, err := wincsv.Prepare(r, lim)
	if err != nil {
		return rn, err
	}
	lr, _, err := textio.NewLineReader(pr, lim.MaxLineBytes)
	if err != nil {
		return rn, err
	}
	rn.lr = lr
	return rn, nil
}

// nextLine returns the next line. done is true at the end of the input. A line
// that is not valid text is counted and rejected here and the next one is read.
func (rn *run) nextLine() (text string, no int64, done bool, err error) {
	for {
		if rn.n++; rn.n%ctxCheckEvery == 0 {
			if err := rn.ctx.Err(); err != nil {
				return "", 0, false, err
			}
		}
		line, no, err := rn.lr.Next()
		if errors.Is(err, io.EOF) {
			return "", no, true, nil
		}
		if err != nil {
			reason, ok := wincsv.RowReason(err)
			if !ok {
				return "", no, false, err
			}
			if e := rn.acc.Row(no); e != nil {
				return "", no, false, e
			}
			rn.acc.Reject(no, reason)
			continue
		}
		return string(line), no, false, nil
	}
}

// lockZone fixes the clock rule of the file at its first time. A later time that
// follows another rule is not admitted.
func (rn *run) lockZone(rule string) bool {
	if rn.zone == "" {
		rn.zone = rule
		rn.acc.Rep.Quality.TimezoneRule = rule
		rn.acc.Rep.FieldRules["timestamp"] = rule
		if rule == clock.RuleHostLocal {
			rn.acc.Gap(GapLocalTime)
		}
		return true
	}
	return rn.zone == rule
}

// localTime reads layout in loc.
func localTime(layout, value string, loc *time.Location) (time.Time, importer.Reason) {
	r, err := clock.ParseLocal(layout, value, loc)
	switch {
	case err != nil:
		return time.Time{}, importer.ReasonBadTime
	case r.Ambiguous:
		return time.Time{}, importer.ReasonDSTAmbiguous
	case r.Nonexistent:
		return time.Time{}, importer.ReasonDSTNonexistent
	}
	if !wincsv.SaneTime(r.UTC) {
		return time.Time{}, importer.ReasonBadTime
	}
	return r.UTC, ""
}

// utcTime reads layout as UTC.
func utcTime(layout, value string) (time.Time, importer.Reason) {
	t, err := time.Parse(layout, value)
	if err != nil || !wincsv.SaneTime(t) {
		return time.Time{}, importer.ReasonBadTime
	}
	return t.UTC(), ""
}

// epochTime reads a nine to eleven digit epoch in seconds.
func epochTime(s string) (time.Time, bool) {
	if len(s) < 9 || len(s) > 11 {
		return time.Time{}, false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return time.Time{}, false
		}
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	t := time.Unix(v, 0).UTC()
	if !wincsv.SaneTime(t) {
		return time.Time{}, false
	}
	return t, true
}

// hostMap maps the host names of a file to hosts, as win-counters does.
type hostMap struct {
	opts     importer.Options
	acc      *wincsv.Acc
	cache    map[string]importer.HostHint
	onlyName string
	haveOnly bool
}

func newHostMap(opts importer.Options, acc *wincsv.Acc) *hostMap {
	return &hostMap{opts: opts, acc: acc, cache: map[string]importer.HostHint{}}
}

// picked returns the host the user chose; a file for which it is the only source
// of the host (no name in the file) needs one.
func (m *hostMap) picked() (importer.HostHint, error) {
	if m.opts.Host == nil || m.opts.Host.Label == "" {
		return importer.HostHint{}, importer.ErrUnknownHost
	}
	return *m.opts.Host, nil
}

// resolve maps one raw name. The name goes to the report's observed hosts and
// nowhere else. An empty name is the picked host.
func (m *hostMap) resolve(name string) (importer.HostHint, error) {
	if name == "" {
		return m.picked()
	}
	if h, ok := m.cache[name]; ok {
		return h, nil
	}
	if err := m.acc.Observe(name); err != nil {
		return importer.HostHint{}, err
	}
	var (
		h  importer.HostHint
		ok bool
	)
	switch {
	case m.opts.Hosts != nil:
		// The host map is the only mapping. An unknown name is never filed
		// under the picked host.
		h, ok = m.opts.Hosts(name)
	case m.opts.Host != nil:
		// No host map: the picked host stands for exactly one machine name (any
		// letter case); a second name would put two machines under one label.
		key := strings.ToLower(name)
		if !m.haveOnly {
			m.onlyName, m.haveOnly = key, true
		}
		if key == m.onlyName {
			h, ok = *m.opts.Host, true
		}
	}
	if !ok || h.Label == "" {
		return importer.HostHint{}, importer.ErrUnknownHost
	}
	m.cache[name] = h
	return h, nil
}

// parseValue reads one number cell. A blank cell and "nan" (what the tools print
// for a value they could not work out) are missing, never zero. ok is false for
// a cell that is not a finite, non-negative number.
func parseValue(cell string) (v float64, present, ok bool) {
	cell = strings.TrimSpace(cell)
	switch strings.ToLower(cell) {
	case "", "nan", "-nan", "?", "-":
		return 0, false, true
	}
	f, err := strconv.ParseFloat(cell, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 {
		return 0, false, false
	}
	return f, true, true
}

// overPercent reports whether v is too large to be a percentage of a whole
// (a little slack for rounding). It catches a row read under the wrong header.
func overPercent(v float64) bool { return v > 100.5 }

// kibToBytes converts a KiB number to bytes.
func kibToBytes(v float64) float64 { return v * 1024 }

// events collects the events of one row, so that a row with a bad number
// emits nothing.
type events struct {
	rn   *run
	host string
	t    time.Time
	line int64
	out  []evidence.Event
}

func (rn *run) newRow(host string, t time.Time, line int64) *events {
	return &events{rn: rn, host: host, t: t, line: line}
}

// metric adds one metric event; ord is its position in the row (the id source).
func (e *events) metric(source string, ord int, name string, v float64, unit string, attrs map[string]string) {
	ev := wincsv.NewEvent(e.rn.opts, source, e.line, ord)
	val := v
	ev.Kind, ev.Host, ev.Start, ev.End = "metric", e.host, e.t, e.t
	ev.Value, ev.Unit, ev.TZRule = &val, unit, e.rn.zone
	a := map[string]string{"metric": name}
	for k, x := range attrs {
		a[k] = x
	}
	ev.Attrs = a
	e.out = append(e.out, ev)
}

// flush emits the row's events.
func (e *events) flush() error {
	if len(e.out) == 0 {
		return nil
	}
	acc := e.rn.acc
	if err := acc.CheckEmit(len(e.out)); err != nil {
		return err
	}
	for _, ev := range e.out {
		if err := e.rn.emit(ev); err != nil {
			return err
		}
		acc.Rep.Emitted++
		acc.Rep.Cover(e.t)
	}
	return nil
}

// firstTimeRefusal is the refusal for a file whose first time cannot be read.
func firstTimeRefusal() error {
	return &importer.QuarantineError{Reason: importer.ReasonBadTime, Column: "timestamp"}
}
