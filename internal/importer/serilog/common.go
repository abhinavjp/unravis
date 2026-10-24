// Package serilog imports Serilog logs in two shapes: compact JSON lines (CLEF,
// "serilog-clef") and the plain text of the default file template
// ("serilog-text"). Both are framers for the shared application-log core
// (internal/importer/applog): they read one line, build an in-memory
// applog.Record and hand it to the core, which classifies it, emits the events
// and discards the record.
//
// Formats, from the public Serilog documentation (verify against the sink
// versions in use; no sink was run for this card):
//
//   - CLEF (Serilog.Formatting.Compact, https://github.com/serilog/serilog-formatting-compact):
//     one JSON object per line with "@t" (ISO 8601 with an offset or "Z"), "@mt"
//     (message template), "@m" (rendered message, only the rendered formatter
//     writes it), "@l" (level name, absent for Information), "@x" (exception
//     text), "@i" (event id hash), "@r" (renderings) and one member per
//     property. A member whose name starts with "@" is written "@@name".
//   - Text, default file sink template (https://github.com/serilog/serilog-sinks-file):
//     "{Timestamp:yyyy-MM-dd HH:mm:ss.fff zzz} [{Level:u3}] {Message:lj}{NewLine}{Exception}".
//     A record is one header line followed by the lines of a multi-line message
//     or exception. The console template ("[HH:mm:ss INF] ...") has no date and is
//     refused.
//
// Time: both formats carry the offset of the machine that wrote them, so the
// time is converted by the offset in the field (RuleOffsetInField), or by the
// UTC marker "Z" (RuleUTCField). A CLEF time with no offset is rejected
// (tz-unlabelled): the host zone is not guessed.
//
// Privacy: message text, templates, rendered messages, properties, event ids,
// stack frames and paths are never stored. What leaves a line is its time, its
// level, its logger name (only as the core's label), the curated exception
// types the core finds in the exception text, the family the core derives and
// the numbers of a request-finished line (status, method, duration). Nothing
// from the file reaches an error, a report or a known gap.
package serilog

import (
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/wincsv"
)

// Importer names.
const (
	NameCLEF = "serilog-clef"
	NameText = "serilog-text"
)

// Fixed known-gap sentences. Each passes evidence.LintCausal.
const (
	GapTemplateOnly = "Some compact JSON lines hold the message template and not the rendered message; " +
		"entries are classified by logger name and exception type only, and numbers inside the message are not read."
	GapNoLogger = "The default Serilog text template holds no logger name; entries are classified by message sentence " +
		"and exception type only, application start and stop lines are not recognised, and the logger attribute is not set."
	GapOffsets = "The file mixes lines written with a UTC marker and lines written with an offset; each line is converted by its own marker."
)

// maxExceptionBytes bounds the exception text one text record keeps in memory.
const maxExceptionBytes = 128 << 10

// saneTime reports whether t lies in the range wincsv.SaneTime accepts.
func saneTime(t time.Time) bool { return wincsv.SaneTime(t) }

// ruleTracker folds the per-line time rules of a file into the one value the
// report's quality block can hold.
type ruleTracker struct {
	first, rule string
	mixed       bool
}

func (r *ruleTracker) see(rule string) {
	switch {
	case r.first == "":
		r.first, r.rule = rule, rule
	case rule != r.first:
		r.mixed = true
		r.rule = clock.RuleOffsetInField // an offset of zero is an offset: the weaker claim covers both
	}
}

// finish writes the observed rule into rep.
func (r *ruleTracker) finish(rep *importer.Report, field string) {
	if r.rule == "" {
		return
	}
	rep.Quality.TimezoneRule = r.rule
	rep.Rule(field, r.rule)
	if r.mixed {
		rep.Gap(GapOffsets)
	}
}

// finishReport seals rep after the sink has flushed: coverage into the quality
// block and the reject-fraction rule.
func finishReport(rep *importer.Report, lim importer.Limits, host *importer.HostHint) error {
	if host != nil {
		rep.Quality.SourceVersion = host.Runtime
	}
	if !rep.First.IsZero() {
		f, l := rep.First, rep.Last
		rep.Quality.CoverageFirst, rep.Quality.CoverageLast = &f, &l
	}
	return lim.CheckRejects(rep.Rows, rep.Rejected)
}
