package audit

import "time"

// P2-lite incident patterns (roadmap §8.3). The rules read completed requests
// through the existing SlowestReader and Repository.Requests, so no new Mongo
// code exists. Every threshold is a tunable default; a caller may override the
// two that matter per case before looking at data.
const (
	// DefaultStallMs is rule B1: a request observed at or above this many ms is a stall.
	DefaultStallMs = 45000
	// DefaultEpisodeGapSeconds is rule B2 (ruling R9): stalls closer than this merge into one episode.
	DefaultEpisodeGapSeconds = 600
	// DefaultAuditStallMs and DefaultAuditStallShare are rule B6.
	DefaultAuditStallMs    = 5000
	DefaultAuditStallShare = 0.3
	// DefaultVolleyWindowMs is rule B12: starts within this many ms are one volley.
	DefaultVolleyWindowMs = 1000
	// MinRecurringEpisodes is the independent-episode count below which nothing is "recurring".
	MinRecurringEpisodes = 3
	// LowNRows marks a finding resting on fewer rows than this as low-n.
	LowNRows = 10
	// MaxPatternRows bounds the stall rows and audit-stall rows read per source.
	MaxPatternRows = 500
)

// Evidence labels (conventions): exactly one of these three.
const (
	EvidenceSymptom           = "Symptom"
	EvidenceCorrelation       = "Correlation"
	EvidenceStrongCorrelation = "Strong correlation"
)

// Recurrence values. A pattern that does not recur across independent episodes
// is never shown as recurring.
const (
	RecurrenceNone          = "none"
	RecurrenceSingleEpisode = "single_episode"
	RecurrenceTwoEpisodes   = "two_episodes"
	RecurrenceRecurring     = "recurring"
)

// PatternInput selects the window and sources. StallMs and EpisodeGapSeconds
// override the defaults when non-zero (validated in ValidatePattern).
type PatternInput struct {
	SourceIDs         []string  `json:"sourceIds"`
	Start             time.Time `json:"start"`
	End               time.Time `json:"end"`
	StallMs           float64   `json:"stallMs,omitempty"`
	EpisodeGapSeconds int       `json:"episodeGapSeconds,omitempty"`
	// IdleGaps opts in to rule B6g, which reads every row in the window (not only
	// the stalls). It is off unless a caller asks for it.
	IdleGaps bool `json:"idleGaps,omitempty"`
	// TimeZone is the display zone (an IANA name, "" for UTC) that B9 and the
	// audit-create series use to cut days.
	TimeZone string `json:"timeZone,omitempty"`
	// StepChange opts in to rule B9. Its window is its own, up to 120 days, and
	// is not limited by the 7-day window of the other rules.
	StepChange *StepChangeInput `json:"stepChange,omitempty"`
	// AuditSeries opts in to the audit-create series (B6s) and the
	// fixed-duration check (B6f). Like B6g it reads every request in the window.
	AuditSeries bool `json:"auditSeries,omitempty"`
}

// PatternParams echoes the thresholds actually used, so a finding can be reread.
type PatternParams struct {
	StallMs           float64 `json:"stallMs"`
	EpisodeGapSeconds int     `json:"episodeGapSeconds"`
	AuditStallMs      float64 `json:"auditStallMs"`
	AuditStallShare   float64 `json:"auditStallShare"`
	VolleyWindowMs    int     `json:"volleyWindowMs"`
}

// Stall is one B1 request. It carries no URL, payload, IP or user agent.
type Stall struct {
	SourceID    string     `json:"sourceId"`
	ID          string     `json:"id"`
	Controller  string     `json:"controller"`
	Action      string     `json:"action"`
	InitiatedAt time.Time  `json:"initiatedAt"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
	ObservedMs  float64    `json:"observedMs"`
	ApiTimeMs   *float64   `json:"apiTimeMs,omitempty"`
	UserID      *int       `json:"userId,omitempty"`
	Episode     int        `json:"episode"` // 1-based index into PatternReport.Episodes
}

// Episode is B2: stalls merged by the quiet gap. Requests is the stall count, one episode however many.
type Episode struct {
	Index      int       `json:"index"`
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
	Requests   int       `json:"requests"`
	Users      int       `json:"users"`
	MaxMs      float64   `json:"maxMs"`
	Operations []string  `json:"operations"` // "Controller.Action", at most 5, by count
}

// Volley is B12: two or more stalls from one user starting within the volley window.
type Volley struct {
	Episode        int       `json:"episode"`
	UserID         int       `json:"userId"`
	Requests       int       `json:"requests"`
	FirstStart     time.Time `json:"firstStart"`
	StartSpreadMs  int64     `json:"startSpreadMs"`
	EndStaggerMs   int64     `json:"endStaggerMs"` // last completion minus first completion
	EndGapsMs      []int64   `json:"endGapsMs"`    // gaps between consecutive completions
	IncompleteEnds int       `json:"incompleteEnds"`
}

// AuditStall is B6: audit create took at least AuditStallMs and at least AuditStallShare of observed time.
type AuditStall struct {
	SourceID          string    `json:"sourceId"`
	ID                string    `json:"id"`
	Controller        string    `json:"controller"`
	Action            string    `json:"action"`
	InitiatedAt       time.Time `json:"initiatedAt"`
	ObservedMs        float64   `json:"observedMs"`
	AuditCreateTimeMs float64   `json:"auditCreateTimeMs"`
	Share             float64   `json:"share"`
}

// OnsetMinute is one B3a row: the onsets of B2 episodes (the first stall start of
// each) that fall within two minutes of Minute, a minute of the hour in UTC.
// Days counts distinct UTC days among them. Episodes lists their 1-based indexes.
type OnsetMinute struct {
	Minute   int   `json:"minute"`
	Days     int   `json:"days"`
	Onsets   int   `json:"onsets"`
	Episodes []int `json:"episodes"`
}

// Concentration dimensions.
const (
	ConcentrationOperation = "operation"
	ConcentrationUser      = "user"
)

// Concentration is one A3 row. StallShare, TrafficShare and Lift are null when
// Withheld is true (fewer than MinSampleCount rows overall for the key); the
// counts stay exact. Key is "Controller.Action" or a decimal user id.
type Concentration struct {
	Dimension    string   `json:"dimension"`
	Key          string   `json:"key"`
	StallShare   *float64 `json:"stallShare"`
	TrafficShare *float64 `json:"trafficShare"`
	Lift         *float64 `json:"lift"`
	StallN       int      `json:"stallN"`
	AllN         int64    `json:"allN"`
	Episodes     int      `json:"episodes"`
	Withheld     bool     `json:"withheld"`
}

// Repeat is one B12r row: one user started Operation again Repeats times while
// an earlier request of the same operation was still running, during Episode.
// SameSize is true when the repeats had the same RequestKB as the earlier one;
// a different size is reported as its own row. LongestChain is the most starts
// one earlier request had in flight over it, counting itself.
type Repeat struct {
	Episode      int    `json:"episode"`
	UserID       int    `json:"userId"`
	Operation    string `json:"operation"`
	Repeats      int    `json:"repeats"`
	SameSize     bool   `json:"sameSize"`
	LongestChain int    `json:"longestChain"`
}

// IdleBand is one B6g gap band: gaps from FromS (inclusive) to ToS (exclusive;
// null for the last band). MedianMs, P95Ms and SlowRate are null when N is below
// MinSampleCount.
type IdleBand struct {
	FromS    int      `json:"fromS"`
	ToS      *int     `json:"toS"`
	N        int      `json:"n"`
	MedianMs *float64 `json:"medianMs"`
	P95Ms    *float64 `json:"p95Ms"`
	SlowRate *float64 `json:"slowRate"`
}

// IdleLift is the B6g lift for gaps of at least GS seconds. Value is null when
// Withheld is true.
type IdleLift struct {
	GS       int      `json:"gS"`
	Value    *float64 `json:"value"`
	Withheld bool     `json:"withheld"`
}

// IdleGaps is B6g: audit create time by the quiet gap before the request.
// PartialReason is additive and says why Partial is true.
type IdleGaps struct {
	Bands            []IdleBand `json:"bands"`
	Lift             []IdleLift `json:"lift"`
	ExcludedHangfire int        `json:"excludedHangfire"`
	Partial          bool       `json:"partial"`
	PartialReason    string     `json:"partialReason,omitempty"`
}

// Finding is one printed result. Label is an Evidence* constant. Headline uses
// only the permitted §6.6 wordings. Every finding carries n and the low-n flag.
type Finding struct {
	Rule       string `json:"rule"` // B1, B2, B6, B12, B3a, A3, B12r, B6g, B9, B6f
	Label      string `json:"label"`
	Headline   string `json:"headline"`
	N          int    `json:"n"`
	LowN       bool   `json:"lowN"`
	EvidenceID string `json:"evidenceId"` // stable provenance reference, never a row id
}

// PatternQuality counts the rows the figures leave out, so no count reads as complete.
type PatternQuality struct {
	Incomplete int64 `json:"incomplete"`
	Pending    int64 `json:"pending"`
	Invalid    int64 `json:"invalid"`
}

// PatternCoverage is the P1a figure printed with every finding. Run is false
// when no census ran (a live source without --allow-broad-scans); Note says why.
type PatternCoverage struct {
	Run                   bool     `json:"run"`
	RequestInitiatedAtPct *float64 `json:"requestInitiatedAtPct,omitempty"`
	AccountIDPct          *float64 `json:"accountIdPct,omitempty"`
	Note                  string   `json:"note"`
}

// PatternReport is the whole P2-lite answer for one window.
type PatternReport struct {
	Params      PatternParams    `json:"params"`
	Stalls      []Stall          `json:"stalls"`
	Episodes    []Episode        `json:"episodes"`
	Volleys     []Volley         `json:"volleys"`
	AuditStalls []AuditStall     `json:"auditStalls"`
	Recurrence  string           `json:"recurrence"`
	Findings    []Finding        `json:"findings"`
	Quality     PatternQuality   `json:"quality"`
	Coverage    *PatternCoverage `json:"coverage,omitempty"`
	// Truncated is StallsTruncated || AuditsTruncated, kept for older clients.
	Truncated bool `json:"truncated"`
	// StallsTruncated is true when the stall read (B1, B2, B12) hit the row cap.
	StallsTruncated bool `json:"stallsTruncated"`
	// AuditsTruncated is true when the audit-create read (B6) hit the row cap.
	AuditsTruncated bool         `json:"auditsTruncated"`
	Provenance      []Provenance `json:"provenance,omitempty"`
	Warnings        []string     `json:"warnings"`

	// Additive fields (all optional). OnsetMinutes is B3a, Concentration is A3,
	// Repeats is B12r and IdleGaps is B6g, which only runs when asked for.
	OnsetMinutes  []OnsetMinute   `json:"onsetMinutes,omitempty"`
	Concentration []Concentration `json:"concentration,omitempty"`
	Repeats       []Repeat        `json:"repeats,omitempty"`
	IdleGaps      *IdleGaps       `json:"idleGaps,omitempty"`
	// StepChange is B9 and AuditSeries is B6s with B6f; each only runs when asked for.
	StepChange  *StepChange  `json:"stepChange,omitempty"`
	AuditSeries *AuditSeries `json:"auditSeries,omitempty"`
}
