package dotnetdiag

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"testing"
	"testing/iotest"
)

// Goldens come from the BL-110 spec (docs/pipeline/BL-110-113-spec.md 3.3),
// checked against the dotnet/diagnostics IPC protocol document. They are NOT
// bytes captured from a real runtime yet.
const (
	goldenStart = "444f544e45545f4950435f5631008800020400000100000001000000000001000000ffffffff00000000050000000f000000530079007300740065006d002e00520075006e00740069006d00650000001a0000004500760065006e00740043006f0075006e0074006500720049006e00740065007200760061006c005300650063003d0035000000"
	goldenStop  = "444f544e45545f4950435f5631001c00020100008877665544332211"
	goldenOK    = "444f544e45545f4950435f5631001c00ff0000000100000000000000"
	goldenErr   = "444f544e45545f4950435f5631001800ffff000015151380"
)

func mustHex(t testing.TB, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestEncodeStartGolden(t *testing.T) {
	got := encodeStart()
	want := mustHex(t, goldenStart)
	if !bytes.Equal(got, want) {
		t.Fatalf("encodeStart mismatch\n got %x\nwant %x", got, want)
	}
	if len(got) != 136 {
		t.Fatalf("length %d, want 136", len(got))
	}
	if sz := binary.LittleEndian.Uint16(got[14:16]); sz != 136 {
		t.Fatalf("size field %d, want 136", sz)
	}
	if got[16] != 0x02 || got[17] != 0x04 {
		t.Fatalf("command %02x%02x, want 0204", got[16], got[17])
	}
}

func TestEncodeStopGolden(t *testing.T) {
	got := encodeStop(0x1122334455667788)
	want := mustHex(t, goldenStop)
	if !bytes.Equal(got, want) {
		t.Fatalf("encodeStop mismatch\n got %x\nwant %x", got, want)
	}
	if len(got) != 28 {
		t.Fatalf("length %d, want 28", len(got))
	}
}

func TestDecodeReplyOK(t *testing.T) {
	r, err := decodeReply(bytes.NewReader(mustHex(t, goldenOK)))
	if err != nil {
		t.Fatal(err)
	}
	if r.IsError || r.SessionID != 1 {
		t.Fatalf("got %+v, want session 1", r)
	}
	if r.err() != nil {
		t.Fatalf("OK reply gave error %v", r.err())
	}
}

func TestDecodeReplyError(t *testing.T) {
	r, err := decodeReply(bytes.NewReader(mustHex(t, goldenErr)))
	if err != nil {
		t.Fatal(err)
	}
	if !r.IsError || r.HRESULT != 0x80131515 {
		t.Fatalf("got %+v, want HRESULT 0x80131515", r)
	}
	var de *Error
	if !errors.As(r.err(), &de) || de.Reason != ReasonUnsupported || de.Code != 0x80131515 {
		t.Fatalf("got %v", r.err())
	}
}

// countingReader proves how many bytes a decode consumed.
type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += n
	return n, err
}

type reject struct {
	name  string
	in    []byte
	trunc bool // the cut itself is the defect, so no padding may follow it
}

func rejectCases(t testing.TB) []reject {
	ok := mustHex(t, goldenOK)
	er := mustHex(t, goldenErr)
	var cs []reject
	for i := 0; i < 14; i++ {
		b := append([]byte(nil), ok...)
		b[i] ^= 0xFF
		cs = append(cs, reject{name: fmt.Sprintf("magic byte %d", i), in: b})
	}
	{
		b := append([]byte(nil), ok...)
		b[18] = 1
		cs = append(cs, reject{name: "reserved low", in: b})
		b = append([]byte(nil), ok...)
		b[19] = 1
		cs = append(cs, reject{name: "reserved high", in: b})
	}
	for _, sz := range []uint16{0, 19, 20, 21, 23, 25, 27, 29, 65535} {
		b := append([]byte(nil), ok...)
		binary.LittleEndian.PutUint16(b[14:], sz)
		cs = append(cs, reject{name: fmt.Sprintf("size %d", sz), in: b})
	}
	{
		b := append([]byte(nil), ok...)
		b[16] = 0x02
		cs = append(cs, reject{name: "OK header with set 0x02", in: b})
		b = append([]byte(nil), ok...)
		b[17] = 0x01
		cs = append(cs, reject{name: "OK size with unknown id", in: b})
		// error header carrying the OK size
		b = append([]byte(nil), ok...)
		b[17] = 0xFF
		cs = append(cs, reject{name: "error header with 28 bytes", in: b})
		// OK id with the error size
		b = append([]byte(nil), er...)
		b[17] = 0x00
		cs = append(cs, reject{name: "OK id with 24 bytes", in: b})
	}
	for n := 0; n < 28; n++ {
		cs = append(cs, reject{name: fmt.Sprintf("truncated ok at %d", n), in: ok[:n], trunc: true})
	}
	for n := 0; n < 24; n++ {
		cs = append(cs, reject{name: fmt.Sprintf("truncated error at %d", n), in: er[:n], trunc: true})
	}
	return cs
}

func TestDecodeReplyRejects(t *testing.T) {
	for _, c := range rejectCases(t) {
		in := c.in
		if !c.trunc {
			in = append(append([]byte(nil), c.in...), make([]byte, 64)...)
		}
		cr := &countingReader{r: bytes.NewReader(in)}
		_, err := decodeReply(cr)
		var de *Error
		if !errors.As(err, &de) || de.Reason != ReasonBadReply {
			t.Errorf("%s: err %v, want bad-reply", c.name, err)
		}
		if cr.n > 28 {
			t.Errorf("%s: read %d bytes, want at most 28", c.name, cr.n)
		}
	}
}

func TestDecodeReplyTruncatedExact(t *testing.T) {
	// Without trailing bytes the stream ends at the cut: still bad-reply.
	ok := mustHex(t, goldenOK)
	for n := 0; n < len(ok); n++ {
		_, err := decodeReply(bytes.NewReader(ok[:n]))
		var de *Error
		if !errors.As(err, &de) || de.Reason != ReasonBadReply {
			t.Errorf("cut %d: %v", n, err)
		}
	}
}

func TestDecodeReplyOneByteWrites(t *testing.T) {
	r, err := decodeReply(iotest.OneByteReader(bytes.NewReader(mustHex(t, goldenOK))))
	if err != nil || r.SessionID != 1 {
		t.Fatalf("got %+v %v", r, err)
	}
}

type timeoutReader struct{}

func (timeoutReader) Read([]byte) (int, error) { return 0, errTimeout{} }

type errTimeout struct{}

func (errTimeout) Error() string   { return "i/o timeout" }
func (errTimeout) Timeout() bool   { return true }
func (errTimeout) Temporary() bool { return true }

func TestDecodeReplyTimeoutIsTimeout(t *testing.T) {
	_, err := decodeReply(timeoutReader{})
	var de *Error
	if !errors.As(err, &de) || de.Reason != ReasonTimeout {
		t.Fatalf("got %v, want timeout", err)
	}
}

func TestHRESULTMapping(t *testing.T) {
	cases := []struct {
		hr   uint32
		want Reason
	}{
		{0x80131515, ReasonUnsupported},
		{0x80131385, ReasonUnsupported},
		{0x8013135b, ReasonBusy},
		{0x80131371, ReasonBusy},
		{0x80004005, ReasonRejected},
		{0x80070057, ReasonRejected},
		{0x80131384, ReasonRejected},
		{0x00000000, ReasonRejected},
	}
	for _, c := range cases {
		if got := reasonForHRESULT(c.hr); got != c.want {
			t.Errorf("%#x: got %s, want %s", c.hr, got, c.want)
		}
	}
}

func TestErrorTextIsFixed(t *testing.T) {
	if got := newErr(ReasonBusy, 0).Error(); got != "dotnetdiag: busy" {
		t.Fatal(got)
	}
	if got := newErr(ReasonRejected, 0x80004005).Error(); got != "dotnetdiag: rejected (code 0x80004005)" {
		t.Fatal(got)
	}
}

func FuzzDecodeReply(f *testing.F) {
	f.Add(mustHex(f, goldenOK))
	f.Add(mustHex(f, goldenErr))
	for _, c := range rejectCases(f) {
		f.Add(c.in)
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		cr := &countingReader{r: bytes.NewReader(in)}
		r, err := decodeReply(cr)
		if cr.n > 28 {
			t.Fatalf("read %d bytes", cr.n)
		}
		if err != nil {
			var de *Error
			if !errors.As(err, &de) || de.Reason != ReasonBadReply {
				t.Fatalf("error %v is not bad-reply", err)
			}
			return
		}
		// A valid reply must be a prefix of the input with a valid header.
		if len(in) < okReplyLen-4 {
			t.Fatalf("accepted %d bytes", len(in))
		}
		if r.IsError && cr.n != errReplyLen {
			t.Fatalf("error reply consumed %d", cr.n)
		}
		if !r.IsError && cr.n != okReplyLen {
			t.Fatalf("ok reply consumed %d", cr.n)
		}
	})
}
