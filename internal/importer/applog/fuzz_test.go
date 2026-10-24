package applog

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

var allFamilies = map[string]bool{FamilyRedisTimeout: true, FamilyRedisConnection: true, FamilySQLPoolTimeout: true,
	FamilySQLExecTimeout: true, FamilyMongoSelectionTimeout: true, FamilyMongoPoolWait: true, FamilyHTTPClientTimeout: true,
	FamilyHeartbeatLate: true, FamilyRequestFinished: true, FamilyAppStart: true, FamilyAppStop: true,
	FamilyOutOfMemory: true, FamilyThreadAbort: true, FamilyTaskCanceled: true, FamilyOther: true}

// FuzzSink feeds arbitrary records through the core. Every event must be
// valid, every attribute value must come from the fixed vocabularies or be a
// number, and the sentinel must never show.
func FuzzSink(f *testing.F) {
	f.Add("WARN", "StackExchange.Redis", 0, redisMsg, "", "", 0.0)
	f.Add("Warning", catKestrel, 22, heartbeatMsg, "", "", 0.0)
	f.Add("Information", catHostingDiagnostics, 2, "Request finished HTTP/1.1 GET http://a.example.test/ - 200 1 - 1500ms", "",
		NumElapsedMs, 2000.0)
	f.Add("Error", "Synthetic.Api", 0, "x", "System.Exception: a ---> Synthetic.QuotaException: "+sentinel, NumMethod, 3.0)
	f.Add(sentinel, "Microsoft."+sentinel, 1, sentinel, sentinel+"Exception: x", sentinel, 1e300)
	f.Add("INFO", catLifetime, 0, "Application started.", "", NumStatus, 200.0)
	f.Fuzz(func(t *testing.T, level, cat string, eid int, msg, exc, numKey string, numVal float64) {
		var evs []evidence.Event
		o := testOpts()
		o.Supplied = map[string]string{SuppliedRequestMinMs: "0"}
		s := NewSink(testSrc, o, func(ev evidence.Event) error {
			if err := ev.ValidateFor(testCase, testSrc, AllowedAttrs()); err != nil {
				t.Fatalf("invalid event: %v", err)
			}
			evs = append(evs, ev)
			return nil
		})
		r := Record{Time: time.Date(2001, 10, 25, 1, 2, 3, 0, time.UTC), Rule: clock.RuleOffsetInField, Level: level,
			Category: cat, EventID: eid, Message: msg, Exception: exc, Num: map[string]float64{numKey: numVal}}
		if err := s.Add(r, 1); err != nil {
			t.Fatalf("Add: %v", err)
		}
		var rep importer.Report
		if err := s.Finish(&rep); err != nil {
			t.Fatalf("Finish: %v", err)
		}
		for _, e := range evs {
			for k, v := range e.Attrs {
				if !attrValueOK(k, v) {
					t.Errorf("%s=%q is outside the vocabulary", k, v)
				}
			}
		}
		if out := dump(t, evs, rep); strings.Contains(out, sentinel) {
			t.Fatalf("sentinel in output: %s", out)
		}
	})
}

func attrValueOK(k, v string) bool {
	switch k {
	case AttrLevel:
		_, _, ok := Level(v)
		return ok
	case AttrLogger:
		return frameworkLogger.MatchString(v) || loggerPseudonym.MatchString(v)
	case AttrFamily:
		return allFamilies[v]
	case AttrException:
		parts := strings.Split(v, ",")
		for _, p := range parts {
			if p != AppException && !curatedExceptions[p] {
				return false
			}
		}
		return len(parts) <= maxExceptionTypes
	case AttrMethod:
		_, ok := MethodNum(v)
		return ok
	}
	f, err := strconv.ParseFloat(v, 64)
	return err == nil && f >= 0 && f < 1e15
}
