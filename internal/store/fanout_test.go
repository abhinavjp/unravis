package store

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// fanoutDelay is how long every fake operation holds its slot.
const fanoutDelay = 200 * time.Millisecond

// fanoutProbe counts the operations in flight, per source (collection) and
// globally, from the moment a fake server call starts to the moment its cursor is
// closed (the span the store holds its slots for).
type fanoutProbe struct {
	mu        sync.Mutex
	inflight  int
	maxGlobal int
	per       map[string]int
	maxPer    map[string]int
	calls     int
}

func newFanoutProbe() *fanoutProbe {
	return &fanoutProbe{per: map[string]int{}, maxPer: map[string]int{}}
}

func (p *fanoutProbe) enter(collection string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.inflight++
	p.per[collection]++
	if p.inflight > p.maxGlobal {
		p.maxGlobal = p.inflight
	}
	if p.per[collection] > p.maxPer[collection] {
		p.maxPer[collection] = p.per[collection]
	}
}

func (p *fanoutProbe) leave(collection string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.inflight--
	p.per[collection]--
}

func (p *fanoutProbe) current() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.inflight
}

func (p *fanoutProbe) worstPerSource() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	worst := 0
	for _, n := range p.maxPer {
		if n > worst {
			worst = n
		}
	}
	return worst
}

// probedCursor ends the probe's span when the store closes it.
type probedCursor struct {
	*fakeCursor
	once  sync.Once
	leave func()
}

func (c *probedCursor) Close(ctx context.Context) error {
	c.once.Do(c.leave)
	return c.fakeCursor.Close(ctx)
}

// fanoutDocs answers a fake aggregation: the breakdown counters or bins, or a
// request page row, depending on what the pipeline asks for.
func fanoutDocs(collection string, pipeline mongo.Pipeline, now time.Time) []any {
	last := pipeline[len(pipeline)-1]
	switch {
	case last[0].Key == "$limit" && len(pipeline) > 1 && pipeline[len(pipeline)-2][0].Key == "$group":
		raw, _ := bson.Marshal(bson.D{{Key: "_id", Value: bson.D{{Key: "v", Value: "UA-" + collection}}}, {Key: "count", Value: 3}, {Key: "incomplete", Value: 0}, {Key: "pending", Value: 0}, {Key: "c4xx", Value: 0}, {Key: "c5xx", Value: 0}})
		return []any{bson.Raw(raw)}
	case last[0].Key == "$group":
		lower, _ := audit.BinBounds(4)
		raw, _ := bson.Marshal(bson.D{{Key: "_id", Value: bson.D{{Key: "v", Value: "UA-" + collection}, {Key: "o", Value: false}, {Key: "bin", Value: 4}}}, {Key: "count", Value: 3}, {Key: "sum", Value: 3 * lower}, {Key: "min", Value: lower}, {Key: "max", Value: lower}})
		return []any{bson.Raw(raw)}
	}
	// Each source returns its own timestamps, interleaved with the others', so the
	// merged order does not depend on the random source ids.
	index := int(collection[len(collection)-1] - '0')
	docs := make([]any, 0, 3)
	for i := 0; i < 3; i++ {
		at := now.Add(-time.Duration(i*10+index) * time.Second)
		// Fixed ids (the driver's generator adds random bytes), so two runs match.
		id := bson.NewObjectIDFromTimestamp(at)
		copy(id[4:], []byte{0, 0, 0, 0, 0, 0, byte(index), byte(i)})
		docs = append(docs, requestDocument{ID: id, Controller: "Payroll", Action: "Run", Kind: "synchronous", Outcome: "success", InitiatedAt: &at})
	}
	return docs
}

type fanoutHarness struct {
	store   *Store
	sources []audit.Source
	probe   *fanoutProbe
	// block makes every fake operation wait for its context instead of finishing.
	block bool
	// failCollection, when set, makes every operation on that source fail.
	failCollection string
}

func newFanoutHarness(t *testing.T, n int, block bool) *fanoutHarness {
	t.Helper()
	var handles []*fakeClient
	s := newStore(func(context.Context, string, *tls.Config) (clientHandle, error) {
		h := &fakeClient{}
		handles = append(handles, h)
		return h, nil
	})
	h := &fanoutHarness{store: s, probe: newFanoutProbe(), block: block}
	for i := 0; i < n; i++ {
		h.sources = append(h.sources, addFakeSource(t, s, fmt.Sprintf("audit-%d", i)))
	}
	now := fanoutNow()
	fn := func(ctx context.Context, _, collection string, pipeline mongo.Pipeline) (mongoCursor, error) {
		if collection == h.failCollection {
			return nil, errors.New("mongodb://reader:secret@host:27017/db: network error")
		}
		h.probe.enter(collection)
		cur := &probedCursor{fakeCursor: &fakeCursor{docs: fanoutDocs(collection, pipeline, now)}, leave: func() { h.probe.leave(collection) }}
		wait := fanoutDelay
		if h.block {
			wait = time.Hour
		}
		select {
		case <-ctx.Done():
			cur.once.Do(cur.leave)
			return nil, ctx.Err()
		case <-time.After(wait):
		}
		return cur, nil
	}
	for _, handle := range handles {
		handle.aggregateFn = fn
	}
	return h
}

func fanoutNow() time.Time { return time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC) }

func (h *fanoutHarness) query(first, count int) audit.Query {
	q := queryForTest(fanoutNow())
	q.Kind, q.Outcome, q.SourceIDs = "synchronous", "success", nil
	for _, src := range h.sources[first : first+count] {
		q.SourceIDs = append(q.SourceIDs, src.ID)
	}
	return q
}

func (h *fanoutHarness) breakdown(ctx context.Context, first, count int) (audit.Breakdown, error) {
	return h.store.Breakdown(ctx, audit.BreakdownInput{Query: h.query(first, count), Dim: audit.BreakdownUserAgent, Top: 5})
}

func (h *fanoutHarness) requests(ctx context.Context, first, count int) (audit.Page, error) {
	return h.store.Requests(ctx, audit.PageInput{Query: h.query(first, count), Limit: 5})
}

// semaphoresEmpty reports whether the store holds no slot, global or per source.
func semaphoresEmpty(s *Store) bool {
	if len(s.global) != 0 {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, per := range s.perSource {
		if len(per) != 0 {
			return false
		}
	}
	return true
}

type fanoutRun struct {
	name string
	run  func(h *fanoutHarness, ctx context.Context, first, count int) error
}

var fanoutRuns = []fanoutRun{
	{"breakdown", func(h *fanoutHarness, ctx context.Context, first, count int) error {
		_, err := h.breakdown(ctx, first, count)
		return err
	}},
	{"requests", func(h *fanoutHarness, ctx context.Context, first, count int) error {
		_, err := h.requests(ctx, first, count)
		return err
	}},
}

func TestFanoutReadsSourcesInParallel(t *testing.T) {
	for _, tc := range fanoutRuns {
		t.Run(tc.name, func(t *testing.T) {
			h := newFanoutHarness(t, 3, false)
			began := time.Now()
			if err := tc.run(h, context.Background(), 0, 1); err != nil {
				t.Fatal(err)
			}
			single := time.Since(began)
			began = time.Now()
			if err := tc.run(h, context.Background(), 0, 3); err != nil {
				t.Fatal(err)
			}
			three := time.Since(began)
			if three >= single*3/2 {
				t.Fatalf("3 sources took %v, one source %v: want under 1.5x (serial would be about 3x)", three, single)
			}
			if h.probe.worstPerSource() > 1 || h.probe.maxGlobal > 4 {
				t.Fatalf("per source %d, global %d", h.probe.worstPerSource(), h.probe.maxGlobal)
			}
			if h.probe.maxGlobal < 3 {
				t.Fatalf("at most %d operations overlapped, want all 3 sources at once", h.probe.maxGlobal)
			}
			if !semaphoresEmpty(h.store) {
				t.Fatal("slots still held after the call")
			}
		})
	}
}

func TestFanoutKeepsOneOperationPerSourceAndFourGlobally(t *testing.T) {
	for _, tc := range fanoutRuns {
		t.Run(tc.name, func(t *testing.T) {
			h := newFanoutHarness(t, 6, false)
			// Two callers over the same six sources, so a source is wanted by two
			// reads at once and the store has more work than its four slots.
			var wg sync.WaitGroup
			errs := make(chan error, 2)
			for i := 0; i < 2; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					errs <- tc.run(h, context.Background(), 0, 6)
				}()
			}
			wg.Wait()
			close(errs)
			for err := range errs {
				if err != nil {
					t.Fatal(err)
				}
			}
			if got := h.probe.worstPerSource(); got != 1 {
				t.Fatalf("most operations at once on one source = %d, want 1", got)
			}
			if h.probe.maxGlobal != 4 {
				t.Fatalf("most operations at once = %d, want the global limit of 4", h.probe.maxGlobal)
			}
			if h.probe.current() != 0 || !semaphoresEmpty(h.store) {
				t.Fatal("slots still held after the calls")
			}
		})
	}
}

func TestFanoutCancellationReleasesEverySlot(t *testing.T) {
	for _, tc := range fanoutRuns {
		t.Run(tc.name, func(t *testing.T) {
			h := newFanoutHarness(t, 6, true)
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- tc.run(h, ctx, 0, 6) }()
			// Wait until the global limit is full: the other two sources are queued
			// behind it in admit.
			deadline := time.Now().Add(5 * time.Second)
			for h.probe.current() < 4 {
				if time.Now().After(deadline) {
					t.Fatal("operations never reached the global limit")
				}
				time.Sleep(5 * time.Millisecond)
			}
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("call did not return after cancellation")
			}
			if h.probe.current() != 0 || !semaphoresEmpty(h.store) {
				t.Fatalf("slots still held after cancellation: global %d", len(h.store.global))
			}
		})
	}
}

var (
	elapsedField = regexp.MustCompile(`"elapsedMs":\d+`)
	sourceIDHex  = regexp.MustCompile(`"sourceId":"[0-9a-f]{32}"`)

	nextCursorField  = regexp.MustCompile(`"nextCursor":"[A-Za-z0-9_-]+"`)
	fingerprintField = regexp.MustCompile(`"fingerprint":"[0-9a-f]+"`)
)

func fanoutJSON(t *testing.T, v any, err error) string {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	raw, marshalErr := json.Marshal(v)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	text := elapsedField.ReplaceAllString(string(raw), `"elapsedMs":0`)
	// A cursor names its source and the query's fingerprint, both random per
	// store: show it decoded, with the fingerprint masked.
	text = nextCursorField.ReplaceAllStringFunc(text, func(m string) string {
		decoded, err := base64.RawURLEncoding.DecodeString(m[len(`"nextCursor":"`) : len(m)-1])
		if err != nil {
			t.Fatal(err)
		}
		return `"nextCursor":` + fingerprintField.ReplaceAllString(string(decoded), `"fingerprint":"F"`)
	})
	// Source ids are random per store: name them by first appearance.
	names := map[string]string{}
	return sourceIDHex.ReplaceAllStringFunc(text, func(m string) string {
		if _, ok := names[m]; !ok {
			names[m] = fmt.Sprintf(`"sourceId":"S%d"`, len(names))
		}
		return names[m]
	})
}

// fanoutDump renders the output of Breakdown (modern and legacy, every
// dimension, pooled and single kind) and of Requests over several sources, with
// timings removed.
func fanoutDump(t *testing.T, serial bool) string {
	t.Helper()
	var out strings.Builder
	now := bdNow()
	docs := bdDocs(bdCases(now))
	for _, legacy := range []bool{false, true} {
		h := bdStore(t, legacy, docs, "second", "third")
		if serial {
			h.store.global = make(chan struct{}, 1)
		}
		for _, dim := range []string{audit.BreakdownUserAgent, audit.BreakdownStatusCode, audit.BreakdownPollingType, audit.BreakdownUIVersion, audit.BreakdownUserIP, audit.BreakdownUserID} {
			for _, kind := range []string{"all", "synchronous"} {
				q := bdQuery(now)
				q.Kind = kind
				q.SourceIDs = h.ids()
				got, err := h.store.Breakdown(context.Background(), audit.BreakdownInput{Query: q, Dim: dim, Top: 3})
				fmt.Fprintf(&out, "breakdown legacy=%v %s %s: %s\n", legacy, dim, kind, fanoutJSON(t, got, err))
			}
		}
	}
	// A source that fails keeps its place in the order.
	h := newFanoutHarness(t, 4, false)
	if serial {
		h.store.global = make(chan struct{}, 1)
	}
	h.failCollection = "audit-2"
	for _, limit := range []int{2, 5, 12} {
		page, err := h.store.Requests(context.Background(), audit.PageInput{Query: h.query(0, 4), Limit: limit})
		fmt.Fprintf(&out, "requests limit=%d: %s\n", limit, fanoutJSON(t, page, err))
	}
	return out.String()
}

func TestFanoutOutputMatchesSequentialRead(t *testing.T) {
	want := fanoutDump(t, true)
	for i := 0; i < 5; i++ {
		if got := fanoutDump(t, false); got != want {
			t.Fatalf("parallel read %d differs from the sequential read:\n%s\n---\n%s", i, got, want)
		}
	}
	if path := os.Getenv("UNRAVIS_FANOUT_DUMP"); path != "" {
		if err := os.WriteFile(path, []byte(want), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
