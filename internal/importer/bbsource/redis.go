package bbsource

import (
	"encoding/json"
	"math"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/redis"
	"api-audit-explorer/internal/importer/wincsv"
)

// The Redis group of a sample (internal/blackbox/redis.go). It is read exactly as the Redis importers read an INFO file, a
// slow log and a latency history: the same event kinds, the same "metric", "command", "id" and "event" attributes, the same
// units and scales (redis.InfoMetrics is the one list), so a lane that holds both never mixes meanings. No key, no key
// argument, no client address and no word of the recording other than a fixed table spelling is ever read.

// Event kinds that come from the Redis group (the Redis importers' own kind names).
const (
	KindRedisMetric  = redis.KindMetric
	KindRedisStart   = redis.KindRedisStart
	KindSlowCommand  = redis.KindSlowCommand
	KindLatencySpike = redis.KindLatencySpike
)

// Index bases of the events of the Redis group on one line, so their ids never meet another kind's.
const (
	idxRedisMetric  = 1 << 16
	idxRedisSlow    = 1 << 17
	idxRedisLatency = 1 << 18
	idxRedisStart   = 1<<18 + 1<<17
)

const (
	maxSlowPerSample    = 128
	maxLatencyPerSample = 32
	maxRedisUptime      = 4e9
	maxSlowUsec         = 1e12 // about 11 days; anything above is not a reading
)

type redisSlow struct {
	ID   int64  `json:"id"`
	Unix int64  `json:"t"`
	Usec int64  `json:"usec"`
	Cmd  string `json:"cmd"`
}

type redisLatency struct {
	Event string `json:"event"`
	Unix  int64  `json:"t"`
	Last  int64  `json:"ms"`
}

type redisGroup struct {
	State   string             `json:"state"`
	Numbers map[string]float64 `json:"numbers"`
	Words   map[string]string  `json:"words"`
	Slow    []redisSlow        `json:"slow"`
	Latency []redisLatency     `json:"latency"`
}

// redisState is what the Redis group needs from one sample to the next in one file.
type redisState struct {
	prevT    time.Time
	prevCPU  map[string]float64
	prevUp   float64
	haveUp   bool
	slowSeen map[[3]int64]struct{}
	latSeen  map[latKey]struct{}
}

type latKey struct {
	event string
	t, ms int64
}

func newRedisState() *redisState {
	return &redisState{prevCPU: map[string]float64{}, slowSeen: map[[3]int64]struct{}{}, latSeen: map[latKey]struct{}{}}
}

var redisMetrics = redis.InfoMetrics()

// seenCap bounds the maps that keep what was already read, so a damaged file cannot grow them without limit.
const seenCap = 100000

// beforeFileMargin: a slow command or a latency spike that ended more than this before the first sample of a file belongs to
// the file before it (the recorder reads the Redis latency list every minute and re-reads its slow ring after a restart),
// so it is left out of this one and is not shown twice.
const beforeFileMargin = 5 * time.Minute

// redisEvents makes the events of the Redis group of one sample. A figure, slow command or latency spike that is not a
// valid reading is left out by itself and the report says so; it never costs the rest of the sample.
func redisEvents(raw json.RawMessage, t time.Time, no int64, opts importer.Options, host string, nt *notes) []evidence.Event {
	var evs []evidence.Event
	var g redisGroup
	if json.Unmarshal(raw, &g) != nil {
		nt.damaged = true
		return nil
	}
	nt.redisSeen = true
	st := nt.redis
	if st == nil {
		st = newRedisState()
		nt.redis = st
	}
	mk := func(kind string, idx int, attrs map[string]string) evidence.Event {
		e := wincsv.NewEvent(opts, Name, no, idx)
		e.Kind, e.Host, e.TZRule, e.Attrs = kind, host, clock.RuleUTCField, attrs
		e.Start, e.End = t, t
		return e
	}
	role := redis.CanonicalRole(g.Words["role"])
	if g.State == "ok" {
		dt := t.Sub(st.prevT).Seconds()
		// A restart is read from the uptime going down; the CPU rate across it is not a reading.
		restarted := false
		var up float64
		haveUp := false
		if u, have := g.Numbers["uptime_in_seconds"]; have && validNumber(u) && u <= maxRedisUptime {
			up, haveUp = u, true
			restarted = st.haveUp && up < st.prevUp
		}
		cpuNow := map[string]float64{}
		for i, m := range redisMetrics {
			v, have := g.Numbers[m.Key]
			if !have {
				continue
			}
			if !validNumber(v) || v > m.Max {
				nt.damaged = true
				continue
			}
			attrs := map[string]string{"metric": m.Key}
			if role != "" {
				attrs["role"] = role
			}
			if m.CPU {
				cpuNow[m.Key] = v
				prev, had := st.prevCPU[m.Key]
				if !had || restarted || dt <= 0 || v < prev {
					continue // the first reading, or a counter that started again: no rate
				}
				rate := (v - prev) / dt * 100
				if math.IsNaN(rate) || math.IsInf(rate, 0) {
					continue
				}
				e := mk(KindRedisMetric, idxRedisMetric+i, attrs)
				e.Value, e.Unit = &rate, m.Unit
				evs = append(evs, e)
				continue
			}
			x := v / m.Div
			e := mk(KindRedisMetric, idxRedisMetric+i, attrs)
			e.Value, e.Unit = &x, m.Unit
			evs = append(evs, e)
		}
		st.prevCPU, st.prevT = cpuNow, t
		if haveUp {
			if restarted {
				at := t.Add(-time.Duration(up * float64(time.Second)))
				attrs := map[string]string{}
				if role != "" {
					attrs["role"] = role
				}
				e := mk(KindRedisStart, idxRedisStart, attrs)
				e.Start, e.End, e.Severity = at, at, "info"
				evs = append(evs, e)
			}
			st.prevUp, st.haveUp = up, true
		}
	}
	slow := g.Slow
	if len(slow) > maxSlowPerSample {
		slow, nt.cut = slow[:maxSlowPerSample], true
	}
	for i, s := range slow {
		if s.ID < 0 || s.Usec < 0 || s.Usec > maxSlowUsec || s.Unix <= 0 {
			nt.damaged = true
			continue
		}
		end := time.Unix(s.Unix, 0).UTC()
		if !wincsv.SaneTime(end) {
			nt.damaged = true
			continue
		}
		if end.Before(nt.firstT.Add(-beforeFileMargin)) {
			continue // the file before this one holds it
		}
		key := [3]int64{s.ID, s.Unix, s.Usec}
		if _, dup := st.slowSeen[key]; dup {
			continue // the same entry, seen in an earlier sample
		}
		if len(st.slowSeen) < seenCap {
			st.slowSeen[key] = struct{}{}
		} else {
			nt.cut = true
		}
		d := float64(s.Usec) / 1000
		e := mk(KindSlowCommand, idxRedisSlow+i, map[string]string{"command": redis.CanonicalCommand(s.Cmd), "id": itoa(s.ID)})
		e.Start, e.End, e.DurationMs, e.Unit, e.Severity = end.Add(-time.Duration(s.Usec)*time.Microsecond), end, &d, "ms", "info"
		evs = append(evs, e)
	}
	lat := g.Latency
	if len(lat) > maxLatencyPerSample {
		lat, nt.cut = lat[:maxLatencyPerSample], true
	}
	for i, l := range lat {
		if l.Last < 0 || l.Last > 1e9 || l.Unix <= 0 {
			nt.damaged = true
			continue
		}
		at := time.Unix(l.Unix, 0).UTC()
		if !wincsv.SaneTime(at) {
			nt.damaged = true
			continue
		}
		if at.Before(nt.firstT.Add(-beforeFileMargin)) {
			continue
		}
		ev := redis.CanonicalLatencyEvent(l.Event)
		key := latKey{ev, l.Unix, l.Last}
		if _, dup := st.latSeen[key]; dup {
			continue
		}
		if len(st.latSeen) < seenCap {
			st.latSeen[key] = struct{}{}
		} else {
			nt.cut = true
		}
		v := float64(l.Last)
		e := mk(KindLatencySpike, idxRedisLatency+i, map[string]string{"event": ev})
		e.Start, e.End, e.Value, e.Unit = at, at, &v, "ms"
		evs = append(evs, e)
	}
	return evs
}
