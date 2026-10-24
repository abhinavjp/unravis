package mongoexports

import (
	"encoding/json"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

var serverStatusAttrs = []string{"metric"}

const mib = 1 << 20

// ssMetric is one projected serverStatus value: the metric name the export lists, the
// path of the value in the document and how it is read. MiB values become bytes.
type ssMetric struct {
	name  string
	path  []string
	unit  string
	scale float64
}

// serverStatusMetrics is the fixed projection (the owner's mongosh recipe projects
// exactly these paths; wave plan W2-k). Field names are from MongoDB's serverStatus
// documentation. Opcounters are cumulative counters and stay raw.
var serverStatusMetrics = []ssMetric{
	{"uptime", []string{"uptime"}, "count", 1},
	{"connections.current", []string{"connections", "current"}, "count", 1},
	{"connections.available", []string{"connections", "available"}, "count", 1},
	{"globalLock.currentQueue.total", []string{"globalLock", "currentQueue", "total"}, "count", 1},
	{"globalLock.currentQueue.readers", []string{"globalLock", "currentQueue", "readers"}, "count", 1},
	{"globalLock.currentQueue.writers", []string{"globalLock", "currentQueue", "writers"}, "count", 1},
	{"globalLock.activeClients.readers", []string{"globalLock", "activeClients", "readers"}, "count", 1},
	{"globalLock.activeClients.writers", []string{"globalLock", "activeClients", "writers"}, "count", 1},
	{"opcounters.query", []string{"opcounters", "query"}, "count", 1},
	{"opcounters.insert", []string{"opcounters", "insert"}, "count", 1},
	{"opcounters.update", []string{"opcounters", "update"}, "count", 1},
	{"opcounters.delete", []string{"opcounters", "delete"}, "count", 1},
	{"opcounters.getmore", []string{"opcounters", "getmore"}, "count", 1},
	{"opcounters.command", []string{"opcounters", "command"}, "count", 1},
	{"mem.resident", []string{"mem", "resident"}, "bytes", mib},
	{"mem.virtual", []string{"mem", "virtual"}, "bytes", mib},
	{"wiredTiger.cache.bytes currently in the cache", []string{"wiredTiger", "cache", "bytes currently in the cache"}, "bytes", 1},
	{"wiredTiger.cache.tracked dirty bytes in the cache", []string{"wiredTiger", "cache", "tracked dirty bytes in the cache"}, "bytes", 1},
	{"wiredTiger.cache.maximum bytes configured", []string{"wiredTiger", "cache", "maximum bytes configured"}, "bytes", 1},
	{"wiredTiger.concurrentTransactions.read.out", []string{"wiredTiger", "concurrentTransactions", "read", "out"}, "count", 1},
	{"wiredTiger.concurrentTransactions.read.available", []string{"wiredTiger", "concurrentTransactions", "read", "available"}, "count", 1},
	{"wiredTiger.concurrentTransactions.write.out", []string{"wiredTiger", "concurrentTransactions", "write", "out"}, "count", 1},
	{"wiredTiger.concurrentTransactions.write.available", []string{"wiredTiger", "concurrentTransactions", "write", "available"}, "count", 1},
}

// ssTop is the set of top-level keys read; every other key of the document is skipped.
var ssTop = set("connections", "globalLock", "opcounters", "mem", "wiredTiger")

// NewServerStatus returns the importer for a serverStatus snapshot loop (one projected
// JSON document per line).
func NewServerStatus() importer.Importer {
	return impl{
		sp: spec{name: NameServerStatus, maxEvents: 25_000_000, def: importer.Limits{
			MaxBytes: 512 << 20, MaxRows: 1_000_000, MaxLineBytes: 1 << 20, MaxFields: maxFieldsDflt,
			MaxRejectFraction: rejectFrac}},
		attrs:    serverStatusAttrs,
		detect:   detectServerStatus,
		newLiner: func() liner { return &serverStatusLiner{} },
	}
}

func detectServerStatus(head []byte, fileName string) int {
	line := firstLine(head)
	if line == nil || !hasAll(line, "localTime") || hasAny(line, "currentOpTime") {
		return 0
	}
	score := 80
	if hasAny(line, "uptime") {
		score += 5
	}
	if hasAny(line, "opcounters", "connections", "globalLock", "wiredTiger") {
		score += 5
	}
	if fileHint(fileName, "serverstatus", "server-status", "server_status") {
		score += 5
	}
	return score
}

type serverStatusLiner struct {
	base
	rule     string
	prevUp   map[string]float64   // host label -> last uptime seen
	prevTime map[string]time.Time // host label -> its time
}

func (s *serverStatusLiner) begin(lim importer.Limits, opts importer.Options, rep *importer.Report) error {
	s.setup(lim)
	s.prevUp = map[string]float64{}
	s.prevTime = map[string]time.Time{}
	rep.Rule("localTime", clock.RuleOffsetInField)
	return nil
}

// objCache holds the decoded children of the objects of one line, by path, so each object
// is decoded once however many metrics sit under it.
type objCache map[string]map[string]json.RawMessage

// sub returns the value at path inside raw, descending through objects. prefix names raw
// in the cache.
func sub(raw json.RawMessage, prefix string, path []string, cache objCache) (json.RawMessage, bool) {
	cur := raw
	for _, key := range path {
		m, seen := cache[prefix]
		if !seen {
			if len(cur) == 0 || cur[0] != '{' || json.Unmarshal(cur, &m) != nil {
				m = nil
			}
			cache[prefix] = m
		}
		next, ok := m[key]
		if !ok {
			return nil, false
		}
		cur = next
		prefix += "\x00" + key
	}
	return cur, true
}

func (s *serverStatusLiner) line(line []byte, no int64, _ time.Time, opts importer.Options, rep *importer.Report) (time.Time, []evidence.Event, importer.Reason, error) {
	var ts json.RawMessage
	var hostName string
	top := map[string]json.RawMessage{}
	var uptime json.RawMessage
	ok, tooMany, _ := forEachKey(line, s.maxKeys, func(k string, raw json.RawMessage) {
		switch {
		case k == "localTime":
			ts = raw
		case k == "host":
			if h, ok := str(raw); ok {
				hostName = h
			}
		case k == "version":
			s.noteVersion(raw)
		case k == "uptime":
			uptime = raw
		case ssTop[k]:
			top[k] = raw
		}
	})
	if !ok || tooMany {
		return time.Time{}, nil, importer.ReasonFieldCount, nil
	}
	t, rule, reason := parseMongoTime(ts)
	if reason != "" {
		return time.Time{}, nil, reason, nil
	}

	type got struct {
		idx int
		v   float64
	}
	var vals []got
	var up float64
	var hasUp bool
	cache := objCache{}
	for i, m := range serverStatusMetrics {
		var raw json.RawMessage
		var found bool
		if i == 0 {
			raw, found = uptime, len(uptime) > 0
		} else if root, ok := top[m.path[0]]; ok {
			raw, found = sub(root, m.path[0], m.path[1:], cache)
		}
		if !found {
			continue
		}
		n, ok := nonNeg(raw)
		if !ok {
			return time.Time{}, nil, importer.ReasonBadNumber, nil
		}
		vals = append(vals, got{i, n * m.scale})
		if i == 0 {
			up, hasUp = n, true
		}
	}
	if len(vals) == 0 {
		return time.Time{}, nil, importer.ReasonFieldCount, nil
	}
	label, err := s.host(hostName, opts, rep)
	if err != nil {
		return t, nil, "", err
	}
	s.rule = rule
	rep.Rule("localTime", rule)

	var evs []evidence.Event
	allowed := set(serverStatusAttrs...)
	for _, g := range vals {
		m := serverStatusMetrics[g.idx]
		ev := newEvent(opts, NameServerStatus, KindMetric, label, no, g.idx, t, t, rule)
		ev.Value, ev.Unit = fptr(g.v), m.unit
		ev.Attrs = map[string]string{"metric": m.name}
		if err := ev.Validate(allowed); err != nil {
			return time.Time{}, nil, importer.ReasonFieldCount, nil
		}
		evs = append(evs, ev)
	}

	// A drop in uptime, with the clock moving forward, is a restart. The start time is
	// the sample time less the uptime the new process reports.
	if hasUp {
		if prev, seen := s.prevUp[label]; seen && up < prev && t.After(s.prevTime[label]) {
			at := t.Add(-time.Duration(up * float64(time.Second)))
			ev := newEvent(opts, NameServerStatus, KindMongodStart, label, no, len(serverStatusMetrics), at, at, rule)
			if ev.Validate(allowed) == nil {
				evs = append(evs, ev)
			}
		}
		s.prevUp[label], s.prevTime[label] = up, t
	}
	return t, evs, "", nil
}

func (s *serverStatusLiner) end(rep *importer.Report, opts importer.Options) {
	rule := s.rule
	if rule == "" {
		rule = clock.RuleOffsetInField
	}
	rep.Gap("serverStatus samples are point-in-time values: activity between two samples is not observed")
	rep.Gap("opcounters are cumulative since the server started; rates are not in the file")
	if s.version == "" {
		rep.Gap("mongod version unknown: not in the file")
	}
	rep.Quality = quality(rep, s.version, rule)
}
