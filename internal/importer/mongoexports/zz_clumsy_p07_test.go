package mongoexports

import (
	"strings"
	"testing"

	"api-audit-explorer/internal/importer"
)

func TestClumsyP07UsedDiskOddValues(t *testing.T) {
	const head = `{"op":"query","millis":5,"ts":"2001-09-09T01:00:00Z"`
	cases := map[string]bool{ // line tail -> rejected
		`,"usedDisk":null}`:                  true,
		`,"usedDisk":1}`:                     true,
		`,"usedDisk":0}`:                     true,
		`,"usedDisk":"true"}`:                true,
		`,"usedDisk":"free text here"}`:      true,
		`,"usedDisk":{"a":1}}`:               true,
		`,"usedDisk":[true]}`:                true,
		`,"usedDisk":TRUE}`:                  true,
		`,"usedDisk": true }`:                false,
		`,"usedDisk":true,"usedDisk":false}`: false,
	}
	for tail, rej := range cases {
		o := baseOpts()
		o.Limits.MaxRejectFraction = 1
		evs, rep, err := parse(t, NewProfile(), []byte(head+`}`+"\n"+head+tail+"\n"), o)
		if err != nil {
			t.Errorf("%s: %v", tail, err)
			continue
		}
		if rej && (rep.Rejected != 1 || len(evs) != 1) {
			t.Errorf("%s: rejected %d events %d, want the line rejected", tail, rep.Rejected, len(evs))
		}
		if !rej && rep.Rejected != 0 {
			t.Errorf("%s: rejected %d", tail, rep.Rejected)
		}
		for _, e := range evs {
			if v, ok := e.Attrs["usedDisk"]; ok && v != "true" && v != "false" {
				t.Errorf("%s: usedDisk kept as %q", tail, v)
			}
		}
	}
}

func TestClumsyP07ConnectionsRejectedOddValues(t *testing.T) {
	const head = `{"host":"h:1","localTime":{"$date":"2001-09-09T01:0`
	good := head + `0:00.000Z"},"uptime":10,"connections":{"current":1,"rejected":0}}`
	bad := map[string]importer.Reason{
		`"rejected":-1`:                           importer.ReasonBadNumber,
		`"rejected":"many"`:                       importer.ReasonBadNumber,
		`"rejected":null`:                         importer.ReasonBadNumber,
		`"rejected":true`:                         importer.ReasonBadNumber,
		`"rejected":1e999`:                        importer.ReasonBadNumber,
		`"rejected":1e300`:                        importer.ReasonBadNumber,
		`"rejected":{"$numberLong":"-4"}`:         importer.ReasonBadNumber,
		`"rejected":{"$numberInt":"x"}`:           importer.ReasonBadNumber,
		`"rejected":{"$numberDouble":"NaN"}`:      importer.ReasonBadNumber,
		`"rejected":{"$numberDouble":"Infinity"}`: importer.ReasonBadNumber,
	}
	for frag, reason := range bad {
		line := head + `5:00.000Z"},"uptime":20,"connections":{"current":1,` + frag + `}}`
		o := baseOpts()
		o.Limits.MaxRejectFraction = 1
		evs, rep, err := parse(t, NewServerStatus(), []byte(good+"\n"+line+"\n"), o)
		if err != nil {
			t.Errorf("%s: %v", frag, err)
			continue
		}
		if rep.Rejected != 1 || len(rep.Errors) != 1 || rep.Errors[0].Reason != reason {
			t.Errorf("%s: rejected %d errors %+v", frag, rep.Rejected, rep.Errors)
		}
		for _, e := range evs {
			if e.Attrs["metric"] == "connections.rejected" && e.Value != nil && *e.Value != 0 {
				t.Errorf("%s: a bad value was kept as %v", frag, *e.Value)
			}
		}
	}
	// a missing key is no event for that metric, never a zero
	line := head + `5:00.000Z"},"uptime":20,"connections":{"current":1}}`
	evs, rep := mustParse(t, NewServerStatus(), []byte(line+"\n"), baseOpts())
	if rep.Rejected != 0 {
		t.Fatalf("rejected %d", rep.Rejected)
	}
	for _, e := range evs {
		if strings.Contains(e.Attrs["metric"], "rejected") {
			t.Errorf("a missing rejected key produced %+v", e)
		}
	}
}
