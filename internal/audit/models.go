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
}

type Quantile struct {
	LowerMs float64  `json:"lowerMs"`
	UpperMs *float64 `json:"upperMs"`
	ValueMs *float64 `json:"valueMs"`
}

type Stats struct {
	Count     int64      `json:"count"`
	MeanMs    *float64   `json:"meanMs"`
	MinMs     *float64   `json:"minMs"`
	MaxMs     *float64   `json:"maxMs"`
	P50       *Quantile  `json:"p50"`
	P95       *Quantile  `json:"p95"`
	Histogram []BinCount `json:"histogram"`
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
}

type Point struct {
	At       time.Time `json:"at"`
	Count    int64     `json:"count"`
	P50      *Quantile `json:"p50"`
	P95      *Quantile `json:"p95"`
	P95Ratio *float64  `json:"p95Ratio"`
}

type Series struct {
	Key    string  `json:"key"`
	Points []Point `json:"points"`
}

type Quality struct {
	Incomplete     int64 `json:"incomplete"`
	Invalid        int64 `json:"invalid"`
	UnknownOutcome int64 `json:"unknownOutcome"`
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

type Repository interface {
	Sources(context.Context) ([]Source, error)
	AddSource(context.Context, SourceInput) (Source, error)
	DeleteSource(context.Context, string) error
	Analyze(context.Context, Query) (Analysis, error)
	Requests(context.Context, PageInput) (Page, error)
	Payload(context.Context, PayloadInput) (Payload, error)
	Close(context.Context) error
}
