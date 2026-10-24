package resource

import (
	"sort"
	"strings"
	"time"

	"api-audit-explorer/internal/evidence"
)

// Marker edges.
const (
	EdgeStart   = "start"
	EdgeStop    = "stop"
	EdgeRestart = "restart" // a stop and a start together (a recycle, a reboot)
)

// Derived marker kinds; the other kinds are the event kinds they come from.
const (
	KindNewPID      = "new-pid"
	KindUptimeReset = "uptime-reset"
)

// Marker is a start or stop of a process, service, application or host. A
// point marker has Start == End; an interval marker says the change happened
// somewhere in [Start, End] (process-gone, new-pid, uptime-reset).
type Marker struct {
	Kind        string
	Edge        string
	Host        string
	Proc        Proc // zero when the marker names no process
	Start, End  time.Time
	Source      string
	EvidenceIDs []string
	Text        string // plain description, no causal wording
}

// eventMarkerKinds maps an event kind to its edge and description.
var eventMarkerKinds = map[string]struct{ edge, text string }{
	"apppool-recycle":          {EdgeRestart, "Application pool recycle event"},
	"apppool-idle-shutdown":    {EdgeStop, "Application pool idle shutdown event"},
	"worker-process-failure":   {EdgeStop, "Worker process failure event"},
	"host-unexpected-shutdown": {EdgeRestart, "Unexpected shutdown event"},
	"app-start":                {EdgeStart, "Application start entry"},
	"app-session":              {EdgeStart, "Application session entry"},
	"app-stop":                 {EdgeStop, "Application stop entry"},
	"process-start":            {EdgeStart, "Process start"},
	"process-gone":             {EdgeStop, "Process no longer listed"},
	"redis-start":              {EdgeStart, "Redis start"},
	"redis-stop":               {EdgeStop, "Redis stop"},
	"mongod-start":             {EdgeStart, "mongod start"},
	"mongod-stop":              {EdgeStop, "mongod stop"},
}

// eventMarker turns a marker event into a Marker.
func eventMarker(ev evidence.Event) (Marker, bool) {
	k, ok := eventMarkerKinds[ev.Kind]
	if !ok {
		return Marker{}, false
	}
	m := Marker{Kind: ev.Kind, Edge: k.edge, Host: ev.Host, Start: ev.Start, End: ev.End, Source: ev.Source,
		EvidenceIDs: []string{ev.EvidenceID}}
	text := k.text
	switch ev.Kind {
	case "process-start", "process-gone":
		m.Proc = attrIdentity(ev)
		if key := m.Proc.Key(); key != "" {
			text += " for " + key
		}
	case "apppool-recycle":
		if r := ev.Attrs["reason"]; r != "" {
			text += " (reason: " + r + ")"
		}
	case "worker-process-failure":
		if id := ev.Attrs["id"]; id != "" {
			text += " (event id " + id + ")"
		}
	}
	m.Text = withHost(text, ev.Host)
	return m, true
}

func withHost(text, host string) string {
	if host == "" {
		return text
	}
	return text + " on " + host
}

// markers merges the event markers with the derived ones: a new pid for an
// image already seen on the host, and a drop in an uptime-like value. A
// derived marker is left out when an event marker already records the same
// change (a process-start for that pid, a mongod-start or redis-start in the
// interval).
func (b *builder) markers(series, uptimes []Series) []Marker {
	out := append([]Marker(nil), b.evMarks...)
	out = append(out, b.newPIDMarkers(series)...)
	out = append(out, b.uptimeMarkers(uptimes)...)
	sort.SliceStable(out, func(i, j int) bool {
		a, c := out[i], out[j]
		if !a.Start.Equal(c.Start) {
			return a.Start.Before(c.Start)
		}
		if a.Host != c.Host {
			return a.Host < c.Host
		}
		if a.Kind != c.Kind {
			return a.Kind < c.Kind
		}
		return a.Proc.Key() < c.Proc.Key()
	})
	return out
}

// hasStart reports whether a process-start is recorded for host, name and pid.
func (b *builder) hasStart(host, name string, pid int) bool {
	return len(b.starts[startKey(host, name, pid)]) > 0
}

// newPIDMarkers finds, per host and image, each pid first seen after the image
// was first seen on the host. Images labelled "other" are many images under
// one label and are skipped.
func (b *builder) newPIDMarkers(series []Series) []Marker {
	type firstSeen struct {
		at  Sample
		src string
		p   Proc
	}
	type image struct {
		first time.Time
		pids  map[int]*firstSeen
	}
	images := map[string]*image{} // host|name
	var keys []string
	for _, s := range series {
		p := s.Proc
		if p.PID == 0 || p.Name == "" || strings.EqualFold(p.Name, "other") || len(s.Samples) == 0 {
			continue
		}
		k := s.Host + "\x00" + strings.ToLower(p.Name)
		im, ok := images[k]
		if !ok {
			im = &image{pids: map[int]*firstSeen{}}
			images[k] = im
			keys = append(keys, k)
		}
		sm := s.Samples[0]
		if im.first.IsZero() || sm.End.Before(im.first) {
			im.first = sm.End
		}
		if f, ok := im.pids[p.PID]; !ok || sm.End.Before(f.at.End) {
			im.pids[p.PID] = &firstSeen{at: sm, src: s.Source, p: Proc{Name: p.Name, PID: p.PID}}
		}
	}
	sort.Strings(keys)
	var out []Marker
	for _, k := range keys {
		im := images[k]
		host, _, _ := strings.Cut(k, "\x00")
		for pid, f := range im.pids {
			if !f.at.End.After(im.first) || b.hasStart(host, f.p.Name, pid) {
				continue
			}
			if !b.w.Contains(f.at.End) {
				continue
			}
			out = append(out, Marker{Kind: KindNewPID, Edge: EdgeStart, Host: host, Proc: f.p,
				Start: f.at.Start, End: f.at.End, Source: f.src, EvidenceIDs: []string{f.at.EvidenceID},
				Text: withHost("New process id for "+f.p.Name+" ("+f.p.Key()+" first seen)", host)})
		}
	}
	return out
}

// uptimeMarkers finds each drop in an uptime-like series.
func (b *builder) uptimeMarkers(uptimes []Series) []Marker {
	var out []Marker
	for _, s := range uptimes {
		for i := 1; i < len(s.Samples); i++ {
			prev, cur := s.Samples[i-1], s.Samples[i]
			if cur.Value >= prev.Value {
				continue
			}
			if b.recorded(s, prev.End, cur.End) {
				continue
			}
			what := s.Label
			if what == "" {
				what = "uptime"
			}
			text := what + " dropped"
			if key := s.Proc.Key(); key != "" {
				text += " for " + key
			}
			out = append(out, Marker{Kind: KindUptimeReset, Edge: EdgeRestart, Host: s.Host, Proc: s.Proc,
				Start: prev.End, End: cur.End, Source: s.Source,
				EvidenceIDs: []string{prev.EvidenceID, cur.EvidenceID}, Text: withHost(text, s.Host)})
		}
	}
	return out
}

// recorded reports whether an event marker already records the restart an
// uptime drop in s between from and to shows.
func (b *builder) recorded(s Series, from, to time.Time) bool {
	var kinds []string
	switch s.Source {
	case sourceMongoSS:
		kinds = []string{"mongod-start"}
	case sourceRedisInfo:
		kinds = []string{"redis-start"}
	default:
		if s.Proc.PID > 0 {
			for _, t := range b.starts[startKey(s.Host, s.Proc.Name, s.Proc.PID)] {
				if !t.Before(from) && !t.After(to) {
					return true
				}
			}
			return false
		}
		if s.Proc.IsZero() {
			kinds = []string{"host-unexpected-shutdown"}
		}
	}
	for _, m := range b.evMarks {
		if m.Host != s.Host || m.Start.Before(from) || m.Start.After(to) {
			continue
		}
		for _, k := range kinds {
			if m.Kind == k {
				return true
			}
		}
	}
	return false
}
