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

// Finding is one printed result. Label is an Evidence* constant. Headline uses
// only the permitted §6.6 wordings. Every finding carries n and the low-n flag.
type Finding struct {
	Rule       string `json:"rule"` // B1, B2, B6, B12
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
}
