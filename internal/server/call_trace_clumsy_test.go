package server

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
)

// Clumsy-user probes for the call trace routes: odd numbers, very long calls, calls that end before they start, and big bodies.

func clumsyRepo() *traceRepo {
	repo := newTraceRepo()
	repo.recs["long"] = traceRecord("long", traceT0, 400*24*time.Hour)
	back := traceRecord("back", traceT0, time.Second)
	earlier := traceT0.Add(-time.Hour)
	back.CompletedAt = &earlier
	repo.recs["back"] = back
	return repo
}

func TestCallTraceVeryLongCallStaysAnswerable(t *testing.T) {
	requireRealTrace(t)
	traceRoutes(t, clumsyRepo(), func(t *testing.T, post tracePost, _ bool) {
		res := post(callsBody("long") + `}`)
		if res.Code != http.StatusOK {
			t.Fatalf("status = %d %s", res.Code, res.Body)
		}
		requireNoNullArrays(t, res.Body.String())
		var v traceView
		decodeBody(t, res, &v)
		if len(v.Traces) != 1 || !v.Traces[0].Window.To.After(v.Traces[0].Window.From) {
			t.Fatalf("window not usable: %+v", v.Traces)
		}
	})
}

func TestCallTraceEndBeforeStartDoesNotBreakTheWindow(t *testing.T) {
	requireRealTrace(t)
	traceRoutes(t, clumsyRepo(), func(t *testing.T, post tracePost, _ bool) {
		res := post(callsBody("back") + `}`)
		if res.Code != http.StatusOK && res.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d %s", res.Code, res.Body)
		}
		if res.Code == http.StatusOK {
			var v traceView
			decodeBody(t, res, &v)
			if !v.Traces[0].Window.To.After(v.Traces[0].Window.From) {
				t.Errorf("window runs backwards: %+v", v.Traces[0].Window)
			}
		}
	})
}

func TestCallTraceHugeMarginsClampInsteadOfOverflowing(t *testing.T) {
	requireRealTrace(t)
	traceRoutes(t, newTraceRepo(), func(t *testing.T, post tracePost, _ bool) {
		for _, margin := range []string{"9223372036854775807", "2147483648", "1000000000000"} {
			res := post(callsBody("a") + `,"beforeMin":` + margin + `,"afterMin":` + margin + `}`)
			if res.Code != http.StatusOK {
				t.Errorf("margin %s = %d %s", margin, res.Code, res.Body)
				continue
			}
			var v traceView
			decodeBody(t, res, &v)
			tr := v.Traces[0]
			if !tr.Window.From.Equal(tr.Call.Start.Add(-30*time.Minute)) || !tr.Window.To.Equal(tr.Call.Start.Add(3*time.Second+30*time.Minute)) {
				t.Errorf("margin %s not clamped to 30 minutes: %v to %v", margin, tr.Window.From, tr.Window.To)
			}
		}
	})
}

func TestCallTraceOddMarginsAreRefusedNotGuessed(t *testing.T) {
	traceRoutes(t, newTraceRepo(), func(t *testing.T, post tracePost, _ bool) {
		for name, margin := range map[string]string{"fraction": "1.5", "string": `"5"`, "null-ok-or-400": "null", "exponent": "1e2", "overflow": "99999999999999999999"} {
			res := post(callsBody("a") + `,"beforeMin":` + margin + `}`)
			if res.Code >= 500 {
				t.Errorf("%s margin = %d %s, want a 2xx or 4xx", name, res.Code, res.Body)
			}
		}
	})
}

func TestCallTraceBigBodiesAreRefusedNotRead(t *testing.T) {
	repo := newTraceRepo()
	traceRoutes(t, repo, func(t *testing.T, post tracePost, _ bool) {
		before := repo.read
		huge := callsBody("a") + `,"pad":"` + strings.Repeat("x", 1<<20+512) + `"}`
		if res := post(huge); res.Code < 400 || res.Code >= 500 {
			t.Errorf("1 MiB body = %d, want a 4xx", res.Code)
		}
		long := `{"calls":[{"sourceId":"demo-west","id":"` + strings.Repeat("i", 600) + `"}]}`
		if res := post(long); res.Code != http.StatusBadRequest && res.Code != http.StatusNotFound {
			t.Errorf("600 character id = %d %s, want 400 or 404", res.Code, res.Body)
		}
		if repo.read != before {
			t.Errorf("refused bodies read %d records", repo.read-before)
		}
	})
}

func TestCallTraceErrorsNeverLeakRecordDetails(t *testing.T) {
	repo := clumsyRepo()
	repo.recs["nostart"] = audit.RecordDetail{RequestRecord: audit.RequestRecord{ID: "nostart", SourceID: "demo-west", Controller: "SecretController", Action: "SecretAction"}}
	traceRoutes(t, repo, func(t *testing.T, post tracePost, _ bool) {
		for _, id := range []string{"nostart", "missing"} {
			res := post(callsBody(id) + `}`)
			if strings.Contains(res.Body.String(), "SecretController") || strings.Contains(res.Body.String(), "demo-west/") {
				t.Errorf("%s error leaks record detail: %s", id, res.Body)
			}
		}
	})
}
