// Package investigate turns a case's audit data into facts, signals and an
// evaluation for the guided analysis (Steps 0 to 10). It is pure: every function
// takes values and returns values, reads no file, no clock and no network, and
// states no cause. The UI only renders its output.
//
// The order is fixed by decision A27: facts first (counts, percentiles, onset,
// the time split, concurrency), then signals (yes, no or unknown, each from named
// thresholds), then the evaluation of the hypothesis catalog. Missing data makes
// a signal unknown and a step "Cannot test yet"; it is never a pass.
package investigate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// Version names this table of thresholds. Any change to a value in Current, or
// to the meaning of a field, must come with a new Version: a test compares the
// fingerprint of the table with the history in testdata/investigate and fails
// when a fingerprint changes under the same Version. The result of every run
// carries Version and the whole table, so a saved answer says which rules made it.
const Version = "2026-10-27"

// Thresholds is the one table of named limits the engine reads. No other file
// holds a number that decides a signal. The comment on each field names the
// decision it comes from; fields without a decision number are planner starting
// points (research notes, section 5.2) and are tuned on the demo data.
type Thresholds struct {
	// Version repeats the package Version so a serialized table is self-describing.
	Version string `json:"version"`

	// ArrivalRatePerSec is lambda for Little's Law: the planning rate of the whole
	// API, in requests per second (A22). Audit covers a targeted slice, so the
	// measured audited rate is reported next to it and never replaces it.
	ArrivalRatePerSec float64 `json:"arrivalRatePerSec"`
	// GroupSizeP95 and GroupSizeP99 are the fewest requests a group needs before
	// its P95 or P99 is shown (A22; the same counts as audit.MinSampleCount and
	// audit.P99MinSampleCount, and a test keeps them equal).
	GroupSizeP95 int `json:"groupSizeP95"`
	GroupSizeP99 int `json:"groupSizeP99"`
	// ClockToleranceMs is the clock difference between hosts below which tier
	// onsets inside one minute bucket cannot be told apart (A22).
	ClockToleranceMs float64 `json:"clockToleranceMs"`

	// ChangeMinPercent and ChangeMinMs: a change is meaningful only at this
	// percentage AND this many milliseconds (A23). Both must hold.
	ChangeMinPercent float64 `json:"changeMinPercent"`
	ChangeMinMs      float64 `json:"changeMinMs"`
	// LoadChangePercent is the percentage a rate (requests per second, requests in
	// flight, retried calls) must move to read as up or down. The millisecond half
	// of A23 does not apply to a count.
	LoadChangePercent float64 `json:"loadChangePercent"`

	// OnsetFactor and OnsetRunMinutes: t0 is the first minute whose P95 stays above
	// OnsetFactor times the reference P95 for OnsetRunMinutes minutes in a row (A24).
	OnsetFactor     float64 `json:"onsetFactor"`
	OnsetRunMinutes int     `json:"onsetRunMinutes"`
	// OnsetMinSamples is the fewest requests a minute needs for its P95 to count.
	OnsetMinSamples int `json:"onsetMinSamples"`

	// StepRiseMinutes and StepRampBelowFactor: the shape is a step when, within
	// StepRiseMinutes before t0, the P95 was at or below StepRampBelowFactor times
	// the reference; otherwise it was already building (a ramp).
	StepRiseMinutes     int     `json:"stepRiseMinutes"`
	StepRampBelowFactor float64 `json:"stepRampBelowFactor"`
	// PeriodicMinExcursions, PeriodicShortMinutes, PeriodicLongMinutes and
	// PeriodicToleranceMin: the shape is periodic when the window holds at least
	// PeriodicMinExcursions separate slow bursts whose starts sit a whole number of
	// periods apart (a short period of 15 minutes or a long one of 60), give or take
	// PeriodicToleranceMin minutes.
	PeriodicMinExcursions int `json:"periodicMinExcursions"`
	PeriodicShortMinutes  int `json:"periodicShortMinutes"`
	PeriodicLongMinutes   int `json:"periodicLongMinutes"`
	PeriodicToleranceMin  int `json:"periodicToleranceMin"`

	// ScopeFewMaxRoutes and ScopeAllShare: one route moved is "one route", up to
	// ScopeFewMaxRoutes is "a few routes", at least ScopeAllShare of the testable
	// routes is "everything".
	ScopeFewMaxRoutes int     `json:"scopeFewMaxRoutes"`
	ScopeAllShare     float64 `json:"scopeAllShare"`

	// GapWarnMinutes is the gap rule (A35): a gap of this many minutes or fewer
	// warns and the step runs on the minutes that exist; a longer gap makes those
	// minutes "Cannot test yet". Missing minutes are never counted as zero.
	GapWarnMinutes int `json:"gapWarnMinutes"`
	// GapRefMinPerMinute is the reference median requests per minute at which an
	// empty minute is read as a suspected gap rather than a quiet minute.
	GapRefMinPerMinute int `json:"gapRefMinPerMinute"`

	// ClockMaxSpanHours and ClockAnomalyShare: a call that ends before it starts, or
	// lasts longer than ClockMaxSpanHours, is a clock anomaly; the clock signal is
	// "yes" when the anomalies reach ClockAnomalyShare of the calls read.
	ClockMaxSpanHours int     `json:"clockMaxSpanHours"`
	ClockAnomalyShare float64 `json:"clockAnomalyShare"`

	// DominantMinShare is the share of the extra time one part (A, C, P or O) must
	// hold to be "dominant" in Step 3. PartsMinCoverage is the share of calls that
	// must carry both A and C for the split to run at full strength.
	DominantMinShare float64 `json:"dominantMinShare"`
	PartsMinCoverage float64 `json:"partsMinCoverage"`

	// IISMatchToleranceMs is how far apart an audit start and an IIS start may be
	// and still be one request. IISMatchMinRate is the share of audit calls that must
	// match an IIS row before outside time (O) is shown.
	IISMatchToleranceMs float64 `json:"iisMatchToleranceMs"`
	IISMatchMinRate     float64 `json:"iisMatchMinRate"`
	// AuditDipShare is how far the audit share of IIS requests may fall in the
	// incident window, against the reference, before audit coverage is in doubt.
	AuditDipShare float64 `json:"auditDipShare"`

	// FlatTopMinutes, FlatTopTolerance and FlatTopMinLevel: the in-flight peak
	// stays within FlatTopTolerance of one level, at least FlatTopMinLevel, for
	// FlatTopMinutes minutes: a flat top. PoolFullShare is the share of the entered
	// pool limit that lambda x W must reach for the pool to read as full.
	FlatTopMinutes   int     `json:"flatTopMinutes"`
	FlatTopTolerance float64 `json:"flatTopTolerance"`
	FlatTopMinLevel  int     `json:"flatTopMinLevel"`
	PoolFullShare    float64 `json:"poolFullShare"`

	// ChangeLeadMinutes: a change leads the onset when it sits this many minutes or
	// fewer before t0, and not after it. Overlap alone is not a lead.
	ChangeLeadMinutes int `json:"changeLeadMinutes"`
	// RetryWindowSeconds: the same client calling the same URL again within this
	// many seconds is a retried call.
	RetryWindowSeconds int `json:"retryWindowSeconds"`
	// RetryMinCalls is the fewest retried calls in the incident window before a rise in
	// retries is read as a rise at all.
	RetryMinCalls int `json:"retryMinCalls"`
	// RetryMinShare is the smallest share of incident calls that are retried before a
	// rise in retries is read as a rise: a handful of repeats among thousands of calls
	// is noise.
	RetryMinShare float64 `json:"retryMinShare"`
	// OriginBucketSeconds is the minute bucket of the charts; onsets closer than the
	// bucket plus the clock tolerance are "not separable".
	OriginBucketSeconds int `json:"originBucketSeconds"`
}

// Current returns the table in force. It is a function, not a variable, so a
// caller cannot change a threshold at run time.
func Current() Thresholds {
	return Thresholds{
		Version:               Version,
		ArrivalRatePerSec:     40,
		GroupSizeP95:          20,
		GroupSizeP99:          100,
		ClockToleranceMs:      1000,
		ChangeMinPercent:      30,
		ChangeMinMs:           100,
		LoadChangePercent:     30,
		OnsetFactor:           2,
		OnsetRunMinutes:       5,
		OnsetMinSamples:       5,
		StepRiseMinutes:       2,
		StepRampBelowFactor:   1.5,
		PeriodicMinExcursions: 3,
		PeriodicShortMinutes:  15,
		PeriodicLongMinutes:   60,
		PeriodicToleranceMin:  2,
		ScopeFewMaxRoutes:     3,
		ScopeAllShare:         0.8,
		GapWarnMinutes:        2,
		GapRefMinPerMinute:    5,
		ClockMaxSpanHours:     6,
		ClockAnomalyShare:     0.01,
		DominantMinShare:      0.5,
		PartsMinCoverage:      0.8,
		IISMatchToleranceMs:   3000,
		IISMatchMinRate:       0.5,
		AuditDipShare:         0.3,
		FlatTopMinutes:        3,
		FlatTopTolerance:      0.05,
		FlatTopMinLevel:       5,
		PoolFullShare:         0.95,
		ChangeLeadMinutes:     30,
		RetryWindowSeconds:    10,
		RetryMinCalls:         5,
		RetryMinShare:         0.02,
		OriginBucketSeconds:   60,
	}
}

// Entry is one named value of the table, in the form the fingerprint reads and
// the "Why?" view lists.
type Entry struct {
	Name  string
	Value float64
}

// Entries lists every threshold, in a fixed order. A test checks, by reflection,
// that no numeric field of Thresholds is missing from it, so a new limit cannot
// escape the fingerprint.
func (t Thresholds) Entries() []Entry {
	f := func(n string, v float64) Entry { return Entry{n, v} }
	i := func(n string, v int) Entry { return Entry{n, float64(v)} }
	return []Entry{
		f("arrivalRatePerSec", t.ArrivalRatePerSec),
		i("groupSizeP95", t.GroupSizeP95),
		i("groupSizeP99", t.GroupSizeP99),
		f("clockToleranceMs", t.ClockToleranceMs),
		f("changeMinPercent", t.ChangeMinPercent),
		f("changeMinMs", t.ChangeMinMs),
		f("loadChangePercent", t.LoadChangePercent),
		f("onsetFactor", t.OnsetFactor),
		i("onsetRunMinutes", t.OnsetRunMinutes),
		i("onsetMinSamples", t.OnsetMinSamples),
		i("stepRiseMinutes", t.StepRiseMinutes),
		f("stepRampBelowFactor", t.StepRampBelowFactor),
		i("periodicMinExcursions", t.PeriodicMinExcursions),
		i("periodicShortMinutes", t.PeriodicShortMinutes),
		i("periodicLongMinutes", t.PeriodicLongMinutes),
		i("periodicToleranceMin", t.PeriodicToleranceMin),
		i("scopeFewMaxRoutes", t.ScopeFewMaxRoutes),
		f("scopeAllShare", t.ScopeAllShare),
		i("gapWarnMinutes", t.GapWarnMinutes),
		i("gapRefMinPerMinute", t.GapRefMinPerMinute),
		i("clockMaxSpanHours", t.ClockMaxSpanHours),
		f("clockAnomalyShare", t.ClockAnomalyShare),
		f("dominantMinShare", t.DominantMinShare),
		f("partsMinCoverage", t.PartsMinCoverage),
		f("iisMatchToleranceMs", t.IISMatchToleranceMs),
		f("iisMatchMinRate", t.IISMatchMinRate),
		f("auditDipShare", t.AuditDipShare),
		i("flatTopMinutes", t.FlatTopMinutes),
		f("flatTopTolerance", t.FlatTopTolerance),
		i("flatTopMinLevel", t.FlatTopMinLevel),
		f("poolFullShare", t.PoolFullShare),
		i("changeLeadMinutes", t.ChangeLeadMinutes),
		i("retryWindowSeconds", t.RetryWindowSeconds),
		i("retryMinCalls", t.RetryMinCalls),
		f("retryMinShare", t.RetryMinShare),
		i("originBucketSeconds", t.OriginBucketSeconds),
	}
}

// Fingerprint is a short hash of the names and values of the table (not of the
// Version string). Two tables with the same values have the same fingerprint.
func (t Thresholds) Fingerprint() string {
	var b strings.Builder
	for _, e := range t.Entries() {
		b.WriteString(e.Name)
		b.WriteByte('=')
		b.WriteString(strconv.FormatFloat(e.Value, 'g', -1, 64))
		b.WriteByte('\n')
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])[:16]
}

// String names the table for logs: version and fingerprint.
func (t Thresholds) String() string { return fmt.Sprintf("%s/%s", t.Version, t.Fingerprint()) }
