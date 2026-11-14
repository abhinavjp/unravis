package blackbox

import (
	"encoding/json"
	"testing"
	"time"
)

// W6-06: acknowledgement is explicit and happens only after the durable write.
// Serializing a batch must NOT advance the read cursor - otherwise a write that
// fails after serialization (or any diagnostic re-marshal) would silently skip
// events that were never durably accepted.
func TestSerializationDoesNotAcknowledge(t *testing.T) {
	f := &fakeEventSource{}
	now := time.Unix(1000, 0)
	f.records[0] = []rawEvent{ev(1, 1, 51, 2, now.Add(time.Second))}
	g := &eventsGroup{query: f.query}

	v, err := g.Collect(now, 0)
	if err != nil || v == nil {
		t.Fatalf("Collect: (%v, %v)", v, err)
	}
	eg := v.(*EventsGroup)

	// Marshal as many times as you like: the cursor must stay put.
	for i := 0; i < 3; i++ {
		if _, err := json.Marshal(eg); err != nil {
			t.Fatalf("marshal: %v", err)
		}
	}
	if g.committed.load() != (eventCursors{}) {
		t.Fatalf("serialization advanced the cursor: %+v", g.committed.load())
	}
	// An unacked batch is re-emitted, not skipped.
	v2, err := g.Collect(now.Add(15*time.Second), 15)
	if err != nil || v2 == nil {
		t.Fatalf("re-collect: (%v, %v)", v2, err)
	}
	if len(v2.(*EventsGroup).Events) != len(eg.Events) {
		t.Fatal("the unacked batch was not re-emitted")
	}

	// Only the explicit post-write acknowledgement commits.
	eg.Ack()
	if g.committed.load().ch[0] != 1 {
		t.Fatalf("Ack did not commit record 1: %+v", g.committed.load())
	}
	// Ack is idempotent and safe on a batch read back from disk.
	eg.Ack()
	var fromDisk EventsGroup
	if err := json.Unmarshal([]byte(`{"events":[]}`), &fromDisk); err != nil {
		t.Fatal(err)
	}
	fromDisk.Ack()
}
