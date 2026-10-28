package keeper

import (
	"encoding/json"
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
}

// AuditLog writes NDJSON lines to a file and rotates it once it passes MaxBytes (one older file is kept, <name>.1).
type AuditLog struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	w        io.Writer // tests
	now      func() time.Time
}

// DefaultAuditMaxBytes is the size at which the log rotates.
const DefaultAuditMaxBytes = 4 << 20

// NewAuditLog writes to path. An empty path writes nowhere.
func NewAuditLog(path string, maxBytes int64) *AuditLog {
	if maxBytes <= 0 {
		maxBytes = DefaultAuditMaxBytes
	}
	return &AuditLog{path: path, maxBytes: maxBytes, now: time.Now}
}

// NewAuditWriter writes to w (tests).
func NewAuditWriter(w io.Writer) *AuditLog { return &AuditLog{w: w, now: time.Now} }

// Write records one entry. A log that cannot be written does not stop the keeper.
func (l *AuditLog) Write(e AuditEntry) {
	if l == nil {
		return
	}
	if e.Time.IsZero() {
		e.Time = l.now().UTC()
	}
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	b = append(b, '\n')
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.w != nil {
		_, _ = l.w.Write(b)
		return
	}
	if l.path == "" {
		return
	}
	if info, err := os.Stat(l.path); err == nil && info.Size()+int64(len(b)) > l.maxBytes {
		_ = os.Remove(l.path + ".1")
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
