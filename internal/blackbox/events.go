package blackbox

import (
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// BBX-4-01 Windows event log tailer: IDs and numbers only. A fixed allowlist
// of disk/commit/port-exhaustion/WAS-recycle/crash events is queried per tick.
// Each channel keeps its own record-ID cursor (a stable identity timestamps
// cannot give) and advances forward over consumed records only, with a fair
// per-channel read budget. The read cursor is committed only when the recorder
// durably accepts the batch (W6-01/W6-06). Never any message text, EventData
// strings, account names or process paths.

func init() { RegisterGroup("events", true, newEventsGroup) }

// maxEventsPerTick caps the records ATTEMPTED per tick across all channels
// (rendered or not, W6-02) and the entries emitted.
const maxEventsPerTick = 256

// eventsReadBudget is the cooperative elapsed-work budget for one tick's event
// reads. The query checks it between bounded native calls so a slow provider
// cannot overrun the group deadline (W6-02). It sits well under budgetMaxGroup.
const eventsReadBudget = 750 * time.Millisecond

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

// rawEvent is one allowlisted event: numeric fields only, never text. RecordID
// is the channel's stable event-record identity used for the forward cursor and
// dedupe (W6-01); it is a number, never message text.
type rawEvent struct {
	Channel  uint8
	EventID  uint32
	Level    uint8
	T        time.Time
	RecordID uint64
}

// eventsAllowlistPredicate builds the allowlisted (provider, EventID) predicate
// for one channel. Numbers and fixed provider names only; the caller wraps it in
// System[...] with a record-ID or time filter.
func eventsAllowlistPredicate(channel uint8) string {
	var b strings.Builder
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
	return b.String()
}

// eventsXPath builds the first-read channel query: allowlisted pairs inside the
// no-backfill time window (used only until a record-ID cursor exists).
func eventsXPath(channel uint8, windowMS int64) string {
	return fmt.Sprintf("*[System[(%s) and TimeCreated[timediff(@SystemTime) <= %d]]]",
		eventsAllowlistPredicate(channel), windowMS)
}

// eventsXPathAfter builds the forward channel query: allowlisted pairs with a
// record ID strictly greater than the channel cursor (W6-01).
func eventsXPathAfter(channel uint8, afterID uint64) string {
	return fmt.Sprintf("*[System[(%s) and EventRecordID > %d]]",
		eventsAllowlistPredicate(channel), afterID)
}

// aggregateEvents groups rows by (channel, eventID, level), counts them, keeps
// the latest T, sorts by T desc and caps the entries. Rows are deduped by
// (channel, recordID) so a replayed record within a channel generation is counted
// once (W6-01). The forward cursor already excluded consumed records, so there is
// no timestamp filter here: equal timestamps are distinct records and both count.
func aggregateEvents(rows []rawEvent) []EventEntry {
	type key struct {
		ch    uint8
		id    uint32
		level uint8
	}
	type recKey struct {
		ch uint8
		id uint64
	}
	byKey := make(map[key]*EventEntry)
	seen := make(map[recKey]bool, len(rows))
	for _, r := range rows {
		rk := recKey{r.Channel, r.RecordID}
		if seen[rk] {
			continue // duplicate record ID within the channel generation
		}
		seen[rk] = true
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

// eventCursors is the per-channel read position: the last consumed record ID for
// each channel (index 0=System, 1=Application). Record IDs are a stable identity
// that timestamps cannot give (equal timestamps, delayed visibility, W6-01).
type eventCursors struct {
	ch [2]uint64
}

// eventCommittedStore holds the acknowledged cursors: the read position the
// recorder has durably accepted. It is written by the recorder's goroutine when
// Store.Append serializes the batch (EventsGroup.MarshalJSON) and read by the
// group's worker; the atomic pointer keeps that one field safe across the two
// goroutines without sharing any other group state (W6-06).
type eventCommittedStore struct {
	v atomic.Pointer[eventCursors]
}

func (s *eventCommittedStore) load() eventCursors {
	if p := s.v.Load(); p != nil {
		return *p
	}
	return eventCursors{}
}

func (s *eventCommittedStore) store(c eventCursors) { s.v.Store(&c) }

// eventChannelRead is one channel's forward read result.
type eventChannelRead struct {
	rows      []rawEvent
	lastID    uint64 // highest record ID fetched: the pending cursor
	attempted int    // records fetched, rendered or not (charges the budget)
	failed    int    // fetched but unreadable (bad variant/Level/ID/time)
	truncated bool   // budget exhausted with backlog still unread
	reset     bool   // log cleared: record IDs restarted below the cursor
	lost      uint64 // records dropped from the old generation (loss marker)
}

// eventChannelQuery reads one channel forward from afterID, attempting at most
// budget records and stopping cooperatively at deadline. floor is the
// no-backfill start time used only on the first read (afterID==0). Production
// uses queryEventChannel (per-OS twin); tests inject a fake.
type eventChannelQuery func(channel uint8, afterID uint64, floor time.Time, budget int, deadline time.Time) (eventChannelRead, error)

// eventsPending is the last emitted batch awaiting acknowledgement. It is owned
// by the group's single worker; only its cursor value is published (atomically)
// on ack.
type eventsPending struct {
	entries    []EventEntry
	cursors    eventCursors
	truncated  bool
	unreadable uint32
	lost       uint64
}

// eventsGroup tails the log behind per-channel record-ID cursors. query is
// injected so tests run without Windows; production uses queryEventChannel.
type eventsGroup struct {
	query     eventChannelQuery
	committed eventCommittedStore // acked cursors (atomic, W6-06)
	pending   *eventsPending      // worker-only: batch awaiting ack
	floor     time.Time           // worker-only: no-backfill start
	started   bool                // worker-only
}

func newEventsGroup(GroupDeps) (Group, error) {
	if err := eventsSupported(); err != nil {
		return nil, err // no wevtapi (non-Windows): group skipped
	}
	return &eventsGroup{query: queryEventChannel}, nil
}

func (g *eventsGroup) Name() string   { return "events" }
func (g *eventsGroup) Optional() bool { return true }

// fairShare splits the remaining budget over the channels still to read, so no
// channel can starve another (W6-01): each gets at least its equal share and any
// unused budget flows to the rest.
func fairShare(remaining, channelsLeft int) int {
	if channelsLeft < 1 {
		return 0
	}
	return remaining / channelsLeft
}

func (g *eventsGroup) Collect(now time.Time, dt float64) (any, error) {
	if !g.started {
		g.started = true
		g.floor = now // start at now: no historical backfill on restart
	}
	cur := g.committed.load()
	// The previous batch is acknowledged once the committed cursor catches up to
	// it (the recorder serialized it inside Store.Append). Until then it is
	// re-emitted, never skipped: a sample that was never durably accepted must
	// not advance the read position (W6-06).
	if g.pending != nil && g.pending.cursors == cur {
		g.pending = nil
	}
	if g.pending != nil {
		return g.payload(g.pending), nil
	}

	deadline := time.Now().Add(eventsReadBudget)
	remaining := maxEventsPerTick
	var rows []rawEvent
	var next eventCursors
	var truncated bool
	var unreadable uint32
	var lost uint64
	for i, ch := range eventsChannels {
		share := fairShare(remaining, len(eventsChannels)-i)
		read, err := g.query(ch.num, cur.ch[i], g.floor, share, deadline)
		if err != nil {
			return nil, err // access denied etc: group omitted, never 0
		}
		remaining -= read.attempted
		if remaining < 0 {
			remaining = 0
		}
		rows = append(rows, read.rows...)
		next.ch[i] = read.lastID
		if read.lastID == 0 && !read.reset {
			next.ch[i] = cur.ch[i] // nothing fetched: hold the cursor
		}
		truncated = truncated || read.truncated
		unreadable += uint32(read.failed)
		lost += read.lost
	}

	p := &eventsPending{
		entries:    aggregateEvents(rows),
		cursors:    next,
		truncated:  truncated,
		unreadable: unreadable,
		lost:       lost,
	}
	if len(p.entries) == 0 && !p.truncated && p.unreadable == 0 && p.lost == 0 {
		return nil, nil // no events and no quality signal: omitted, never 0
	}
	g.pending = p
	return g.payload(p), nil
}

// payload builds the emitted batch and wires its acknowledgement: the cursors
// are published only when the recorder serializes this batch (MarshalJSON).
func (g *eventsGroup) payload(p *eventsPending) *EventsGroup {
	return &EventsGroup{
		Events:     p.entries,
		Truncated:  p.truncated,
		Unreadable: p.unreadable,
		Lost:       p.lost,
		commit:     &g.committed,
		pending:    p.cursors,
	}
}

// Ack publishes this batch's read cursor. The recorder calls it only AFTER
// Store.Append has durably accepted the sample (W6-06), so a write that fails at
// any point - before, during or after serialization - leaves the batch pending
// and it is re-emitted rather than skipped.
//
// Acknowledgement is explicit rather than a side effect of MarshalJSON: anything
// that merely serializes a batch (a test, a re-marshal, a diagnostic dump) must
// not be able to advance the read cursor. Idempotent, and safe on a batch read
// back from disk, which carries no ack fields.
func (e *EventsGroup) Ack() {
	if e == nil || e.commit == nil {
		return
	}
	e.commit.store(e.pending)
}
