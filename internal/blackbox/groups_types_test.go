package blackbox

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// groupKeys are the JSON keys of the optional counter groups on Sample.
var groupKeys = []string{
	"http", "iis", "diskCpu", "sqlHost", "clr", "proc10", "procIO",
	"events", "tcp", "aspnet", "memPressure", "vm", "dumps",
}

func fp64(v float64) *float64 { return &v }

func TestSampleGroupsOmitWhenNil(t *testing.T) {
	line, err := jsonLine(Sample{V: FormatVersion, Run: "r", Seq: 1, T: t0})
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &keys); err != nil {
		t.Fatal(err)
	}
	for _, k := range groupKeys {
		if _, ok := keys[k]; ok {
			t.Fatalf("nil group %q must be omitted: %s", k, line)
		}
	}
}

func TestSampleGroupRoundTrip(t *testing.T) {
	want := Sample{
		V: FormatVersion, Run: "r", Seq: 1, T: t0,
		TCP: &TCPGroup{
			RetransmitsPerSec: fp64(1.5),
			States:            []TCPStateCount{{State: 2, Count: 7}},
		},
	}
	b, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	line := string(b)
	if !strings.Contains(line, `"retransmitsPerSec":1.5`) {
		t.Fatal("a set measurement must be written: " + line)
	}
	for _, k := range []string{`"resetsPerSec"`, `"outboundQueue"`} {
		if strings.Contains(line, k) {
			t.Fatal("a nil measurement must stay absent: " + line)
		}
	}
	var got Sample
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip changed the sample:\n got %+v\nwant %+v", got, want)
	}
	if got.TCP.ResetsPerSec != nil || got.TCP.OutboundQueue != nil {
		t.Fatal("nil measurements must stay nil after a round trip")
	}
	// After the round trip only the tcp group key may be present.
	b2, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(b2, &keys); err != nil {
		t.Fatal(err)
	}
	for _, k := range groupKeys {
		_, ok := keys[k]
		if k == "tcp" && !ok {
			t.Fatal("the set tcp group must survive the round trip: " + string(b2))
		}
		if k != "tcp" && ok {
			t.Fatalf("nil group %q must stay omitted: %s", k, b2)
		}
	}
}
