package schedule

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

// Agent is the importer for a SQL Agent schedule export (sysjobs joined to
// sysschedules, optionally to sysjobhistory).
//
// Columns: job_name (or name), enabled, freq_type, freq_interval,
// freq_subday_type, freq_subday_interval, freq_relative_interval,
// freq_recurrence_factor, active_start_date, active_end_date,
// active_start_time, active_end_time; optional history run_date, run_time,
// run_duration (HHMMSS). All times are server-local, so the file is refused
// without a host time zone. The export is SQL-sourced: ruling R12 (DBA dry run)
// gates real files, and the import screen says so.
type Agent struct{}

// NewAgent returns the SQL Agent schedule importer.
func NewAgent() *Agent { return &Agent{} }

// Columns that carry job text, commands, owners or messages quarantine the file.
var agentUnscrubbed = []string{
	"command", "message", "step_name", "database_name", "output_file_name",
	"owner", "owner_sid", "owner_login_name", "proxy_name", "description",
}

// schedCols is the complete group needed to expand a schedule.
var schedCols = []string{
	"enabled", "freq_type", "freq_interval", "freq_subday_type", "freq_subday_interval",
	"active_start_date", "active_start_time",
}

var allSchedCols = []string{
	"enabled", "freq_type", "freq_interval", "freq_subday_type", "freq_subday_interval",
	"freq_relative_interval", "freq_recurrence_factor",
	"active_start_date", "active_end_date", "active_start_time", "active_end_time",
}

func (*Agent) Name() string                   { return NameAgent }
func (*Agent) AllowedAttrs() map[string]bool  { return copyAttrs() }
func (*Agent) DefaultLimits() importer.Limits { return defaultLimits() }

// Detect scores a file by its header row only.
func (*Agent) Detect(head []byte, fileName string) int {
	cells := headerCells(decodeHead(head))
	if (cells["job_name"] || cells["name"]) && cells["freq_type"] {
		return 90
	}
	if (cells["job_name"] || cells["name"]) && cells["run_date"] && cells["run_time"] {
		return 60
	}
	return 0
}

// Parse implements importer.Importer.
func (a *Agent) Parse(ctx context.Context, rd io.Reader, opts importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
	r := newRun(ctx, NameAgent, opts, emit)
	cr, tb, err := openCSV(rd, r.lim)
	if err != nil {
		return r.rep, err
	}
	if err := tb.refuseColumns(agentUnscrubbed); err != nil {
		return r.rep, err
	}
	jobCol := "job_name"
	if !tb.has(jobCol) {
		jobCol = "name"
	}
	if err := tb.require(jobCol); err != nil {
		return r.rep, &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat, Column: "job_name"}
	}
	hasSched := tb.has("freq_type")
	hasHist := tb.has("run_date") || tb.has("run_time")
	if !hasSched && !hasHist {
		return r.rep, &importer.QuarantineError{Reason: importer.ReasonUnsupportedFormat, Column: "freq_type"}
	}
	if hasSched {
		if err := tb.require(schedCols...); err != nil {
			return r.rep, err
		}
	}
	if hasHist {
		if err := tb.require("run_date", "run_time"); err != nil {
			return r.rep, err
		}
	}
	// Server-local times: a host time zone is required, never guessed.
	if opts.Host == nil || opts.Host.TZ == nil {
		return r.rep, importer.ErrTimezoneUnlabelled
	}
	loc := opts.Host.TZ
	for _, f := range []string{"active_start_time", "active_end_time", "run_date", "run_time"} {
		if tb.has(f) {
			r.rep.FieldRules[f] = clock.RuleHostLocal
		}
	}
	seen := map[string]bool{}
	for {
		if err := ctx.Err(); err != nil {
			return r.rep, err
		}
		rec, line, reason, err := nextRow(cr, tb.n)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return r.rep, err
		}
		r.rep.Rows++
		if r.rep.Rows > r.lim.MaxRows {
			return r.rep, &importer.LimitError{Limit: "rows", Max: r.lim.MaxRows, At: r.rep.Rows}
		}
		if reason != "" {
			r.reject(line, reason)
			continue
		}
		if err := a.row(r, tb, rec, line, jobCol, hasSched, loc, seen); err != nil {
			return r.rep, err
		}
	}
	gaps := []string{gapAgentSnap, gapAgentHostLocal}
	if hasHist {
		gaps = append(gaps, gapAgentHistory)
	}
	return r.finish(clock.RuleHostLocal, gaps)
}

// row validates the whole record first, so a rejected row emits nothing.
func (a *Agent) row(r *run, tb *table, rec []string, line int64, jobCol string, hasSched bool, loc *time.Location, seen map[string]bool) error {
	job := tb.get(rec, jobCol)
	if job == "" {
		r.reject(line, importer.ReasonFieldCount)
		return nil
	}
	job = cleanAttr(job)

	// History half.
	var runStart, runEnd time.Time
	var runDur *float64
	hist := false
	if tb.has("run_date") {
		rd, rt := nullable(tb.get(rec, "run_date")), nullable(tb.get(rec, "run_time"))
		if (rd != "" && rd != "0") || (rt != "" && rt != "0") {
			durCell := nullable(tb.get(rec, "run_duration"))
			start, dur, reason := parseHistory(rd, rt, durCell, loc)
			if reason != "" {
				r.reject(line, reason)
				return nil
			}
			runStart, runEnd, hist = start, start.Add(dur), true
			if durCell != "" { // an absent duration stays unknown, never zero
				runDur = msOf(dur)
			}
		}
	}

	// Schedule half.
	var sc *agentSched
	var dedupe string
	unknownReason := ""
	disabled := false
	if hasSched && nullable(tb.get(rec, "freq_type")) != "" {
		parsed, reason, unk := parseAgentSched(tb, rec)
		if reason != "" {
			r.reject(line, reason)
			return nil
		}
		unknownReason = unk
		sc = parsed
		var key strings.Builder
		key.WriteString(job)
		for _, c := range allSchedCols {
			key.WriteByte(0)
			key.WriteString(tb.get(rec, c))
		}
		dedupe = key.String()
		disabled = parsed.disabled
	}

	if hist && r.inWindow(runStart) {
		if err := r.send(line, 0, KindJobRun, runStart, runEnd, runDur, "info", clock.RuleHostLocal,
			map[string]string{"job": job, "basis": "history"}); err != nil {
			return err
		}
	}
	if sc == nil || seen[dedupe] {
		return nil
	}
	seen[dedupe] = true
	ord := 1
	switch {
	case disabled:
		return r.sendUnknown(line, ord, job, reasonDisabled, clock.RuleHostLocal)
	case unknownReason != "":
		return r.sendUnknown(line, ord, job, unknownReason, clock.RuleHostLocal)
	}
	return a.expand(r, sc, loc, line, &ord, job)
}

func nullable(s string) string {
	if strings.EqualFold(s, "null") {
		return ""
	}
	return s
}

func msOf(d time.Duration) *float64 {
	v := float64(d) / float64(time.Millisecond)
	return &v
}

// ---------------------------------------------------------------- schedule

// SQL Agent freq_type values (sysschedules).
const (
	freqOnce       = 1
	freqDaily      = 4
	freqWeekly     = 8
	freqMonthly    = 16
	freqMonthlyRel = 32
	freqAgentStart = 64
	freqIdle       = 128
)

var freqNames = map[int]string{
	freqOnce: "once", freqDaily: "daily", freqWeekly: "weekly",
	freqMonthly: "monthly", freqMonthlyRel: "monthly-relative",
}

type agentSched struct {
	freq, interval, subType, subInt, relInt, recur int
	startDate, endDate                             time.Time // civil dates as UTC midnights
	startSec, endSec                               int       // seconds of day
	disabled                                       bool
}

// parseAgentSched validates the schedule columns. It returns a reject reason,
// or an unknown reason for freq_type 64 and 128 (not clock based).
func parseAgentSched(tb *table, rec []string) (*agentSched, importer.Reason, string) {
	var s agentSched
	ok := true
	num := func(col string, def int) int {
		v := nullable(tb.get(rec, col))
		if v == "" {
			return def
		}
		n, good := smallInt(v)
		if !good {
			ok = false
		}
		return n
	}
	switch strings.ToLower(nullable(tb.get(rec, "enabled"))) {
	case "1", "true":
	case "0", "false":
		s.disabled = true
	default:
		return nil, importer.ReasonBadNumber, ""
	}
	s.freq = num("freq_type", -1)
	s.interval = num("freq_interval", 0)
	s.subType = num("freq_subday_type", 1)
	s.subInt = num("freq_subday_interval", 0)
	s.relInt = num("freq_relative_interval", 0)
	s.recur = num("freq_recurrence_factor", 0)
	if !ok {
		return nil, importer.ReasonBadNumber, ""
	}
	switch s.freq {
	case freqOnce, freqDaily, freqWeekly, freqMonthly, freqMonthlyRel, freqAgentStart, freqIdle:
	default:
		return nil, importer.ReasonBadNumber, "" // an unknown freq_type rejects the row
	}
	if s.freq == freqAgentStart || s.freq == freqIdle {
		return &s, "", reasonFreq
	}
	switch s.subType {
	case 1:
	case 2, 4, 8:
		if s.subInt < 1 {
			return nil, importer.ReasonBadNumber, ""
		}
	default:
		return nil, importer.ReasonBadNumber, ""
	}
	switch s.freq {
	case freqDaily:
		if s.interval < 1 {
			return nil, importer.ReasonBadNumber, ""
		}
	case freqWeekly:
		if s.interval < 1 || s.interval > 127 || s.recur < 1 {
			return nil, importer.ReasonBadNumber, ""
		}
	case freqMonthly:
		if s.interval < 1 || s.interval > 31 || s.recur < 1 {
			return nil, importer.ReasonBadNumber, ""
		}
	case freqMonthlyRel:
		if s.interval < 1 || s.interval > 10 || s.recur < 1 {
			return nil, importer.ReasonBadNumber, ""
		}
		switch s.relInt {
		case 1, 2, 4, 8, 16:
		default:
			return nil, importer.ReasonBadNumber, ""
		}
	}
	var reason importer.Reason
	if s.startDate, reason = parseYMD(tb.get(rec, "active_start_date")); reason != "" {
		return nil, reason, ""
	}
	endRaw := nullable(tb.get(rec, "active_end_date"))
	if endRaw == "" {
		endRaw = "99991231"
	}
	if s.endDate, reason = parseYMD(endRaw); reason != "" {
		return nil, reason, ""
	}
	if s.startSec, reason = parseHMS(tb.get(rec, "active_start_time")); reason != "" {
		return nil, reason, ""
	}
	endT := nullable(tb.get(rec, "active_end_time"))
	if endT == "" {
		endT = "235959"
	}
	if s.endSec, reason = parseHMS(endT); reason != "" {
		return nil, reason, ""
	}
	return &s, "", ""
}

// parseYMD reads a YYYYMMDD integer. Year 9999 (no end date) is accepted.
func parseYMD(s string) (time.Time, importer.Reason) {
	if len(s) != 8 || !allDigits(s) {
		return time.Time{}, importer.ReasonBadTime
	}
	y := int(s[0]-'0')*1000 + int(s[1]-'0')*100 + int(s[2]-'0')*10 + int(s[3]-'0')
	m := int(s[4]-'0')*10 + int(s[5]-'0')
	d := int(s[6]-'0')*10 + int(s[7]-'0')
	t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
	if y < 1990 || m < 1 || m > 12 || t.Day() != d || t.Month() != time.Month(m) {
		return time.Time{}, importer.ReasonBadTime
	}
	return t, ""
}

// parseHMS reads an HHMMSS integer written without leading zeros (30000 is
// 03:00:00) and returns seconds of day.
func parseHMS(s string) (int, importer.Reason) {
	if s == "" || len(s) > 6 || !allDigits(s) {
		return 0, importer.ReasonBadTime
	}
	v := 0
	for _, c := range s {
		v = v*10 + int(c-'0')
	}
	h, m, sec := v/10000, v/100%100, v%100
	if h > 23 || m > 59 || sec > 59 {
		return 0, importer.ReasonBadTime
	}
	return h*3600 + m*60 + sec, ""
}

// parseHistory converts run_date and run_time (server-local) to UTC and reads
// run_duration (HHMMSS, hours unbounded). A time in a DST fold or gap is
// rejected: an observed time is never guessed.
func parseHistory(rd, rt, dur string, loc *time.Location) (time.Time, time.Duration, importer.Reason) {
	date, reason := parseYMD(rd)
	if reason != "" {
		return time.Time{}, 0, reason
	}
	if rt == "" {
		rt = "0"
	}
	sec, reason := parseHMS(rt)
	if reason != "" {
		return time.Time{}, 0, reason
	}
	var d time.Duration
	if dur != "" {
		if len(dur) > 9 || !allDigits(dur) {
			return time.Time{}, 0, importer.ReasonBadNumber
		}
		v := 0
		for _, c := range dur {
			v = v*10 + int(c-'0')
		}
		h, m, s := v/10000, v/100%100, v%100
		if m > 59 || s > 59 {
			return time.Time{}, 0, importer.ReasonBadNumber
		}
		d = time.Duration(h*3600+m*60+s) * time.Second
	}
	w := civilWall{date.Year(), date.Month(), date.Day(), sec / 3600, sec % 3600 / 60, sec % 60}
	utc, flag := resolveWall(w, loc)
	switch flag {
	case "ambiguous":
		return time.Time{}, 0, importer.ReasonDSTAmbiguous
	case "nonexistent":
		return time.Time{}, 0, importer.ReasonDSTNonexistent
	}
	return utc, d, ""
}

// times lists the seconds of day a schedule fires on an active day.
func (s *agentSched) times() []int {
	if s.subType == 1 {
		return []int{s.startSec}
	}
	step := s.subInt
	switch s.subType {
	case 4:
		step *= 60
	case 8:
		step *= 3600
	}
	var out []int
	for t := s.startSec; t <= s.endSec; t += step {
		out = append(out, t)
	}
	return out
}

func daysIn(y int, m time.Month) int { return time.Date(y, m+1, 0, 0, 0, 0, 0, time.UTC).Day() }

// dayMatch reports whether civil date d is an active day. missing is true when
// a monthly schedule names a day beyond the month length (no boundary).
func (s *agentSched) dayMatch(d time.Time) (match, missing bool) {
	if d.Before(s.startDate) || d.After(s.endDate) {
		return false, false
	}
	days := int(d.Sub(s.startDate) / (24 * time.Hour))
	months := (d.Year()-s.startDate.Year())*12 + int(d.Month()) - int(s.startDate.Month())
	switch s.freq {
	case freqOnce:
		return days == 0, false
	case freqDaily:
		return days%s.interval == 0, false
	case freqWeekly:
		startWeek := s.startDate.AddDate(0, 0, -int(s.startDate.Weekday()))
		weeks := int(d.Sub(startWeek)/(24*time.Hour)) / 7
		return weeks%s.recur == 0 && s.interval&(1<<uint(d.Weekday())) != 0, false
	case freqMonthly:
		if months%s.recur != 0 {
			return false, false
		}
		dim := daysIn(d.Year(), d.Month())
		if s.interval > dim {
			return false, d.Day() == dim
		}
		return d.Day() == s.interval, false
	case freqMonthlyRel:
		if months%s.recur != 0 {
			return false, false
		}
		return d.Day() == s.relativeDay(d.Year(), d.Month()), false
	}
	return false, false
}

// relativeDay returns the day of month named by freq_interval and
// freq_relative_interval (first to fourth, or last), or 0.
func (s *agentSched) relativeDay(y int, m time.Month) int {
	var days []int
	for d := 1; d <= daysIn(y, m); d++ {
		wd := time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Weekday()
		ok := false
		switch {
		case s.interval >= 1 && s.interval <= 7:
			ok = int(wd) == s.interval-1 // 1 = Sunday
		case s.interval == 8:
			ok = true
		case s.interval == 9:
			ok = wd != time.Saturday && wd != time.Sunday
		case s.interval == 10:
			ok = wd == time.Saturday || wd == time.Sunday
		}
		if ok {
			days = append(days, d)
		}
	}
	idx := map[int]int{1: 0, 2: 1, 4: 2, 8: 3}[s.relInt]
	if s.relInt == 16 {
		idx = len(days) - 1
	}
	if idx < 0 || idx >= len(days) {
		return 0
	}
	return days[idx]
}

// expand emits the boundaries of one schedule inside the window.
func (a *Agent) expand(r *run, s *agentSched, loc *time.Location, line int64, ord *int, job string) error {
	if !r.win {
		return nil
	}
	if s.freq == freqWeekly && s.recur > 1 {
		r.gap(gapWeeklyFactor)
	}
	base := map[string]string{"job": job, "freq": freqNames[s.freq], "tz": loc.String()}
	times := s.times()
	lo, hi := r.civilRange(loc)
	if lo.Before(s.startDate) {
		lo = s.startDate
	}
	if hi.After(s.endDate) {
		hi = s.endDate
	}
	missingDay := false
	for d := lo; !d.After(hi); d = d.AddDate(0, 0, 1) {
		if err := r.ctx.Err(); err != nil {
			return err
		}
		match, missing := s.dayMatch(d)
		if missing {
			missingDay = true
		}
		if !match {
			continue
		}
		for _, t := range times {
			w := civilWall{d.Year(), d.Month(), d.Day(), t / 3600, t % 3600 / 60, t % 60}
			if err := r.boundaryAt(line, ord, w, loc, clock.RuleHostLocal, base); err != nil {
				return err
			}
		}
	}
	if missingDay {
		r.gap(gapMonthDayMissing)
		return r.sendUnknown(line, *ord, job, reasonMonthDay, clock.RuleHostLocal)
	}
	return nil
}

var _ importer.Importer = (*Agent)(nil)
