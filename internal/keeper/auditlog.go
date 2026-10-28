package keeper

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// AuditEntry is one line of the keeper's audit log: who asked for what, on which saved record, and how it went. Arguments are never
// written: no password, no key, no address, no query window. Record is the saved record's id (an opaque random id).
type AuditEntry struct {
	Time    time.Time `json:"time"`
	SID     string    `json:"sid"`
	Name    string    `json:"name,omitempty"`
	PID     uint32    `json:"pid"`
	Session uint32    `json:"session"`
	Op      string    `json:"op"`
	Record  string    `json:"record,omitempty"`
	Outcome string    `json:"outcome"`
	// Repeated, on a summary line, is how many more times the line before it (same caller, request and refusal) happened within a
	// minute; such repeats are counted instead of written one by one.
	Repeated int `json:"repeated,omitempty"`
}

// noisy reports whether an entry is a refusal that a loop of requests could repeat thousands of times a minute.
func noisy(e AuditEntry) bool {
	switch e.Outcome {
	case CodeRateLimited, CodeBusy, CodeInvalid, CodeVersion, CodeUnknownOp:
		return true
	}
	return e.Op == "?"
}

// burst is a noisy line already written in the current minute, and how many repeats of it were held back.
type burst struct {
	first AuditEntry
	start time.Time
	n     int
}

const (
	burstWindow = time.Minute
	maxBursts   = 256
)

// AuditLog writes NDJSON lines to a file and rotates it once it passes MaxBytes, keeping AuditKeep older files (<name>.1 is the newest).
// Refusals a loop could repeat (busy, rate-limited, unidentified) are written once per caller, request and minute, then counted, so one
// person cannot push everyone's earlier lines out of the log.
type AuditLog struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	w        io.Writer // tests
	now      func() time.Time
	bursts   map[string]*burst
}

// DefaultAuditMaxBytes is the size at which the log rotates.
const DefaultAuditMaxBytes = 4 << 20

// AuditKeep is how many rotated files are kept.
const AuditKeep = 8

// NewAuditLog writes to path. An empty path writes nowhere.
func NewAuditLog(path string, maxBytes int64) *AuditLog {
	if maxBytes <= 0 {
		maxBytes = DefaultAuditMaxBytes
	}
	return &AuditLog{path: path, maxBytes: maxBytes, now: time.Now, bursts: map[string]*burst{}}
}

// NewAuditWriter writes to w (tests).
func NewAuditWriter(w io.Writer) *AuditLog {
	return &AuditLog{w: w, now: time.Now, bursts: map[string]*burst{}}
}

// Write records one entry. A log that cannot be written does not stop the keeper.
func (l *AuditLog) Write(e AuditEntry) {
	if l == nil {
		return
	}
	now := l.now().UTC()
	if e.Time.IsZero() {
		e.Time = now
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.flushBursts(now, false)
	if noisy(e) {
		key := fmt.Sprintf("%s|%d|%d|%s|%s", e.SID, e.PID, e.Session, e.Op, e.Outcome)
		if len(l.bursts) >= maxBursts {
			key = "*" // too many different callers at once: fold them together
		}
		if b, ok := l.bursts[key]; ok {
			b.n++
			return
		}
		l.bursts[key] = &burst{first: e, start: now}
	}
	l.writeLocked(e)
}

// Flush writes the counts of repeats still held back (on stop).
func (l *AuditLog) Flush() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.flushBursts(l.now().UTC(), true)
}

func (l *AuditLog) flushBursts(now time.Time, all bool) {
	for key, b := range l.bursts {
		if !all && now.Sub(b.start) < burstWindow {
			continue
		}
		delete(l.bursts, key)
		if b.n > 0 {
			sum := b.first
			sum.Time, sum.Repeated = now, b.n
			l.writeLocked(sum)
		}
	}
}

func (l *AuditLog) writeLocked(e AuditEntry) {
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	b = append(b, '\n')
	if l.w != nil {
		_, _ = l.w.Write(b)
		return
	}
	if l.path == "" {
		return
	}
	if info, err := os.Stat(l.path); err == nil && info.Size()+int64(len(b)) > l.maxBytes {
		_ = os.Remove(fmt.Sprintf("%s.%d", l.path, AuditKeep))
		for i := AuditKeep - 1; i >= 1; i-- {
			_ = os.Rename(fmt.Sprintf("%s.%d", l.path, i), fmt.Sprintf("%s.%d", l.path, i+1))
		}
		_ = os.Rename(l.path, l.path+".1")
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	_, _ = f.Write(b)
	_ = f.Close()
}
