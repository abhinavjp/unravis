package blackbox

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// BBX-4-01 Windows event log tailer: IDs and numbers only. A fixed allowlist
// of disk/commit/port-exhaustion/WAS-recycle/crash events is queried per tick
// behind a watermark that starts at now (no historical backfill). Never any
// message text, EventData strings, account names or process paths.

func init() { RegisterGroup("events", true, newEventsGroup) }

// maxEventsPerTick caps both the rows read and the entries emitted per tick.
const maxEventsPerTick = 256

// eventsChannels is the fixed channel enum: 1=System, 2=Application.
var eventsChannels = []struct {
	num  uint8
	name string
}{{1, "System"}, {2, "Application"}}

// eventsDiskIDs is the shared disk/Ntfs/volmgr allowlist.
var eventsDiskIDs = []uint32{7, 9, 11, 15, 51, 55, 57, 98, 129, 140, 153}

// eventsAllowlist is the fixed (channel, provider, IDs) table. WAS and
// Resource-Exhaustion-Detector use their canonical provider names.
var eventsAllowlist = []struct {
	channel  uint8
	provider string
	ids      []uint32
}{
	{1, "disk", eventsDiskIDs},
	{1, "Ntfs", eventsDiskIDs},
	{1, "volmgr", eventsDiskIDs},
	{1, "Microsoft-Windows-Resource-Exhaustion-Detector", []uint32{2004}},
	{1, "Microsoft-Windows-WAS", []uint32{5002, 5009, 5011, 5013, 5021, 5057, 5074, 5079, 5080, 5117, 5186}},
	{2, "Application Error", []uint32{1000, 1001, 1002}},
	{2, ".NET Runtime", []uint32{1026}},
}

// rawEvent is one allowlisted event: numeric fields only, never text.
type rawEvent struct {
	Channel uint8
	EventID uint32
	Level   uint8
	T       time.Time
}

// eventsXPath builds the channel query: allowlisted (provider, EventID) pairs
// inside the watermark window. Numbers and fixed provider names only.
func eventsXPath(channel uint8, windowMS int64) string {
	var b strings.Builder
	b.WriteString("*[System[(")
	first := true
	for _, a := range eventsAllowlist {
		if a.channel != channel {
			continue
		}
		if !first {
			b.WriteString(" or ")
		}
		first = false
		fmt.Fprintf(&b, "(Provider[@Name='%s'] and (", a.provider)
		for i, id := range a.ids {
			if i > 0 {
				b.WriteString(" or ")
			}
			fmt.Fprintf(&b, "EventID=%d", id)
		}
		b.WriteString("))")
	}
	fmt.Fprintf(&b, ") and TimeCreated[timediff(@SystemTime) <= %d]]]", windowMS)
	return b.String()
}

// aggregateEvents groups rows newer than since by (channel, eventID, level),
// counts them, keeps the latest T, sorts by T desc and caps the entries.
func aggregateEvents(rows []rawEvent, since time.Time) []EventEntry {
	type key struct {
		ch    uint8
		id    uint32
		level uint8
	}
	byKey := make(map[key]*EventEntry)
	for _, r := range rows {
		if !r.T.After(since) {
			continue // at or before the watermark: already counted
		}
		k := key{r.Channel, r.EventID, r.Level}
		e := byKey[k]
		if e == nil {
			e = &EventEntry{Channel: r.Channel, EventID: r.EventID, Level: r.Level}
			byKey[k] = e
		}
		e.Count++
		if r.T.After(e.T) {
			e.T = r.T
		}
	}
	out := make([]EventEntry, 0, len(byKey))
	for _, e := range byKey {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].T.Equal(out[j].T) {
			return out[i].T.After(out[j].T)
		}
		if out[i].Channel != out[j].Channel {
			return out[i].Channel < out[j].Channel
		}
		return out[i].EventID < out[j].EventID
	})
	if len(out) > maxEventsPerTick {
		out = out[:maxEventsPerTick]
	}
	return out
}

// eventsGroup tails the log behind a stored watermark. query is injected so
// tests run without Windows; production uses queryEvents (per-OS twin).
type eventsGroup struct {
	query     func(since time.Time) ([]rawEvent, error)
	watermark time.Time
}

func newEventsGroup(GroupDeps) (Group, error) {
	if err := eventsSupported(); err != nil {
		return nil, err // no wevtapi (non-Windows): group skipped
	}
	return &eventsGroup{query: queryEvents}, nil
}

func (g *eventsGroup) Name() string   { return "events" }
func (g *eventsGroup) Optional() bool { return true }

func (g *eventsGroup) Collect(now time.Time, dt float64) (any, error) {
	if g.watermark.IsZero() {
		g.watermark = now // start at now: no historical backfill on restart
	}
	rows, err := g.query(g.watermark)
	if err != nil {
		return nil, err // access denied etc: group omitted, never 0
	}
	entries := aggregateEvents(rows, g.watermark)
	for _, r := range rows {
		if r.T.After(g.watermark) {
			g.watermark = r.T // advance so nothing is re-read
		}
	}
	if len(entries) == 0 {
		return nil, nil // no events: group omitted, never 0
	}
	return &EventsGroup{Events: entries}, nil
}
