package audit

import (
	"context"
	"errors"
	"time"
)

// OperationTimeout bounds connection tests and data operations.
const OperationTimeout = 5 * time.Minute

const (
	MaxSources      = 8
	MaxPageSize     = 100
	MaxSummaryRows  = 40000
	MaxCohorts      = 10000
	MaxTimeBuckets  = 120
	MaxPayloadBytes = 262144
	MaxInterval     = 7 * 24 * time.Hour
	MaxFilterLength = 200
)

var (
	ErrInvalidInput       = errors.New("invalid input")
	ErrNotFound           = errors.New("not found")
	ErrLimit              = errors.New("limit exceeded")
	ErrLegacyScanLimit    = errors.New("legacy metadata scan limit exceeded")
	ErrAPIGroupLimit      = errors.New("API group limit exceeded")
	ErrSummaryRowLimit    = errors.New("summary row limit exceeded")
	ErrSummaryBytesLimit  = errors.New("summary byte limit exceeded")
	ErrAnalysisBytesLimit = errors.New("analysis byte limit exceeded")
	ErrBusy               = errors.New("busy")
)

type Query struct {
	SourceIDs     []string  `json:"sourceIds"`
	Start         time.Time `json:"start"`
	End           time.Time `json:"end"`
	BaselineStart time.Time `json:"baselineStart"`
	BaselineEnd   time.Time `json:"baselineEnd"`
	Kind          string    `json:"kind"`
	Outcome       string    `json:"outcome"`
	Metric        string    `json:"metric"`
	BucketSeconds int       `json:"bucketSeconds"`
	Controller    string    `json:"controller"`
	Action        string    `json:"action"`
}

type Source struct {
	SelectionKey    string `json:"selectionKey,omitempty"`
	ConnectionID    string `json:"connectionId,omitempty"`
	ConnectionLabel string `json:"connectionLabel,omitempty"`
	ID              string `json:"id"`
	Label           string `json:"label"`
	Database        string `json:"database"`
	Collection      string `json:"collection"`
}

type SourceInput struct {
	ConnectionLabel   string `json:"connectionLabel"`
	Label             string `json:"label"`
	URI               string `json:"uri"`
	Username          string `json:"username"`
	Password          string `json:"password"`
	AuthSource        string `json:"authSource"`
	TLSMode           string `json:"tlsMode"`
	CACertificate     string `json:"caCertificate"`
	SavedConnectionID string `json:"savedConnectionId"`
	Database          string `json:"database"`
	Collection        string `json:"collection"`
}

type ConnectionInput struct {
	URI               string `json:"uri"`
	Username          string `json:"username"`
	Password          string `json:"password"`
	AuthSource        string `json:"authSource"`
	TLSMode           string `json:"tlsMode"`
	CACertificate     string `json:"caCertificate"`
	SavedConnectionID string `json:"savedConnectionId"`
}

type ConnectionResult struct {
	Databases []string `json:"databases"`
	Warning   string   `json:"warning,omitempty"`
}

// ConnectionTester is an optional repository capability for temporary connection tests.
type ConnectionTester interface {
	TestConnection(context.Context, ConnectionInput) (ConnectionResult, error)
}

// ConnectionValidator checks URI and TLS settings locally without opening a
// connection to the configured MongoDB server.
type ConnectionValidator interface {
	ValidateConnectionSettings(ConnectionInput) error
}

// ConnectionError carries a sanitized, stable category for connection UI.
// Message must never contain user-provided URI, certificate, or credential data.
type ConnectionError struct {
	Stage   string `json:"stage"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *ConnectionError) Error() string { return e.Message }

func (e *ConnectionError) Unwrap() error { return ErrInvalidInput }

type SourceResult struct {
	SourceID     string `json:"sourceId"`
	OK           bool   `json:"ok"`
	Error        string `json:"error"`
	ErrorCode    string `json:"errorCode,omitempty"`
	Version      string `json:"version"`
	IndexWarning string `json:"indexWarning"`
}

type Bucket struct {
	SourceID   string    `json:"sourceId"`
	Controller string    `json:"controller"`
	Action     string    `json:"action"`
	Kind       string    `json:"kind"`
	Outcome    string    `json:"outcome"`
	State      string    `json:"state"`
	At         time.Time `json:"at"`
	Bin        int       `json:"bin"`
	Count      int64     `json:"count"`
	SumMs      float64   `json:"sumMs"`
	MinMs      float64   `json:"minMs"`
	MaxMs      float64   `json:"maxMs"`
	// Phase sums cover valid documents only. The per-phase counts say how many
	// of them carried that phase as a number, so a phase mean divides by its own
	// count rather than Count.
	SumAuditMs   float64 `json:"sumAuditMs,omitempty"`
	SumApiMs     float64 `json:"sumApiMs,omitempty"`
	SumPayloadMs float64 `json:"sumPayloadMs,omitempty"`
	NAudit       int64   `json:"nAudit,omitempty"`
	NApi         int64   `json:"nApi,omitempty"`
	NPayload     int64   `json:"nPayload,omitempty"`

	PendingAsync    int64 `json:"pendingAsync,omitempty"`
	ObservedPartial int64 `json:"observedPartial,omitempty"`

	Count4xx            int64 `json:"count4xx,omitempty"`
	Count5xx            int64 `json:"count5xx,omitempty"`
	CountHangfireFailed int64 `json:"countHangfireFailed,omitempty"`
}

type Quantile struct {
	LowerMs float64  `json:"lowerMs"`
	UpperMs *float64 `json:"upperMs"`
	ValueMs *float64 `json:"valueMs"`
}

type Stats struct {
	Count  int64     `json:"count"`
	MeanMs *float64  `json:"meanMs"`
	MinMs  *float64  `json:"minMs"`
	MaxMs  *float64  `json:"maxMs"`
	P50    *Quantile `json:"p50"`
	P90    *Quantile `json:"p90,omitempty"`
	P95    *Quantile `json:"p95"`
	// P99 is nil below 100 samples; the nearest-rank P99 would otherwise be the maximum.
	P99       *Quantile  `json:"p99,omitempty"`
	Histogram []BinCount `json:"histogram"`
	// Phases holds each phase's mean over the documents that carry it; nil when none do.
	Phases *PhaseMeans `json:"phases,omitempty"`

	ObservedPartial int64 `json:"observedPartial,omitempty"`

	ClientErrors    int64    `json:"clientErrors,omitempty"`
	ServerErrors    int64    `json:"serverErrors,omitempty"`
	ClientErrorRate *float64 `json:"clientErrorRate,omitempty"`
	ServerErrorRate *float64 `json:"serverErrorRate,omitempty"`
}

// PhaseMeans splits request time into the audit-row write and API processing,
// which add up to the total, plus the payload write, which happens after the
// response and is not part of latency. A field is nil when it has no samples.
// In Comparison.PhaseDeltaMs it holds current minus reference.
type PhaseMeans struct {
	AuditMs   *float64 `json:"auditMs,omitempty"`
	ApiMs     *float64 `json:"apiMs,omitempty"`
	PayloadMs *float64 `json:"payloadMs,omitempty"`
}

type BinCount struct {
	Bin   int   `json:"bin"`
	Count int64 `json:"count"`
}

type Comparison struct {
	Key             string   `json:"key"`
	SourceID        string   `json:"sourceId"`
	Controller      string   `json:"controller"`
	Action          string   `json:"action"`
	Kind            string   `json:"kind"`
	Outcome         string   `json:"outcome"`
	State           string   `json:"state"`
	CurrentEvents   int64    `json:"currentEvents"`
	ReferenceEvents int64    `json:"referenceEvents"`
	Current         Stats    `json:"current"`
	Reference       Stats    `json:"reference"`
	MedianRatio     *float64 `json:"medianRatio"`
	P95Ratio        *float64 `json:"p95Ratio"`
	MedianDeltaMs   *float64 `json:"medianDeltaMs"`
	P95DeltaMs      *float64 `json:"p95DeltaMs"`
	P99Ratio        *float64 `json:"p99Ratio,omitempty"`
	P99DeltaMs      *float64 `json:"p99DeltaMs,omitempty"`
	ImpactMs        *float64 `json:"impactMs,omitempty"`
	TotalTimeMs     float64  `json:"totalTimeMs"`
	ShiftZ          *float64 `json:"shiftZ,omitempty"`
	Significant     bool     `json:"significant,omitempty"`
	// PhaseDeltaMs is the change in each phase's mean, current minus reference.
	PhaseDeltaMs *PhaseMeans `json:"phaseDeltaMs,omitempty"`
}

type Point struct {
	At       time.Time `json:"at"`
	Count    int64     `json:"count"`
	P50      *Quantile `json:"p50"`
	P95      *Quantile `json:"p95"`
	P95Ratio *float64  `json:"p95Ratio"`
}

// LoadCorrelation is the Spearman rho between the load other cohorts put on
// the system (Little's-law concurrency) and this series' per-bucket P95; it is
// nil below 8 paired buckets or when either side is constant.
type Series struct {
	Key             string   `json:"key"`
	Points          []Point  `json:"points"`
	LoadCorrelation *float64 `json:"loadCorrelation,omitempty"`
}

type Quality struct {
	Incomplete     int64 `json:"incomplete"`
	Invalid        int64 `json:"invalid"`
	UnknownOutcome int64 `json:"unknownOutcome"`

	PendingAsync int64 `json:"pendingAsync,omitempty"`

	// ByKind splits the counts above by row kind (synchronous, asynchronous,
	// hangfire, unknown). Only kinds with something to report are present.
	ByKind map[string]QualityCounts `json:"byKind,omitempty"`
}

// QualityCounts is one kind's share of the quality counters.
type QualityCounts struct {
	Incomplete     int64 `json:"incomplete"`
	Invalid        int64 `json:"invalid"`
	UnknownOutcome int64 `json:"unknownOutcome"`
	PendingAsync   int64 `json:"pendingAsync,omitempty"`
}

type Analysis struct {
	LoadWindows   []LoadWindow   `json:"loadWindows"`
	RequestVolume []VolumeBucket `json:"requestVolume"`
	Query         Query          `json:"query"`
	APIs          []Comparison   `json:"apis"`
	Timeline      []Series       `json:"timeline"`
	Sources       []SourceResult `json:"sources"`
	Quality       Quality        `json:"quality"`
	Approximate   bool           `json:"approximate"`
	Warnings      []string       `json:"warnings"`
	// Timings reports how long each source spent answering each window. It is
	// additive and absent for repositories that do not measure it.
	Timings []SourceTiming `json:"timings,omitempty"`
	// Rollups is one row per API with current-window throughput and error rate.
	Rollups []Rollup `json:"rollups,omitempty"`
}

// SourceTiming is the database read time for one source and one window.
// Window is "current" or "reference". Rows is the number of summary rows the
// source returned for that window.
type SourceTiming struct {
	SourceID string `json:"sourceId"`
	Window   string `json:"window"`
	ReadMs   int64  `json:"readMs"`
	Rows     int64  `json:"rows"`
}

// VolumeBucket counts starts, including records without a usable duration.
// Names are combined across the selected sources, request kinds and outcomes.
type VolumeBucket struct {
	Controller string    `json:"controller"`
	Action     string    `json:"action"`
	At         time.Time `json:"at"`
	Count      int64     `json:"count"`
}

type RequestRecord struct {
	ID                  string     `json:"id"`
	SourceID            string     `json:"sourceId"`
	Controller          string     `json:"controller"`
	Action              string     `json:"action"`
	Kind                string     `json:"kind"`
	Outcome             string     `json:"outcome"`
	Status              string     `json:"status"`
	URL                 string     `json:"url"`
	InitiatedAt         *time.Time `json:"initiatedAt"`
	CompletedAt         *time.Time `json:"completedAt"`
	DurationMs          *float64   `json:"durationMs"`
	ApiTimeMs           *float64   `json:"apiTimeMs"`
	AuditCreateTimeMs   *float64   `json:"auditCreateTimeMs"`
	PayloadRecordTimeMs *float64   `json:"payloadRecordTimeMs"`
	RequestKB           float64    `json:"requestKB"`
	ResponseKB          float64    `json:"responseKB"`
	UserID              *int       `json:"userId,omitempty"`
	UserIP              string     `json:"userIp,omitempty"`
	UserAgent           string     `json:"userAgent,omitempty"`
	UIVersion           string     `json:"uiVersion,omitempty"`
	CreatedOn           *time.Time `json:"createdOn,omitempty"`
	UpdatedOn           *time.Time `json:"updatedOn,omitempty"`
	PollingTypeRaw      string     `json:"pollingTypeRaw,omitempty"`
}

// RequestRecord's last seven fields are additive audit evidence for the table,
// the CSV export and RecordDetail. PollingTypeRaw keeps the stored
// RequestPollingType verbatim, including its trailing space.

// RecordInput selects one audit document by source and Mongo ObjectID.
type RecordInput struct {
	SourceID string `json:"sourceId"`
	ID       string `json:"id"`
}

// RecordDetail is the complete underlying audit evidence for one document: the
// 19 non-payload schema fields (RequestRecord), the state derived from them and
// both capped payloads. InitiatedAt is the stored RequestInitiatedAt and is nil
// when the document lacks it; Kind, Outcome (status class), State and
// DurationMs (observed time) come from the same normalisation the table and
// the summaries use.
type RecordDetail struct {
	RequestRecord
	State        string  `json:"state"`
	PendingAsync bool    `json:"pendingAsync,omitempty"`
	Payload      Payload `json:"payload"`
}

type PageInput struct {
	Query  Query  `json:"query"`
	APIKey string `json:"apiKey"`
	Cursor string `json:"cursor"`
	Limit  int    `json:"limit"`
}

type Page struct {
	Rows       []RequestRecord `json:"rows"`
	NextCursor string          `json:"nextCursor"`
	Sources    []SourceResult  `json:"sources"`
	Truncated  bool            `json:"truncated"`
}

type PayloadInput struct {
	SourceID string `json:"sourceId"`
	ID       string `json:"id"`
}

type Payload struct {
	Request   string `json:"request"`
	Response  string `json:"response"`
	Truncated bool   `json:"truncated"`
}

const (
	// DefaultSchemaSample is used when a schema census does not name a size.
	DefaultSchemaSample = 200
	// MaxSchemaSample caps the documents a schema census may inspect.
	MaxSchemaSample = 1000
)

// SchemaCensus lists which top-level fields appear in a sample of audit
// documents. It carries names, BSON types and counts only, never values.
type SchemaCensus struct {
	Sampled int             `json:"sampled"`
	Fields  []FieldPresence `json:"fields"`
}

// FieldPresence counts the sampled documents that contain a field and the
// sorted, de-duplicated BSON type names seen for it.
type FieldPresence struct {
	Name    string   `json:"name"`
	Present int      `json:"present"`
	Types   []string `json:"types"`
}

// SchemaSampler is an optional repository capability for schema census.
type SchemaSampler interface {
	SampleSchema(ctx context.Context, sourceID string, n int) (SchemaCensus, error)
}

type Repository interface {
	Sources(context.Context) ([]Source, error)
	AddSource(context.Context, SourceInput) (Source, error)
	DeleteSource(context.Context, string) error
	Analyze(context.Context, Query) (Analysis, error)
	Requests(context.Context, PageInput) (Page, error)
	Payload(context.Context, PayloadInput) (Payload, error)
	Record(context.Context, RecordInput) (RecordDetail, error)
	Close(context.Context) error
}
