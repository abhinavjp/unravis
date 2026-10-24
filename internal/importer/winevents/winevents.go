// Package winevents imports Windows event log rows exported with the documented
// recipe: Get-WinEvent | Select-Object ... | Export-Csv.
//
// Required columns: a time column (TimeCreatedUtc, ISO 8601 with Z; or a local
// TimeCreated in sortable "s" form, accepted only with the picked host's time
// zone), plus MachineName, LogName, ProviderName, Id and Level. AppPool is kept
// when present. Message, Properties and every other column are dropped at parse:
// message text is never stored. Local times go through clock.ParseLocal; a time
// inside a DST fold or gap rejects that row (dst-ambiguous, dst-nonexistent) and
// is never guessed. A local TimeCreated is in the zone of the machine that ran
// the export, not of MachineName, so only the zone of the host picked for the
// file is used; with none, the file is refused. A first time cell in the
// exporting machine's culture format refuses the file with a hint.
//
// Hosts: with hosts.json every MachineName must map through it. Without it the
// picked host stands for one machine name; a second name refuses the file. A
// name is never filed under the picked host just because it is unmapped. A
// leading "#TYPE" line is skipped; no other line is treated as a comment.
package winevents

import (
	"context"
	"io"
	"strconv"
	"strings"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/wincsv"
)

// Name is the importer name.
const Name = "win-events"

// GapNoTaskEvents is added when the file has no TaskScheduler/Operational rows.
const GapNoTaskEvents = "TaskScheduler/Operational is disabled by default; no task events is not evidence that no task ran"

// GapLocalExporterZone is added when the time column is a local TimeCreated.
const GapLocalExporterZone = "TimeCreated is in the time zone of the machine that ran the export, which may differ from the zone of MachineName; the zone picked for the file is applied to every row"

// GapTimeFormat is added when the file is refused for a time column that is not in ISO 8601 form.
const GapTimeFormat = "The time column is not in ISO 8601 form; export TimeCreatedUtc with TimeCreated.ToUniversalTime().ToString('o') instead of the default culture format"

const taskLog = "Microsoft-Windows-TaskScheduler/Operational"

// Classifier maps a provider and event id to a Kind and allowlisted attributes.
// ok false means "not mine". INT wires iis.ClassifyWAS and ClassifyTaskScheduler.
type Classifier func(provider string, id int) (kind string, attrs map[string]string, ok bool)

// New returns the events importer. Classifiers are tried in order; the first that
// returns ok wins. Rows nobody classifies get Kind "win-event".
func New(classifiers ...Classifier) importer.Importer { return &imp{cls: classifiers} }

type imp struct{ cls []Classifier }

func (m *imp) Name() string { return Name }

// AllowedAttrs: log, provider, id and app-pool come from columns; reason from classifiers.
func (m *imp) AllowedAttrs() map[string]bool {
	return map[string]bool{"log": true, "provider": true, "id": true, "app-pool": true, "reason": true}
}

func (m *imp) DefaultLimits() importer.Limits {
	return importer.Limits{
		MaxBytes: 256 << 20, MaxRows: 2_000_000, MaxEvents: 2_000_000,
		MaxLineBytes: wincsv.DefaultMaxLineBytes, MaxFields: wincsv.DefaultMaxFields,
		MaxRejectFraction: wincsv.DefaultRejectFrac,
	}
}

// Detect scores an Export-Csv header from Get-WinEvent: 90 when it names a
// TimeCreated column with MachineName, LogName and ProviderName.
func (m *imp) Detect(head []byte, _ string) int {
	s := strings.ToLower(wincsv.HeadText(head))
	if i := strings.IndexByte(s, '\n'); i >= 0 && strings.HasPrefix(s, "#") { // "#TYPE ..." line
		s = s[i+1:]
	}
	if j := strings.IndexByte(s, '\n'); j >= 0 {
		s = s[:j]
	}
	if strings.Contains(s, "timecreated") && strings.Contains(s, "machinename") &&
		strings.Contains(s, "logname") && strings.Contains(s, "providername") {
		return 90
	}
	return 0
}

// local time layouts: sortable "s" form, with or without the T; fractions are accepted.
var localLayouts = []string{"2006-01-02T15:04:05", "2006-01-02 15:04:05"}

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
		k := strings.ToLower(strings.TrimSpace(h))
		if _, dup := idx[k]; dup {
			dups[k] = true // a repeated column we read is refused below, by its fixed name
			continue
		}
		idx[k] = i
	}
	// A column we read must appear exactly once: a second copy could hold other data.
	need := func(name string) (int, error) {
		k := strings.ToLower(name)
		i, ok := idx[k]
		if !ok || dups[k] {
			return 0, wincsv.MissingColumn(name)
		}
		return i, nil
	}
	var cMach, cLog, cProv, cID, cLevel int
	for _, p := range []struct {
		dst  *int
		name string
	}{{&cMach, "MachineName"}, {&cLog, "LogName"}, {&cProv, "ProviderName"}, {&cID, "Id"}, {&cLevel, "Level"}} {
		if *p.dst, err = need(p.name); err != nil {
			return acc.Fail(err)
		}
	}
	rule, timeCol, timeName := clock.RuleUTCField, 0, "TimeCreatedUtc"
	if _, ok := idx["timecreatedutc"]; !ok {
		if _, ok := idx["timecreated"]; ok {
			rule, timeName = clock.RuleHostLocal, "TimeCreated"
		}
	}
	if timeCol, err = need(timeName); err != nil {
		return acc.Fail(err)
	}
	cApp, haveApp := idx["apppool"]
	if haveApp {
		if cApp, err = need("AppPool"); err != nil {
			return acc.Fail(err)
		}
	}
	acc.Rep.Rule(timeName, rule)
	acc.Rep.Quality.TimezoneRule = rule

	// A local TimeCreated is written in the zone of the machine that ran the
	// export, which is not necessarily the zone of MachineName. The only zone
	// accepted is the one the user picked for the file; a zone that hosts.json
	// holds for some MachineName is never used for it. With no picked zone the
	// file is refused before any row is read.
	var exportTZ *time.Location
	if rule == clock.RuleHostLocal {
		if opts.Host == nil || opts.Host.TZ == nil {
			return acc.Fail(wincsv.TZUnlabelled())
		}
		exportTZ = opts.Host.TZ
		acc.Gap(GapLocalExporterZone)
	}

	allowed := m.AllowedAttrs()
	hosts := map[string]importer.HostHint{}
	onlyMachine, haveOnly := "", false // the one machine name allowed without hosts.json
	anyTime := false                   // a time cell has parsed
	sawTask := false
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
		if len(rec) != len(hdr) {
			acc.Reject(line, importer.ReasonFieldCount)
			continue
		}

		// Host: with hosts.json every MachineName must map through it, so an
		// unknown name refuses the file. Without hosts.json the picked host
		// stands for exactly one machine name (any letter case); a second
		// distinct name refuses the file. A name is never filed under the
		// picked host just because it is unmapped.
		machine := rec[cMach]
		hint, seen := hosts[machine]
		if !seen {
			if e := acc.Observe(machine); e != nil {
				return acc.Fail(e)
			}
			ok := false
			if opts.Hosts != nil {
				if machine != "" {
					hint, ok = opts.Hosts(machine)
				}
			} else if opts.Host != nil {
				key := strings.ToLower(machine)
				if !haveOnly {
					onlyMachine, haveOnly = key, true
				}
				if key == onlyMachine {
					hint, ok = *opts.Host, true
				}
			}
			if !ok {
				return acc.Fail(importer.ErrUnknownHost)
			}
			if len(hosts) < wincsv.MaxObservedHosts {
				hosts[machine] = hint
			}
		}

		var utc time.Time
		cell := strings.TrimSpace(rec[timeCol])
		if rule == clock.RuleUTCField {
			t, perr := time.Parse(time.RFC3339Nano, cell)
			if perr != nil || !(strings.HasSuffix(cell, "Z") || strings.HasSuffix(cell, "z")) {
				if !anyTime && perr != nil && wincsv.LooksLikeCultureTime(cell) {
					return failTimeFormat(acc, timeName)
				}
				acc.Reject(line, importer.ReasonBadTime)
				continue
			}
			utc = t.UTC()
		} else {
			var res clock.LocalResult
			var perr error
			for _, layout := range localLayouts {
				if res, perr = clock.ParseLocal(layout, cell, exportTZ); perr == nil {
					break
				}
			}
			switch {
			case perr != nil:
				if !anyTime && wincsv.LooksLikeCultureTime(cell) {
					return failTimeFormat(acc, timeName)
				}
				acc.Reject(line, importer.ReasonBadTime)
				continue
			case res.Ambiguous:
				acc.Reject(line, importer.ReasonDSTAmbiguous)
				continue
			case res.Nonexistent:
				acc.Reject(line, importer.ReasonDSTNonexistent)
				continue
			}
			utc = res.UTC
		}
		if !wincsv.SaneTime(utc) { // a zero or far-off date rejects its row only
			acc.Reject(line, importer.ReasonBadTime)
			continue
		}
		anyTime = true

		id, perr := strconv.ParseUint(strings.TrimSpace(rec[cID]), 10, 32)
		if perr != nil {
			acc.Reject(line, importer.ReasonBadNumber)
			continue
		}
		sev, ok := severity(rec[cLevel])
		if !ok {
			acc.Reject(line, importer.ReasonBadNumber)
			continue
		}

		provider, logName := rec[cProv], rec[cLog]
		if strings.EqualFold(logName, taskLog) {
			sawTask = true
		}
		attrs := map[string]string{
			"log":      wincsv.ClipAttr(logName),
			"provider": wincsv.ClipAttr(provider),
			"id":       strconv.FormatUint(id, 10),
		}
		if haveApp && rec[cApp] != "" {
			attrs["app-pool"] = wincsv.ClipAttr(rec[cApp])
		}
		kind := "win-event"
		for _, c := range m.cls {
			if k, ca, ok := c(provider, int(id)); ok {
				kind = k
				for key, v := range ca {
					if allowed[key] {
						attrs[key] = wincsv.ClipAttr(v)
					}
				}
				break
			}
		}

		if e := acc.CheckEmit(1); e != nil {
			return acc.Fail(e)
		}
		e := wincsv.NewEvent(opts, Name, line, 0)
		e.Kind, e.Host, e.Start, e.End = kind, hint.Label, utc, utc
		e.Severity, e.TZRule, e.Attrs = sev, rule, attrs
		if err := emit(e); err != nil {
			return acc.Fail(err)
		}
		acc.Emitted(utc)
	}
	if !sawTask {
		acc.Gap(GapNoTaskEvents)
	}
	return acc.Finish()
}

// failTimeFormat refuses a file whose first time cell is in the exporting
// machine's culture format. Nothing from the cell is kept: only the column name,
// which is one of this importer's two fixed names.
func failTimeFormat(acc *wincsv.Acc, column string) (importer.Report, error) {
	acc.Gap(GapTimeFormat)
	return acc.Fail(&importer.QuarantineError{Reason: importer.ReasonBadTime, Column: column})
}

// severity maps Get-WinEvent Level (number, or LevelDisplayName text) to an Event severity.
func severity(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "":
		return "", true
	case "1", "critical":
		return "critical", true
	case "2", "error":
		return "error", true
	case "3", "warning":
		return "warning", true
	case "0", "4", "5", "information", "informational", "verbose", "logalways":
		return "info", true
	}
	return "", false
}

// ClassifyTaskScheduler classifies Microsoft-Windows-TaskScheduler events by id.
// The ids follow the Task Scheduler operational log documentation and have not been
// checked against a real export; a sanitised sample is needed to call this validated.
// Kinds: task-start (100), task-complete (102), task-fail (101, 103, 203),
// task-skipped (322), task-stop (111 with no reason, 329 reason "timeout",
// 330 reason "user-request").
func ClassifyTaskScheduler(provider string, id int) (string, map[string]string, bool) {
	if !strings.EqualFold(provider, "Microsoft-Windows-TaskScheduler") {
		return "", nil, false
	}
	switch id {
	case 100:
		return "task-start", nil, true
	case 102:
		return "task-complete", nil, true
	case 101, 103, 203:
		return "task-fail", nil, true
	case 322:
		return "task-skipped", map[string]string{"reason": "instance-already-running"}, true
	case 111:
		return "task-stop", nil, true // terminated; the log gives no reason
	case 329:
		return "task-stop", map[string]string{"reason": "timeout"}, true
	case 330:
		return "task-stop", map[string]string{"reason": "user-request"}, true
	}
	return "", nil, false
}
