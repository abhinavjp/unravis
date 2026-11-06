package server

import (
	"net/http"
	"testing"

	"api-audit-explorer/internal/casestore"
)

// The IIS lane carries what the logs say besides the request count: the 4xx
// requests of each bucket and the response times (p50, p95, slowest). The owner's
// logs hold no 5xx at all, so without these the lane was a bare count.
func TestTracingIISLaneCarriesStatusAndTiming(t *testing.T) {
	forEachTracingStore(t, func(t *testing.T, e *caseEnv, id string) {
		evs := tracingEvents(20)
		var want4xx int64
		for i := range evs {
			st := "200"
			if i%4 == 0 {
				st = "404"
				want4xx++
			}
			evs[i].Value, evs[i].Unit = nil, ""
			evs[i].Kind = "http"
			evs[i].Attrs = map[string]string{"status": st}
			d := float64(100)
			if i == 7 {
				d = 4000 // the one slow request
			}
			evs[i].DurationMs = &d
		}
		imp := seedTracingImport(t, e.store, id, "iis-w3c", "a.log", evs)
		seedTracingTag(t, e.store, id, casestore.SourceTag{Kind: "iis-w3c", Label: "a.log", ImportID: imp.ID})

		res := e.json(http.MethodPost, tracingPath(id), tracingBody(""))
		if res.Code != http.StatusOK {
			t.Fatalf("status %d %s", res.Code, res.Body)
		}
		var out tracingResponse
		decodeBody(t, res, &out)
		lane := tracingLaneOf(t, out, "iis")

		var count, client, timed int64
		var maxMs float64
		var sawP95 bool
		for _, b := range lane.Buckets {
			count += b.Count
			if b.ClientErrors != nil {
				client += *b.ClientErrors
			}
			timed += b.TimedCount
			if b.MaxMs != nil && *b.MaxMs > maxMs {
				maxMs = *b.MaxMs
			}
			if b.P95Ms != nil {
				sawP95 = true
				if b.P50Ms == nil || b.MaxMs == nil || *b.P50Ms > *b.P95Ms || *b.P95Ms > *b.MaxMs {
					t.Errorf("bucket figures out of order: %+v", b)
				}
			}
			if b.Errors != nil && *b.Errors != 0 {
				t.Errorf("4xx leaked into the 5xx errors: %+v", b)
			}
		}
		if count != 20 || client != want4xx || timed != 20 || !sawP95 {
			t.Errorf("count=%d client=%d (want %d) timed=%d p95=%v", count, client, want4xx, timed, sawP95)
		}
		if maxMs < 3500 || maxMs > 4500 {
			t.Errorf("slowest = %v ms, want about 4000", maxMs)
		}
	})
}

// Events without a duration leave the timing figures out; the sql lane never
// gets them.
func TestTracingIISLaneWithoutDurationsHasNoTiming(t *testing.T) {
	forEachTracingStore(t, func(t *testing.T, e *caseEnv, id string) {
		evs := tracingEvents(6)
		for i := range evs {
			evs[i].Attrs = map[string]string{"status": "200"}
		}
		imp := seedTracingImport(t, e.store, id, "iis-w3c", "a.log", evs)
		seedTracingTag(t, e.store, id, casestore.SourceTag{Kind: "iis-w3c", Label: "a.log", ImportID: imp.ID})
		res := e.json(http.MethodPost, tracingPath(id), tracingBody(""))
		var out tracingResponse
		decodeBody(t, res, &out)
		for _, b := range tracingLaneOf(t, out, "iis").Buckets {
			if b.TimedCount != 0 || b.P95Ms != nil || b.MaxMs != nil || b.ClientErrors != nil {
				t.Errorf("bucket carries figures it has no data for: %+v", b)
			}
		}
	})
}

