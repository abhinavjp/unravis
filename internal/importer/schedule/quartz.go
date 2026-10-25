package schedule

// Quartz.NET scheduler logs (importer "schedule-quartz").
//
// Quartz.NET writes through Common.Logging, Microsoft.Extensions.Logging,
// log4net or Serilog, so the file has the layout of whichever logger the host
// chose. Two text layouts are read:
//
//   - the log4net/Common.Logging pattern "%date [%thread] %level %logger - %message",
//     for example
//     "2001-03-04 05:06:07,123 [QuartzScheduler_Worker-1] INFO  Quartz.Core.JobRunShell - Job DEFAULT.nightly-sync execution complete";
//   - the Microsoft.Extensions.Logging console layout with a time stamp prefix
//     (TimestampFormat set): a header line "2001-03-04 05:06:07 info: Quartz.Core.JobRunShell[0]"
//     with the message on the following indented line (or on the header line
//     itself in single-line mode). A MEL console file with no time stamp prefix
//     carries no time and is refused.
//
// Any other layout is refused with a reason; a line that starts like an entry
// but is not a whole header is rejected, never guessed.
//
// Only lines whose logger is Quartz or starts with "Quartz." and whose message
// is a scheduler or job lifecycle line become events. The wordings below are
// from the public Quartz.NET source and documentation; no Quartz.NET was run
// for this card, so a version that words a line differently has that line
// skipped (it is not an error):
//
//	Job <group>.<name> execution complete [... in <n> ms]   job-run, outcome=complete
//	Job <group>.<name> execution failed                     job-run, outcome=failed
//	Job <group>.<name> threw a <Type>Exception              job-run, outcome=failed
//	Batch acquisition of <n> triggers                       trigger-batch (n above 0)
//	Trigger <group>.<name> misfired                         trigger-misfire
//	Handling <n> trigger(s) that missed their scheduled fire-time   trigger-misfire
//	Scheduler <name> started / shutdown                     scheduler-start / scheduler-stop
//	Failed to acquire next trigger, ClusterManager: Error managing cluster   scheduler-fault
//
// The hangfire and agent importers have the kinds schedule-boundary,
// schedule-unknown and job-run only. A Quartz log is what happened, so the run
// of a job keeps the job-run kind (with outcome and severity), and the other
// lifecycle lines get their own kinds.
//
// Time: the layouts carry no zone, so the user picks the host and the time is
// read in that host's zone (RuleHostLocal), exactly as the NLog and log4net
// importers do. No host zone refuses the file (tz-unlabelled); a time inside a
// clock change is rejected, never guessed. The owner may state that the times
// are UTC (supplied "log-time"="utc"). A job key is "group.name" and is split
// at its first dot.
//
// Privacy: message text, exception text and stack frames are never stored. What
// leaves a line is its time, the level, the job, group, trigger, scheduler and
// thread names (identifiers only: no control or format characters, and never
// starting with = + - @ so no spreadsheet formula survives), a stated duration,
// a count and the exception type names. Nothing from the file reaches an error,
// a report or a known gap.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/applog"
	"api-audit-explorer/internal/importer/textio"
	"api-audit-explorer/internal/importer/wincsv"
)

// Event kinds of the Quartz importer (job-run is shared with the others).
const (
	KindTriggerBatch   = "trigger-batch"
	KindTriggerMisfire = "trigger-misfire"
	KindSchedulerStart = "scheduler-start"
	KindSchedulerStop  = "scheduler-stop"
	KindSchedulerFault = "scheduler-fault"
)

// SuppliedLogTime is "utc" when the owner states that the log times are UTC.
const SuppliedLogTime = "log-time"

// Fixed known-gap sentences. Each passes evidence.LintCausal.
const (
	GapQuartzLocalTime = "Log times are host-local with no zone in the file; the host time zone chosen by the user is applied."
	GapQuartzUTC       = "Log times are read as UTC; the owner stated this and the file does not."
	GapQuartzPreamble  = "Lines before the first entry header were skipped."
	GapQuartzExcCap    = "Message or exception text beyond 64 KiB for one entry was ignored."
	GapQuartzScope     = "Only Quartz scheduler and job lifecycle lines are read (job complete or failed, batch acquisition, misfires, scheduler start and shutdown, trigger acquisition and cluster faults); other lines were skipped."
	GapQuartzWording   = "The Quartz message wordings are matched from public documentation and source, not from a real export; a line worded differently is skipped and counts as unknown."
	GapQuartzNoDur     = "A job run that states no duration is a point in time, not an interval."
	GapQuartzEmptyPoll = "Batch acquisitions of zero triggers are not recorded."
)

const (
	quartzMaxExcBytes = 64 << 10
	quartzCtxEvery    = 256
	quartzMaxHeadLine = 8192
	quartzMaxName     = 200
	quartzMaxDur      = 7 * 24 * time.Hour
	quartzTimeLayout  = "2006-01-02 15:04:05"
)

// quartzAttrs is the attribute allowlist of the Quartz importer (at most 16).
var quartzAttrs = map[string]bool{
	"job": true, "group": true, "trigger": true, "scheduler": true, "thread": true,
	"outcome": true, "level": true, "exception": true, "count": true,
}

var (
	// quartzLogRe is a whole log4net-layout header with its message.
	quartzLogRe = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}(?:[,.]\d{1,7})?) \[([^\]]*)\]\s+([A-Za-z]{3,11})\s+(\S+) -(?: (.*))?$`)
	// quartzMelRe is a MEL console header with a time stamp prefix. The message
	// is on the next indented line unless the header line carries it.
	quartzMelRe = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}:\d{2}(?:[,.]\d{1,7})?)\s+(trce|dbug|info|warn|fail|crit): (\S+?)\[(-?\d{1,9})\](?:\s+(.*))?$`)
	// quartzMelBareRe is a MEL header with no time stamp.
	quartzMelBareRe = regexp.MustCompile(`^(?:trce|dbug|info|warn|fail|crit): \S+?\[-?\d{1,9}\](?:\s.*)?$`)
	// quartzDatedRe is a line that starts like an entry, whatever follows.
	quartzDatedRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}:\d{2}`)

	reJobDone  = regexp.MustCompile(`^Job (.+?) execution complete(?:d)?\b(.*)$`)
	reJobFail  = regexp.MustCompile(`^Job (.+?) execution failed\b`)
	reJobThrew = regexp.MustCompile(`^Job (.+?) threw (?:an? )?(?:unhandled )?([A-Za-z0-9_.]*Exception)\b`)
	reBatch    = regexp.MustCompile(`^Batch acquisition of (\d{1,9}) triggers?\b`)
	reMisTrig  = regexp.MustCompile(`^Trigger (.+?) misfired\b`)
	reMisN     = regexp.MustCompile(`^Handling (\d{1,9}) trigger\(s\) that missed their scheduled fire-time`)
	reSched    = regexp.MustCompile(`^Scheduler (.+?) (started|shutdown)\b`)
	reFault    = regexp.MustCompile(`^(?:Failed to acquire next trigger|An error occurred while scanning for the next trigger to fire|ClusterManager: Error managing cluster)\b`)

	reDurUnit = regexp.MustCompile(`(?i)\b(?:in|took|duration|elapsed)[:= ]+(\d{1,9}(?:\.\d{1,6})?)\s*(milliseconds?|ms|seconds?|secs?|s)\b`)
	reDurSpan = regexp.MustCompile(`(?i)\b(?:in|took|duration|elapsed)[:= ]+(\d{1,2}):(\d{2}):(\d{2})(?:\.(\d{1,7}))?\b`)

	// reExcType is a .NET type name that starts an exception line.
	reExcType = regexp.MustCompile("^(?:Unhandled exception\\. )?((?:[A-Za-z_][A-Za-z0-9_]*\\.)*[A-Za-z_][A-Za-z0-9_`+]*(?:Exception|Error))(?::|\\s\\(|$)")
)

// quartzExceptions are the Quartz exception types the importer stores by name.
// Short names (as the message text writes them) map to the full name.
var quartzExceptions = map[string]string{
	"Quartz.JobExecutionException":                    "Quartz.JobExecutionException",
	"Quartz.SchedulerException":                       "Quartz.SchedulerException",
	"Quartz.JobPersistenceException":                  "Quartz.JobPersistenceException",
	"Quartz.ObjectAlreadyExistsException":             "Quartz.ObjectAlreadyExistsException",
	"Quartz.SchedulerConfigException":                 "Quartz.SchedulerConfigException",
	"Quartz.UnableToInterruptJobException":            "Quartz.UnableToInterruptJobException",
	"Quartz.Impl.AdoJobStore.LockException":           "Quartz.Impl.AdoJobStore.LockException",
	"Quartz.Impl.AdoJobStore.NoRecordFoundException":  "Quartz.Impl.AdoJobStore.NoRecordFoundException",
	"JobExecutionException":                           "Quartz.JobExecutionException",
	"SchedulerException":                              "Quartz.SchedulerException",
	"JobPersistenceException":                         "Quartz.JobPersistenceException",
	"ObjectAlreadyExistsException":                    "Quartz.ObjectAlreadyExistsException",
	"SchedulerConfigException":                        "Quartz.SchedulerConfigException",
}

// QuartzExceptionNames lists the Quartz exception type names the importer can
// store, for the case export's enum.
func QuartzExceptionNames() []string {
	seen := map[string]bool{}
	var out []string
	for _, full := range quartzExceptions {
		if !seen[full] {
			seen[full] = true
			out = append(out, full)
		}
	}
	return out
}

// Quartz is the importer for Quartz.NET text logs.
type Quartz struct{}

// NewQuartz returns the Quartz.NET log importer.
func NewQuartz() *Quartz { return &Quartz{} }

func (*Quartz) Name() string { return NameQuartz }

func (*Quartz) AllowedAttrs() map[string]bool {
	m := make(map[string]bool, len(quartzAttrs))
	for k, v := range quartzAttrs {
		m[k] = v
	}
	return m
}

func (*Quartz) DefaultLimits() importer.Limits {
	return importer.Limits{
		MaxBytes: 2 << 30, MaxRows: 10_000_000, MaxEvents: 2_000_000,
		MaxLineBytes: 64 << 10, MaxFields: wincsv.DefaultMaxFields,
		MaxRejectFraction: wincsv.DefaultRejectFrac,
	}
}

// SuppliedKeys declares the owner's statement that the log times are UTC.
func (*Quartz) SuppliedKeys() map[string]importer.SuppliedSpec {
	return map[string]importer.SuppliedSpec{
		SuppliedLogTime: {Kind: importer.SuppliedEnum, Values: []string{"utc"}},
	}
}

func quartzLogger(name string) bool { return name == "Quartz" || strings.HasPrefix(name, "Quartz.") }

// quartzKind says what a message is.
type quartzKind int

const (
	qNone quartzKind = iota
	qJobDone
	qJobFail
	qJobThrew
	qBatch
	qMisTrig
	qMisN
	qSchedStart
	qSchedStop
	qFault
)

// quartzMsg is a classified message: the kind and its raw captures.
type quartzMsg struct {
	kind quartzKind
	key  string // job or trigger key, or scheduler name
	num  string // batch or misfire count
	exc  string // exception name written in the message
	rest string // text after "execution complete"
}

func classifyQuartz(msg string) quartzMsg {
	if len(msg) < 5 {
		return quartzMsg{}
	}
	switch msg[0] {
	case 'J':
		if m := reJobDone.FindStringSubmatch(msg); m != nil {
			return quartzMsg{kind: qJobDone, key: m[1], rest: m[2]}
		}
		if m := reJobFail.FindStringSubmatch(msg); m != nil {
			return quartzMsg{kind: qJobFail, key: m[1]}
		}
		if m := reJobThrew.FindStringSubmatch(msg); m != nil {
			return quartzMsg{kind: qJobThrew, key: m[1], exc: m[2]}
		}
	case 'B':
		if m := reBatch.FindStringSubmatch(msg); m != nil {
			return quartzMsg{kind: qBatch, num: m[1]}
		}
	case 'T':
		if m := reMisTrig.FindStringSubmatch(msg); m != nil {
			return quartzMsg{kind: qMisTrig, key: m[1]}
		}
	case 'H':
		if m := reMisN.FindStringSubmatch(msg); m != nil {
			return quartzMsg{kind: qMisN, num: m[1]}
		}
	case 'S':
		if m := reSched.FindStringSubmatch(msg); m != nil {
			k := qSchedStart
			if m[2] == "shutdown" {
				k = qSchedStop
			}
			return quartzMsg{kind: k, key: m[1]}
		}
	case 'F', 'A', 'C':
		if reFault.MatchString(msg) {
			return quartzMsg{kind: qFault}
		}
	}
	return quartzMsg{}
}

// Detect scores 97 when the head holds a Quartz lifecycle line in a layout read
// here and Quartz lines are at least half of the entry headers. The score is
// above log4net-text and mel-console (85) by more than the tie window, so a
// Quartz log is not offered as a tie with them.
func (*Quartz) Detect(head []byte, _ string) int {
	s := strings.TrimLeft(wincsv.HeadText(head), " \t\r\n")
	if s == "" || s[0] == '<' {
		return 0
	}
	var q, h int
	life, melWait := false, false
	for i, line := range strings.SplitN(s, "\n", 80) {
		if i >= 79 {
			break
		}
		line = strings.TrimRight(line, "\r")
		if len(line) > quartzMaxHeadLine {
			melWait = false
			continue
		}
		if melWait {
			melWait = false
			if strings.HasPrefix(line, "      ") {
				msg := strings.TrimSpace(line)
				if strings.HasPrefix(msg, "=> ") {
					melWait = true
					continue
				}
				if classifyQuartz(msg).kind != qNone {
					life = true
				}
				continue
			}
		}
		if line == "" || line[0] < '0' || line[0] > '9' {
			continue
		}
		if m := quartzLogRe.FindStringSubmatch(line); m != nil {
			h++
			if quartzLogger(m[4]) {
				q++
				if classifyQuartz(strings.TrimSpace(m[5])).kind != qNone {
					life = true
				}
			}
		} else if m := quartzMelRe.FindStringSubmatch(line); m != nil {
			h++
			if quartzLogger(m[3]) {
				q++
				if m[5] != "" {
					if classifyQuartz(strings.TrimSpace(m[5])).kind != qNone {
						life = true
					}
				} else {
					melWait = true
				}
			}
		}
	}
	if q > 0 && life && q*2 >= h {
		return 97
	}
	return 0
}

// qpend is the entry being read.
type qpend struct {
	on      bool
	line    int64
	t       time.Time
	level   string // as written
	sev     string
	thread  string
	mel     bool
	haveMsg bool
	msg     string
	exc     []byte
	capped  bool
}

func logTimeIsUTC(opts importer.Options) bool {
	return strings.EqualFold(strings.TrimSpace(opts.Supplied[SuppliedLogTime]), "utc")
}

// quartzWhen reads a header time as UTC under rule.
func quartzWhen(val, rule string, loc *time.Location) (time.Time, importer.Reason) {
	val = strings.NewReplacer(",", ".", "T", " ").Replace(val)
	var t time.Time
	if rule == clock.RuleHostLocal {
		var why importer.Reason
		if t, why = applog.LocalTime(quartzTimeLayout, val, loc); why != "" {
			return time.Time{}, why
		}
	} else {
		var err error
		if t, err = time.Parse(quartzTimeLayout, val); err != nil {
			return time.Time{}, importer.ReasonBadTime
		}
		t = t.UTC()
	}
	if !wincsv.SaneTime(t) {
		return time.Time{}, importer.ReasonBadTime
	}
	return t, ""
}

// quartzNotHeader says why a line that starts with a time stamp is not a header.
func quartzNotHeader(text string) importer.Reason {
	if len(text) >= len(quartzTimeLayout) {
		v := strings.Replace(text[:len(quartzTimeLayout)], "T", " ", 1)
		if _, err := time.Parse(quartzTimeLayout, v); err != nil {
			return importer.ReasonBadTime
		}
	}
	return importer.ReasonFieldCount
}

// okName reports whether s is an identifier safe to keep as an attribute: not
// empty, valid UTF-8, no control or format characters, no trailing space, and a
// first character that is a letter, a digit or one of _ $ # : (so it can never
// start a spreadsheet formula).
func okName(s string) bool {
	if s == "" || len(s) > quartzMaxName || !utf8.ValidString(s) || strings.HasSuffix(s, " ") {
		return false
	}
	for i, r := range s {
		if i == 0 {
			if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '$' || r == '#' || r == ':') {
				return false
			}
			continue
		}
		if r != ' ' && !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}

// splitKey splits a Quartz key "group.name" at its first dot. ok is false when
// a part is not an identifier.
func splitKey(key string) (group, name string, ok bool) {
	if i := strings.IndexByte(key, '.'); i >= 0 {
		group, name = key[:i], key[i+1:]
		return group, name, okName(group) && okName(name)
	}
	return "", key, okName(key)
}

func sevRank(s string) int {
	switch s {
	case "info":
		return 1
	case "warning":
		return 2
	case "error":
		return 3
	case "critical":
		return 4
	}
	return 0
}

func sevAtLeast(sev, min string) string {
	if sevRank(sev) >= sevRank(min) {
		return sev
	}
	return min
}

// quartzDuration reads a duration the "execution complete" text states.
func quartzDuration(rest string) (time.Duration, bool) {
	if m := reDurUnit.FindStringSubmatch(rest); m != nil {
		v, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			return 0, false
		}
		unit := strings.ToLower(m[2])
		var d time.Duration
		if strings.HasPrefix(unit, "milli") || unit == "ms" {
			d = time.Duration(v * float64(time.Millisecond))
		} else {
			d = time.Duration(v * float64(time.Second))
		}
		if d < 0 || d > quartzMaxDur {
			return 0, false
		}
		return d, true
	}
	if m := reDurSpan.FindStringSubmatch(rest); m != nil {
		h, _ := strconv.Atoi(m[1])
		mi, _ := strconv.Atoi(m[2])
		se, _ := strconv.Atoi(m[3])
		if mi > 59 || se > 59 {
			return 0, false
		}
		d := time.Duration(h)*time.Hour + time.Duration(mi)*time.Minute + time.Duration(se)*time.Second
		if f := m[4]; f != "" {
			frac, _ := strconv.ParseFloat("0."+f, 64)
			d += time.Duration(frac * float64(time.Second))
		}
		if d > quartzMaxDur {
			return 0, false
		}
		return d, true
	}
	return 0, false
}

// excLabel is the stored name of one raw exception type.
func excLabel(raw string) string {
	if full, ok := quartzExceptions[raw]; ok {
		return full
	}
	if t := applog.ExceptionTypes(raw); len(t) > 0 {
		return t[0]
	}
	return applog.AppException
}

// exceptionTypes returns the outer and the innermost exception type of the
// continuation text, or the type the message names when the text has none.
func exceptionNames(exc, msgType string) []string {
	var raws []string
	outer := false
	lines := 0
	for rest := exc; rest != "" && lines < 2000; lines++ {
		var line string
		line, rest, _ = strings.Cut(rest, "\n")
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "at ") {
			continue
		}
		for i, seg := range strings.Split(line, "--->") {
			seg = strings.TrimSpace(seg)
			if i == 0 && outer {
				continue
			}
			if m := reExcType.FindStringSubmatch(seg); m != nil {
				raws = append(raws, m[1])
				outer = true
			}
		}
	}
	if len(raws) == 0 {
		if msgType == "" {
			return nil
		}
		raws = []string{msgType}
	}
	out := []string{excLabel(raws[0])}
	if len(raws) > 1 {
		if in := excLabel(raws[len(raws)-1]); in != out[0] {
			out = append(out, in)
		}
	}
	return out
}

func (m *Quartz) Parse(ctx context.Context, r io.Reader, opts importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
	lim := wincsv.MergeLimits(opts.Limits, m.DefaultLimits())
	opts.Limits = lim
	acc := wincsv.NewAcc(NameQuartz, lim)
	if err := ctx.Err(); err != nil {
		return acc.Fail(err)
	}
	pr, err := wincsv.Prepare(r, lim)
	if err != nil {
		return acc.Fail(err)
	}
	lr, _, err := textio.NewLineReader(pr, lim.MaxLineBytes)
	if err != nil {
		return acc.Fail(err)
	}
	// The lines carry no host name: they belong to the host the user picked.
	if opts.Host == nil {
		return acc.Fail(importer.ErrUnknownHost)
	}

	var (
		rule      string
		pd        qpend
		seen      bool
		started   bool
		recog     int64
		noDur     bool
		emptyPoll bool
	)
	inWindow := func(a, b time.Time) bool {
		w := opts.Window
		return (w.From.IsZero() || !b.Before(w.From)) && (w.To.IsZero() || !a.After(w.To))
	}
	send := func(line int64, kind string, from, to time.Time, dur *float64, sev string, a map[string]string) error {
		if !inWindow(from, to) {
			return nil
		}
		ev := wincsv.NewEvent(opts, NameQuartz, line, 0)
		ev.Kind, ev.Host = kind, opts.Host.Label
		ev.Start, ev.End = from.UTC(), to.UTC()
		ev.DurationMs, ev.Severity, ev.TZRule, ev.Attrs = dur, sev, rule, a
		if err := ev.Validate(quartzAttrs); err != nil {
			return fmt.Errorf("schedule: invalid event: %w", err)
		}
		if err := acc.CheckEmit(1); err != nil {
			return err
		}
		if err := emit(ev); err != nil {
			return err
		}
		acc.Emitted(ev.End)
		acc.Rep.Cover(ev.Start)
		return nil
	}
	start := func() error {
		rule = clock.RuleHostLocal
		if logTimeIsUTC(opts) {
			rule = clock.RuleUTCField
			acc.Gap(GapQuartzUTC)
		} else {
			if opts.Host.TZ == nil {
				return wincsv.TZUnlabelled()
			}
			acc.Gap(GapQuartzLocalTime)
		}
		acc.Rep.Quality.TimezoneRule = rule
		acc.Rep.Quality.SourceVersion = opts.Host.Runtime
		acc.Rep.FieldRules["timestamp"] = rule
		acc.Gap(GapQuartzScope)
		acc.Gap(GapQuartzWording)
		started = true
		return nil
	}
	// flush classifies the finished entry and emits its event.
	flush := func() error {
		if !pd.on {
			return nil
		}
		p := pd
		pd = qpend{exc: pd.exc[:0]}
		cm := classifyQuartz(strings.TrimSpace(p.msg))
		if cm.kind == qNone {
			return nil
		}
		base := map[string]string{"level": wincsv.ClipAttr(p.level)}
		if okName(p.thread) {
			base["thread"] = p.thread
		}
		reject := func() {
			acc.Reject(p.line, importer.ReasonFieldCount)
		}
		switch cm.kind {
		case qJobDone, qJobFail, qJobThrew:
			group, name, ok := splitKey(cm.key)
			if !ok {
				reject()
				return nil
			}
			recog++
			base["job"] = name
			if group != "" {
				base["group"] = group
			}
			sev := p.sev
			from, to := p.t, p.t
			var dur *float64
			if cm.kind == qJobDone {
				base["outcome"] = "complete"
				if d, ok := quartzDuration(cm.rest); ok && wincsv.SaneTime(p.t.Add(-d)) {
					ms := float64(d) / float64(time.Millisecond)
					dur = &ms
					from = p.t.Add(-d)
				} else {
					noDur = true
				}
			} else {
				base["outcome"] = "failed"
				sev = sevAtLeast(sev, "error")
				noDur = true
			}
			if names := exceptionNames(string(p.exc), quartzExceptions[cm.exc]); len(names) > 0 {
				base["exception"] = strings.Join(names, ",")
			}
			return send(p.line, KindJobRun, from, to, dur, sev, base)
		case qBatch:
			recog++
			n, _ := strconv.Atoi(cm.num)
			if n == 0 {
				emptyPoll = true
				return nil
			}
			base["count"] = cm.num
			return send(p.line, KindTriggerBatch, p.t, p.t, nil, p.sev, base)
		case qMisTrig:
			group, name, ok := splitKey(cm.key)
			if !ok {
				reject()
				return nil
			}
			recog++
			base["trigger"] = name
			if group != "" {
				base["group"] = group
			}
			return send(p.line, KindTriggerMisfire, p.t, p.t, nil, sevAtLeast(p.sev, "warning"), base)
		case qMisN:
			recog++
			base["count"] = cm.num
			return send(p.line, KindTriggerMisfire, p.t, p.t, nil, sevAtLeast(p.sev, "warning"), base)
		case qSchedStart, qSchedStop:
			if !okName(cm.key) {
				reject()
				return nil
			}
			recog++
			base["scheduler"] = cm.key
			kind := KindSchedulerStart
			if cm.kind == qSchedStop {
				kind = KindSchedulerStop
			}
			return send(p.line, kind, p.t, p.t, nil, p.sev, base)
		case qFault:
			recog++
			if names := exceptionNames(string(p.exc), ""); len(names) > 0 {
				base["exception"] = strings.Join(names, ",")
			}
			return send(p.line, KindSchedulerFault, p.t, p.t, nil, sevAtLeast(p.sev, "error"), base)
		}
		return nil
	}
	addExc := func(s string) {
		if pd.capped {
			return
		}
		if len(pd.exc)+len(s)+1 > quartzMaxExcBytes {
			pd.capped = true
			acc.Gap(GapQuartzExcCap)
			return
		}
		if len(pd.exc) > 0 {
			pd.exc = append(pd.exc, '\n')
		}
		pd.exc = append(pd.exc, s...)
	}

	for n := 0; ; n++ {
		if n%quartzCtxEvery == 0 {
			if err := ctx.Err(); err != nil {
				return acc.Fail(err)
			}
		}
		line, no, err := lr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			reason, ok := wincsv.RowReason(err)
			if !ok {
				return acc.Fail(err)
			}
			if e := acc.Row(no); e != nil {
				return acc.Fail(e)
			}
			acc.Reject(no, reason)
			continue
		}
		seen = true
		text := string(line)
		var logM, melM []string
		if text != "" && text[0] >= '0' && text[0] <= '9' {
			if logM = quartzLogRe.FindStringSubmatch(text); logM == nil {
				melM = quartzMelRe.FindStringSubmatch(text)
			}
		}
		dated := logM != nil || melM != nil || (text != "" && quartzDatedRe.MatchString(text)) ||
			(started && quartzMelBareRe.MatchString(text))
		if !started && !dated {
			if strings.TrimSpace(text) != "" {
				acc.Gap(GapQuartzPreamble)
			}
			continue
		}
		if e := acc.Row(no); e != nil { // every line from the first header on is a row
			return acc.Fail(e)
		}
		if !dated { // continuation line
			if !pd.on {
				continue
			}
			if pd.mel && !pd.haveMsg {
				msg := strings.TrimSpace(text)
				if strings.HasPrefix(msg, "=> ") {
					continue // a logging scope line
				}
				pd.msg, pd.haveMsg = msg, true
				continue
			}
			addExc(text)
			continue
		}
		if e := flush(); e != nil {
			return acc.Fail(e)
		}
		if logM == nil && melM == nil { // starts like an entry but is not a whole header
			if started && quartzMelBareRe.MatchString(text) {
				acc.Reject(no, importer.ReasonBadTime) // a MEL header with no time stamp
			} else {
				acc.Reject(no, quartzNotHeader(text))
			}
			continue
		}
		var (
			val, levelWord, logger, thread, msg string
			mel                                 bool
		)
		if logM != nil {
			val, thread, levelWord, logger, msg = logM[1], logM[2], logM[3], logM[4], logM[5]
		} else {
			val, levelWord, logger, msg, mel = melM[1], melM[2], melM[3], melM[5], true
		}
		stored, sev, ok := applog.Level(levelWord)
		if !ok {
			acc.Reject(no, importer.ReasonFieldCount)
			continue
		}
		if !started {
			if e := start(); e != nil {
				return acc.Fail(e)
			}
		}
		t, why := quartzWhen(val, rule, opts.Host.TZ)
		if why != "" {
			acc.Reject(no, why)
			continue
		}
		acc.Rep.Cover(t)
		if !quartzLogger(logger) {
			continue
		}
		pd = qpend{on: true, line: no, t: t, level: stored, sev: sev, thread: thread, mel: mel,
			msg: msg, haveMsg: !mel || strings.TrimSpace(msg) != "", exc: pd.exc[:0]}
	}
	if !started {
		if !seen {
			return acc.Fail(textio.ErrEmpty)
		}
		return acc.Fail(&importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat})
	}
	if e := flush(); e != nil {
		return acc.Fail(e)
	}
	if recog == 0 {
		// A layout this importer reads, but no Quartz lifecycle line in it.
		return acc.Fail(&importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat})
	}
	if noDur {
		acc.Gap(GapQuartzNoDur)
	}
	if emptyPoll {
		acc.Gap(GapQuartzEmptyPoll)
	}
	return acc.Finish()
}

var _ importer.Importer = (*Quartz)(nil)
