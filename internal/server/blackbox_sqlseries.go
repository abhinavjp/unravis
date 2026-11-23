package server

import (
	"sort"
	"strconv"

	"api-audit-explorer/internal/blackbox"
)

// SQL charts: the SQL service's samples (requests, waits, schedulers, memory grants, tempdb,
// file stalls, availability-group state) drawn beside the host counters. Everything charted
// is a number the service already stored (C9: counts, milliseconds, pages, ids and enum
// states; no query text, login, host, program or database name). Nothing is collected here.
//
// Keys are "sqlDmv.<kind>[.<what>].<field>". A reading the service did not take is absent,
// never 0: an empty reply (nothing running, first cycle with no baseline) leaves no point.
// The wait types and databases that get a line of their own are the busiest ones over the
// window, capped, so a long window cannot make a thousand lines.

const (
	bbDmvGroup          = "sqlDmv"
	bbSQLMaxWaitLines   = 40 // wait types with their own delta lines
	bbSQLMaxReqWaitLine = 20 // wait types with their own "requests waiting" line
	bbSQLMaxDBLines     = 20 // databases with their own file-latency lines
	sqlPageBytes        = 8192
)

// bbSQLRead is one read of the SQL samples for a window.
type bbSQLRead struct {
	Samples []blackbox.SQLSample
	Report  blackbox.SQLReadReport
}

// bbSeriesSQL says what the SQL part of the answer rests on, so the view can say why a
// SQL line is missing (service off, folder locked, recorder too old) instead of drawing nothing.
type bbSeriesSQL struct {
	Files       int  `json:"files"`
	Samples     int  `json:"samples"`
	Missing     int  `json:"missing"`
	Dropped     int  `json:"dropped"`
	Corrupt     int  `json:"corrupt"`
	Torn        int  `json:"torn"`
	Truncated   int  `json:"truncated"` // samples the service cut at its row cap
	Locked      bool `json:"locked,omitempty"`
	Unsupported bool `json:"unsupported,omitempty"`
	Limited     bool `json:"limited,omitempty"`
	Newer       bool `json:"newer,omitempty"`
}

func bbSQLSummary(in *bbSQLRead) *bbSeriesSQL {
	if in == nil {
		return nil
	}
	r := in.Report
	out := &bbSeriesSQL{Files: r.Files, Samples: len(in.Samples), Missing: r.Missing, Dropped: r.Dropped, Corrupt: r.Corrupt, Torn: r.Torn,
		Locked: r.Locked, Unsupported: r.Unsupported, Limited: r.Limited, Newer: r.Newer}
	for _, s := range in.Samples {
		if s.Truncated {
			out.Truncated++
		}
	}
	return out
}

// topKeys returns the keys of m with the biggest totals, at most n, in a fixed order.
func topKeys[K ~string | ~uint64](m map[K]float64, n int) map[K]bool {
	keys := make([]K, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if m[keys[i]] != m[keys[j]] {
			return m[keys[i]] > m[keys[j]]
		}
		return keys[i] < keys[j]
	})
	if len(keys) > n {
		keys = keys[:n]
	}
	out := make(map[K]bool, len(keys))
	for _, k := range keys {
		out[k] = true
	}
	return out
}

// addSQLSeries adds the SQL samples to the buckets and counts them in the group cover.
func addSQLSeries(bk *bbBuckets, cover map[string]*bbSeriesGroupCover, in *bbSQLRead) {
	if in == nil {
		return
	}
	c := cover[bbDmvGroup]
	// Which wait types and databases get lines of their own: the busiest over the window.
	waitMs := map[string]float64{}
	reqTasks := map[string]float64{}
	dbStall := map[uint64]float64{}
	for _, s := range in.Samples {
		for _, w := range s.Waits {
			waitMs[w.WaitType] += float64(w.DeltaMs)
		}
		for _, r := range s.Requests {
			reqTasks[r.WaitType] += float64(r.Tasks)
		}
		for _, f := range s.FileStats {
			dbStall[uint64(f.DatabaseID)] += float64(f.ReadStallMs) + float64(f.WriteStallMs)
		}
	}
	waitKeep := topKeys(waitMs, bbSQLMaxWaitLines)
	reqKeep := topKeys(reqTasks, bbSQLMaxReqWaitLine)
	dbKeep := topKeys(dbStall, bbSQLMaxDBLines)

	add := func(key string, s blackbox.SQLSample, v float64) { bk.add(bbDmvGroup+"."+key, s.T, v) }
	for _, s := range in.Samples {
		if c != nil {
			c.Read++
			if s.Truncated {
				c.Partial++
			}
		}
		switch s.Kind {
		case "requests":
			if len(s.Requests) == 0 {
				continue
			}
			var tasks, blocked, heads, maxWait, maxBlock float64
			for _, r := range s.Requests {
				tasks += float64(r.Tasks)
				blocked += float64(r.Blocked)
				heads += float64(r.HeadBlockers)
				maxWait = maxF(maxWait, float64(r.MaxWaitMs))
				maxBlock = maxF(maxBlock, float64(r.MaxBlockMs))
				if reqKeep[r.WaitType] {
					add("requestsByWait."+r.WaitType+".tasks", s, float64(r.Tasks))
				}
			}
			add("requests.tasks", s, tasks)
			add("requests.blocked", s, blocked)
			add("requests.headBlockers", s, heads)
			add("requests.maxWaitMs", s, maxWait)
			add("requests.maxBlockMs", s, maxBlock)
		case "waits":
			for _, w := range s.Waits {
				if waitKeep[w.WaitType] {
					add("waits."+w.WaitType+".deltaMs", s, float64(w.DeltaMs))
					add("waits."+w.WaitType+".deltaTasks", s, float64(w.DeltaTasks))
				}
			}
		case "schedulers":
			if s.Schedulers != nil {
				add("schedulers.currentTasks", s, float64(s.Schedulers.CurrentTasks))
				add("schedulers.runnableTasks", s, float64(s.Schedulers.RunnableTasks))
			}
		case "semaphores":
			for _, r := range s.Semaphores {
				if r.PoolID == nil || *r.PoolID < 0 || r.ResourceSemaphoreID < 0 {
					continue // an unreadable identity is missing, never pool 0
				}
				id := "pool" + strconv.Itoa(int(*r.PoolID)) + "sem" + strconv.Itoa(int(r.ResourceSemaphoreID))
				add("semaphores."+id+".grantees", s, float64(r.Grantees))
				add("semaphores."+id+".waiters", s, float64(r.Waiters))
				if r.Forced != nil {
					add("semaphores."+id+".forced", s, float64(*r.Forced))
				}
				if r.Timeouts != nil {
					add("semaphores."+id+".timeouts", s, float64(*r.Timeouts))
				}
			}
		case "tempdb":
			if s.Tempdb != nil {
				add("tempdb.internalBytes", s, float64(s.Tempdb.InternalPages)*sqlPageBytes)
				add("tempdb.userBytes", s, float64(s.Tempdb.UserPages)*sqlPageBytes)
			}
		case "filestats":
			if len(s.FileStats) == 0 {
				continue
			}
			type tot struct{ rStall, wStall, reads, writes float64 }
			var all tot
			byDB := map[uint64]*tot{}
			for _, f := range s.FileStats {
				all.rStall += float64(f.ReadStallMs)
				all.wStall += float64(f.WriteStallMs)
				all.reads += float64(f.Reads)
				all.writes += float64(f.Writes)
				db := uint64(f.DatabaseID)
				if dbKeep[db] {
					t := byDB[db]
					if t == nil {
						t = &tot{}
						byDB[db] = t
					}
					t.rStall += float64(f.ReadStallMs)
					t.wStall += float64(f.WriteStallMs)
					t.reads += float64(f.Reads)
					t.writes += float64(f.Writes)
				}
			}
			latency := func(prefix string, t tot) {
				if t.reads > 0 {
					add(prefix+"readLatencyMs", s, t.rStall/t.reads)
				}
				if t.writes > 0 {
					add(prefix+"writeLatencyMs", s, t.wStall/t.writes)
				}
			}
			latency("fileStats.", all)
			add("fileStats.reads", s, all.reads)
			add("fileStats.writes", s, all.writes)
			for db, t := range byDB {
				latency("fileStats.db"+strconv.FormatUint(db, 10)+".", *t)
			}
		case "ag":
			if len(s.AG) == 0 {
				continue
			}
			var n, notSync, syncing, synced, other float64
			for _, r := range s.AG {
				n++
				switch r.SyncState {
				case 0:
					notSync++
				case 1:
					syncing++
				case 2:
					synced++
				default:
					other++ // reverting, initializing
				}
			}
			add("ag.databases", s, n)
			add("ag.notSynchronizing", s, notSync)
			add("ag.synchronizing", s, syncing)
			add("ag.synchronized", s, synced)
			add("ag.other", s, other)
		}
	}
}

func maxF(a, b float64) float64 {
	if b > a {
		return b
	}
	return a
}
