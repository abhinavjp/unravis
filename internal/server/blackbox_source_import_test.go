package server

// A Blackbox recording imported through the real import route: the file is filed under the
// computer the person picked, and its events land in the case. The file is written by the recorder's own Sample type, so a
// format drift between the recorder and the reader fails here.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/blackbox"
	"api-audit-explorer/internal/importer/all"
)

func bbRecording(t *testing.T) string {
	t.Helper()
	cpu, q := 12.5, 0.5
	mem := uint64(4 << 30)
	t0 := time.Date(2001, 9, 9, 1, 47, 0, 0, time.UTC)
	var b strings.Builder
	for i := 0; i < 5; i++ {
		raw, err := json.Marshal(blackbox.Sample{V: blackbox.FormatVersion, Run: "run-1", Seq: uint64(i + 1), T: t0.Add(time.Duration(i) * 15 * time.Second),
			CPU: &cpu, MemAvail: &mem, DiskQueue: &q})
		if err != nil {
			t.Fatal(err)
		}
		b.Write(raw)
		b.WriteString("\n")
	}
	return b.String()
}

func TestBlackboxRecordingImportsThroughTheRealRoute(t *testing.T) {
	e := newCaseEnv(t, envConfig{importers: all.Importers()})
	cid := e.newCase("Blackbox recording")
	res := e.upload("/api/cases/"+cid+"/imports?importer=blackbox-source&host=web-a", ackFields, &upload{"bb-20010909-01.ndjson", bbRecording(t)})
	if res.Code != http.StatusCreated {
		t.Fatalf("import = %d %s", res.Code, res.Body)
	}
	imports := e.importsOf(cid)
	if len(imports) != 1 || imports[0].Importer != "blackbox-source" || imports[0].HostLabel != "web-a" || imports[0].Rows != 5 || imports[0].Rejected != 0 {
		t.Fatalf("imports = %+v", imports)
	}
	events, _ := e.eventsOf(cid, "")
	var cpuEvents, starts int
	for _, ev := range events {
		if ev.Source != "blackbox-source" || ev.Host != "web-a" {
			t.Fatalf("event %+v", ev)
		}
		switch {
		case ev.Kind == "counter" && ev.Attrs["counter"] == "% Processor Time":
			cpuEvents++
		case ev.Kind == "recorder-start":
			starts++
		}
	}
	if cpuEvents != 5 || starts != 1 {
		t.Errorf("%d cpu events and %d recorder starts from 5 samples of one run", cpuEvents, starts)
	}
	// Without a picked computer the recording is refused: it holds no machine name.
	cid2 := e.newCase("Blackbox recording, no computer")
	res = e.upload("/api/cases/"+cid2+"/imports?importer=blackbox-source", ackFields, &upload{"bb-20010909-01.ndjson", bbRecording(t)})
	if res.Code == http.StatusCreated {
		t.Errorf("a recording with no computer was stored: %s", res.Body)
	}
}
