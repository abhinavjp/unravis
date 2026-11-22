package dotnetdiag

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The files in testdata/ are REAL bytes from a live .NET runtime: the Ubuntu
// build of .NET 10.0.15 (a source-built variant) on Linux, over its Unix
// socket, started from a neutral path. capture_test.go (build tag capture)
// wrote them. The Windows runtime writes the same IPC and nettrace formats,
// but Windows bytes themselves are not captured yet.

const (
	fixturePID     = 31368 // the process id inside the captured stream
	fixtureHeadLen = 200
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestFixturesAreScrubbed(t *testing.T) {
	for _, name := range []string{"start_reply.bin", "stop_reply.bin", "stream_head.bin"} {
		b := fixture(t, name)
		if len(b) > 60<<10 {
			t.Errorf("%s is %d bytes", name, len(b))
		}
		for _, bad := range []string{"/home", "/root", "/tmp", `C:\`, "claude", "diagtarget", "Users"} {
			if bytes.Contains(b, []byte(bad)) {
				t.Errorf("%s contains %q", name, bad)
			}
		}
	}
}

func TestFixtureRepliesDecode(t *testing.T) {
	start, err := decodeReply(bytes.NewReader(fixture(t, "start_reply.bin")))
	if err != nil || start.IsError || start.SessionID == 0 {
		t.Fatalf("real start reply: %+v %v", start, err)
	}
	stop, err := decodeReply(bytes.NewReader(fixture(t, "stop_reply.bin")))
	if err != nil || stop.IsError {
		t.Fatalf("real stop reply: %+v %v", stop, err)
	}
	if stop.SessionID != start.SessionID {
		t.Fatalf("the runtime answered stop for session %d, started %d", stop.SessionID, start.SessionID)
	}
	// The spec's golden layout matches what the runtime really sent.
	golden := mustHex(t, goldenOK)
	if got := fixture(t, "start_reply.bin"); !bytes.Equal(got[:20], golden[:20]) || len(got) != len(golden) {
		t.Fatalf("real header %x differs from the spec golden %x", got[:20], golden[:20])
	}
}

func TestFixturePreamble(t *testing.T) {
	head := fixture(t, "stream_head.bin")
	if len(head) != fixtureHeadLen {
		t.Fatalf("%d bytes", len(head))
	}
	p, err := ParsePreamble(head)
	if err != nil {
		t.Fatalf("real preamble rejected: %v", err)
	}
	if p.Version != 4 || p.PointerSize != 8 || p.PID != fixturePID || p.QPCFreq != 1_000_000_000 ||
		p.Processors != 4 || p.SamplingRate != 1_000_000 || p.SyncTime[0] != 2026 {
		t.Fatalf("real preamble fields: %+v", p)
	}
	// What follows the 102 bytes is the metadata block header: begin object,
	// begin type object, null reference (step 2 reads it).
	if !bytes.Equal(head[PreambleLen:PreambleLen+3], []byte{5, 5, 1}) || !bytes.Contains(head[PreambleLen:], []byte("MetadataBlock")) {
		t.Fatalf("bytes after the preamble: %x", head[PreambleLen:])
	}
	// The synthetic builder produces the same layout: rebuilding from the real
	// fields gives the real bytes.
	if !bytes.Equal(buildPreambleFrom(p), head[:PreambleLen]) {
		t.Fatalf("synthetic builder differs from real bytes:\n got %x\nreal %x", buildPreambleFrom(p), head[:PreambleLen])
	}
}

// TestFixtureReplayProbe plays the real runtime bytes back through the whole
// probe with a replaying fake.
func TestFixtureReplayProbe(t *testing.T) {
	if os.Getpid() == fixturePID {
		t.Skip("test process has the fixture's pid")
	}
	startReply := fixture(t, "start_reply.bin")
	head := fixture(t, "stream_head.bin")
	stopReply := fixture(t, "stop_reply.bin")
	session := binary.LittleEndian.Uint64(startReply[20:])
	stopped := make(chan struct{})
	d := &fakeDialer{handler: func(i int, s net.Conn) {
		if i == 0 {
			got := make([]byte, 136)
			if _, err := io.ReadFull(s, got); err != nil || !bytes.Equal(got, encodeStart()) {
				return
			}
			s.Write(startReply)
			s.Write(head)
			select {
			case <-stopped:
			case <-time.After(3 * time.Second):
			}
			return
		}
		got := make([]byte, 28)
		if _, err := io.ReadFull(s, got); err != nil || !bytes.Equal(got, encodeStop(session)) {
			return
		}
		s.Write(stopReply)
		close(stopped)
	}}
	res, err := Probe(context.Background(), d, Target{PID: fixturePID}, testOptions())
	if err != nil || res.Reason != "" {
		t.Fatalf("err %v result %+v", err, res)
	}
	if !res.PreambleOK || !res.StopAck || !res.StopSession || res.TracePID != fixturePID || !res.TracePIDMatch ||
		res.TraceVersion != 4 || res.PointerSize != 8 || res.BytesBefore+res.BytesAfter != fixtureHeadLen {
		t.Fatalf("result %+v", res)
	}
}
