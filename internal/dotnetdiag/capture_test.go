//go:build capture

package dotnetdiag

import (
	"context"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// TestCaptureFixtures writes the real bytes in testdata/ from a live .NET
// process. It is not built in CI. Run it by hand from a neutral path so no
// private path ends up in a fixture:
//
//	UNRAVIS_DIAG_PID=<pid> go test -tags capture -run TestCaptureFixtures ./internal/dotnetdiag
//
// It writes the 28-byte start reply, the first 200 bytes of the stream and the
// 28-byte stop reply, then check the files with grep for your home path and user
// name before committing.
func TestCaptureFixtures(t *testing.T) {
	v := os.Getenv("UNRAVIS_DIAG_PID")
	if v == "" {
		t.Skip("set UNRAVIS_DIAG_PID")
	}
	pid64, err := strconv.ParseUint(v, 10, 32)
	if err != nil {
		t.Fatal(err)
	}
	tg := Target{PID: uint32(pid64)}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	d := CLIDialer()

	c1, peer, err := d.Dial(ctx, tg)
	if err != nil {
		t.Fatal(err)
	}
	defer c1.Close()
	tg.StartTicks = peer.StartTicks
	c1.SetDeadline(time.Now().Add(10 * time.Second))
	if err := writeAll(c1, encodeStart()); err != nil {
		t.Fatal(err)
	}
	startReply := make([]byte, 28)
	if _, err := io.ReadFull(c1, startReply); err != nil {
		t.Fatal(err)
	}
	session := binary.LittleEndian.Uint64(startReply[20:])
	head := make([]byte, 200)
	if _, err := io.ReadFull(c1, head); err != nil {
		t.Fatal(err)
	}

	c2, _, err := d.Dial(ctx, tg)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	c2.SetDeadline(time.Now().Add(10 * time.Second))
	if err := writeAll(c2, encodeStop(session)); err != nil {
		t.Fatal(err)
	}
	stopReply := make([]byte, 28)
	if _, err := io.ReadFull(c2, stopReply); err != nil {
		t.Fatal(err)
	}

	for name, b := range map[string][]byte{
		"start_reply.bin": startReply,
		"stream_head.bin": head,
		"stop_reply.bin":  stopReply,
	} {
		if err := os.WriteFile(filepath.Join("testdata", name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
