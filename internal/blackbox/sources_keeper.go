package blackbox

import (
	"context"
	"sync"
	"time"
)

// SourcesKeeper keeps the sources manifest fresh: it looks once shortly after start and then every sourcesRefreshMin
// minutes, writes sources.json when something changed and holds the last answer for /sources. The work is a few folder
// listings and small settings files, never a log line, and it runs on its own goroutine so a slow share or a huge folder
// cannot delay a recording tick.
type SourcesKeeper struct {
	dir     string
	cfg     Config
	env     SourcesEnv
	host    string
	version string
	now     func() time.Time
	diag    *DiagLog

	mu   sync.RWMutex
	doc  SourcesDoc
	have bool
}

// NewSourcesKeeper builds the keeper. dir is the recordings folder.
func NewSourcesKeeper(dir string, cfg Config, env SourcesEnv, host, version string, now func() time.Time, diag *DiagLog) *SourcesKeeper {
	if now == nil {
		now = time.Now
	}
	return &SourcesKeeper{dir: dir, cfg: cfg, env: env, host: host, version: version, now: now, diag: diag}
}

// Doc is the last manifest, false before the first look.
func (k *SourcesKeeper) Doc() (SourcesDoc, bool) {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return k.doc, k.have
}

// Refresh looks now. A failure to write the file is logged and the in-memory answer is kept, so /sources still works.
func (k *SourcesKeeper) Refresh() {
	defer func() {
		if r := recover(); r != nil {
			k.diag.Line("sources", "panic", "recovered")
		}
	}()
	doc := DiscoverSources(k.env, k.cfg, k.host, k.version, k.now())
	k.mu.Lock()
	k.doc, k.have = doc, true
	k.mu.Unlock()
	if err := WriteSourcesFile(k.dir, doc); err != nil {
		k.diag.Line("sources", "write", "failed")
		return
	}
	k.diag.Line("sources", "entries", len(doc.Sources))
}

// Run blocks until ctx is done. The first look waits a little so it never competes with the start-up of the recorder.
func (k *SourcesKeeper) Run(ctx context.Context) {
	t := time.NewTimer(20 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			k.Refresh()
			t.Reset(sourcesRefreshMin * time.Minute)
		}
	}
}
