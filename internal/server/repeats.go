package server

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"sync"
	"time"

	"api-audit-explorer/internal/audit"
)

// This counter measures how often an analysis is asked for again. It decides
// whether a server-side analysis cache is worth building, so it keeps nothing
// but SHA-256 digests of the query, in RAM, and writes one contents-free line at
// shutdown. It never stores or logs source names, times or any result.
const (
	repeatMemory = 8
	repeatWindow = 10 * time.Minute
)

type repeatKey [sha256.Size]byte

type repeatSeen struct {
	key repeatKey
	at  time.Time
}

type analyzeRepeats struct {
	mu      sync.Mutex
	now     func() time.Time
	recent  []repeatSeen // most recently seen first, at most repeatMemory keys
	total   int          // counted analyses
	repeats int          // counted analyses whose key was seen within repeatWindow
}

// observe counts one valid analyze request. Age-range queries are never counted:
// their window is measured against the server clock, so the same request is not
// the same data twice.
func (c *analyzeRepeats) observe(q audit.Query) {
	if c == nil || q.HasAgeRange() {
		return
	}
	key, ok := analyzeKey(q)
	if !ok {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	if c.now != nil {
		now = c.now()
	}
	c.total++
	found := -1
	for i, seen := range c.recent {
		if seen.key == key {
			found = i
			break
		}
	}
	if found >= 0 {
		if now.Sub(c.recent[found].at) < repeatWindow {
			c.repeats++
		}
		c.recent = append(c.recent[:found], c.recent[found+1:]...)
	}
	c.recent = append([]repeatSeen{{key: key, at: now}}, c.recent...)
	if len(c.recent) > repeatMemory {
		c.recent = c.recent[:repeatMemory]
	}
}

// summary is the single line written at shutdown: repeats of the counted analyses.
func (c *analyzeRepeats) summary() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return fmt.Sprintf("analyze repeats: %d of %d", c.repeats, c.total)
}

// analyzeKey digests the query as the analysis sees it: operation folded, source
// order ignored, times in UTC. Anything the digest cannot encode is not counted.
func analyzeKey(q audit.Query) (repeatKey, bool) {
	n := q.Normalized()
	n.SourceIDs = append([]string(nil), n.SourceIDs...)
	sort.Strings(n.SourceIDs)
	n.Start, n.End = n.Start.UTC(), n.End.UTC()
	n.BaselineStart, n.BaselineEnd = n.BaselineStart.UTC(), n.BaselineEnd.UTC()
	data, err := json.Marshal(n)
	if err != nil {
		return repeatKey{}, false
	}
	return sha256.Sum256(data), true
}

// LogAnalyzeRepeats writes the repeat summary for a handler returned by New. The
// dashboard calls it once, as it shuts down.
func LogAnalyzeRepeats(served http.Handler) {
	if h, ok := served.(*handler); ok && h != nil {
		log.Print(h.repeats.summary())
	}
}
