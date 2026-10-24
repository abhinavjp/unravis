// Package applog is the shared core of the application log importers
// (log4net, Serilog, MEL, ANCM and node). Card W2-a owns it; W2-F lands this
// file with the API as signatures and no-op bodies so the format cards
// compile before the core exists (plan §3 W2-F, W2-a).
package applog

import (
	"time"

	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

// Attribute names an app-log event may carry (plan §2.2). They match the
// export policy of every app-log source.
const (
	AttrLevel      = "level"
	AttrLogger     = "logger"
	AttrException  = "exception"
	AttrFamily     = "family"
	AttrTimeoutMs  = "timeout-ms"
	AttrQs         = "qs"
	AttrQu         = "qu"
	AttrIn         = "in"
	AttrWorkerBusy = "worker-busy"
	AttrWorkerMin  = "worker-min"
	AttrIOCPBusy   = "iocp-busy"
	AttrIOCPMin    = "iocp-min"
	AttrStatus     = "status"
	AttrMethod     = "method"
	AttrCommand    = "command"
	AttrErrorCode  = "error-code"
)

// Event kinds the core emits (plan §2.3).
const (
	KindLogEntry      = "log-entry"
	KindLogRate       = "log-rate"
	KindAppRequest    = "app-request"
	KindAppStart      = "app-start"
	KindAppStop       = "app-stop"
	KindHeartbeatLate = "heartbeat-late"
)

// Families Classify returns (plan §2.2).
const (
	FamilyRedisTimeout          = "redis-timeout"
	FamilyRedisConnection       = "redis-connection"
	FamilySQLPoolTimeout        = "sql-pool-timeout"
	FamilySQLExecTimeout        = "sql-exec-timeout"
	FamilyMongoSelectionTimeout = "mongo-selection-timeout"
	FamilyMongoPoolWait         = "mongo-pool-wait"
	FamilyHTTPClientTimeout     = "http-client-timeout"
	FamilyHeartbeatLate         = "heartbeat-late"
	FamilyRequestFinished       = "request-finished"
	FamilyAppStart              = "app-start"
	FamilyAppStop               = "app-stop"
	FamilyOutOfMemory           = "out-of-memory"
	FamilyThreadAbort           = "thread-abort"
	FamilyTaskCanceled          = "task-canceled"
	FamilyOther                 = "other"
)

// SuppliedRequestMinMs is the owner-stated threshold for app-request
// intervals, in milliseconds (default 1000).
const SuppliedRequestMinMs = "app-request-min-ms"

var allowedAttrs = []string{AttrLevel, AttrLogger, AttrException, AttrFamily, AttrTimeoutMs,
	AttrQs, AttrQu, AttrIn, AttrWorkerBusy, AttrWorkerMin, AttrIOCPBusy, AttrIOCPMin,
	AttrStatus, AttrMethod, AttrCommand, AttrErrorCode}

// AllowedAttrs returns a fresh set of the attribute names above.
func AllowedAttrs() map[string]bool {
	out := make(map[string]bool, len(allowedAttrs))
	for _, a := range allowedAttrs {
		out[a] = true
	}
	return out
}

// Record is one parsed log entry handed to a Sink. Message and Exception are
// zeroed after Add; nothing in a Record reaches an event except through the
// family captures.
type Record struct {
	Time               time.Time
	Rule               string
	Level, Category    string
	EventID            int
	Message, Exception string
	Num                map[string]float64
}

// Sink turns Records into events for one source.
type Sink struct{}

// NewSink returns a Sink for source src. W2-a fills in the body.
func NewSink(src string, opts importer.Options, emit func(evidence.Event) error) *Sink {
	return &Sink{}
}

// Add classifies and emits one Record read at line. W2-a fills in the body.
func (s *Sink) Add(r Record, line int64) error { return nil }

// Finish flushes log-rate and adds the gaps to rep. W2-a fills in the body.
func (s *Sink) Finish(rep *importer.Report) error { return nil }

// Classify returns the family of an entry and the numbers it captures.
// W2-a fills in the body.
func Classify(category string, eventID int, msg, exc string) (family string, num map[string]float64) {
	return "", nil
}

// ExceptionTypes returns the exception types named in exc, with every type
// outside the framework rule written as "app-exception". W2-a fills in the body.
func ExceptionTypes(exc string) []string { return nil }

// LoggerLabel returns the logger name to store: a framework name as it is,
// anything else keyed through ps. W2-a fills in the body.
func LoggerLabel(cat string, ps func(kind, v string) string) (string, error) { return "", nil }
