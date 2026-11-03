package mongoexports

import (
	"testing"

	"api-audit-explorer/internal/importer"
)

func TestProfileUsedDisk(t *testing.T) {
	const head = `{"op":"query","millis":5,"ts":"2001-09-09T01:00:00Z"`
	evs, rep := mustParse(t, NewProfile(), []byte(head+`,"usedDisk":true}`+"\n"+head+`,"usedDisk":false}`+"\n"+head+"}\n"), baseOpts())
	if len(evs) != 3 || rep.Rejected != 0 {
		t.Fatalf("%d events, %d rejected", len(evs), rep.Rejected)
	}
	wantAttrs(t, evs[0], map[string]string{"type": "query", "usedDisk": "true"})
	wantAttrs(t, evs[1], map[string]string{"type": "query", "usedDisk": "false"})
	if _, ok := evs[2].Attrs["usedDisk"]; ok {
		t.Errorf("usedDisk set without the key: %v", evs[2].Attrs)
	}

	o := baseOpts()
	o.Limits.MaxRejectFraction = 1
	evs, rep, err := parse(t, NewProfile(), []byte(head+"}\n"+head+`,"usedDisk":"yes"}`+"\n"), o)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Rejected != 1 || len(rep.Errors) != 1 || rep.Errors[0].Reason != importer.ReasonBadNumber || len(evs) != 1 {
		t.Errorf("bad usedDisk: rejected %d errors %+v events %d", rep.Rejected, rep.Errors, len(evs))
	}
}
