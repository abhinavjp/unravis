package dotnetdiag

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"
)

func hostileOptions() Options {
	return Options{Duration: 50 * time.Millisecond, ReplyTimeout: 100 * time.Millisecond,
		StopTimeout: 100 * time.Millisecond, StreamCap: 64 << 10}
}

// readStart consumes the 136-byte start message the probe sends.
func readStart(s net.Conn) bool {
	_, err := io.ReadFull(s, make([]byte, 136))
	return err == nil
}

// stopServer answers a stop message with OK and the session id it carried.
func stopServer(s net.Conn, stopped func()) {
	buf := make([]byte, 28)
	if _, err := io.ReadFull(s, buf); err != nil {
		return
	}
	if bytes.Equal(buf, encodeStop(binary.LittleEndian.Uint64(buf[20:]))) {
		s.Write(okReply(binary.LittleEndian.Uint64(buf[20:])))
		if stopped != nil {
			stopped()
		}
	}
}

// waitStop blocks until the stop arrived, the client hung up, or a short limit.
func waitStop(s net.Conn, stopped <-chan struct{}) {
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
	}
}

// twoConn builds a handler: conn0 plays first, every later connection is an
// honest stop server that signals stopped.
func twoConn(first func(s net.Conn, stopped <-chan struct{})) (func(int, net.Conn), *sync.Once) {
	stopped := make(chan struct{})
	once := &sync.Once{}
	return func(i int, s net.Conn) {
		if i == 0 {
			first(s, stopped)
			return
		}
		stopServer(s, func() { once.Do(func() { close(stopped) }) })
	}, once
}

func writeSplit(s net.Conn, b []byte) bool {
	for _, c := range b {
		if _, err := s.Write([]byte{c}); err != nil {
			return false
		}
	}
	return true
}

func TestProbeHostileServer(t *testing.T) {
	pid := testPID()
	good := func(_ net.Conn, w func(net.Conn, []byte) bool, after func(net.Conn)) func(net.Conn, <-chan struct{}) {
		return func(s net.Conn, stopped <-chan struct{}) {
			if !readStart(s) {
				return
			}
			if !w(s, okReply(7)) {
				return
			}
			after(s)
			waitStop(s, stopped)
		}
	}
	whole := func(s net.Conn, b []byte) bool { _, err := s.Write(b); return err == nil }
	preambleThen := func(more func(net.Conn)) func(net.Conn) {
		return func(s net.Conn) {
			if _, err := s.Write(buildPreamble(pid, 8, 4)); err != nil {
				return
			}
			if more != nil {
				more(s)
			}
		}
	}

	type tc struct {
		name   string
		first  func(s net.Conn, stopped <-chan struct{})
		dialEr func(i int) error
		reason Reason
		check  func(t *testing.T, res Result, d *fakeDialer)
	}
	cases := []tc{
		{name: "wrong magic", reason: ReasonBadReply, first: func(s net.Conn, _ <-chan struct{}) {
			readStart(s)
			b := okReply(7)
			b[3] ^= 0xFF
			s.Write(b)
			io.Copy(io.Discard, s)
		}},
		{name: "size 65535", reason: ReasonBadReply, first: func(s net.Conn, _ <-chan struct{}) {
			readStart(s)
			b := okReply(7)
			binary.LittleEndian.PutUint16(b[14:], 65535)
			s.Write(b)
			io.Copy(io.Discard, s)
		}},
		{name: "reply in one byte writes is accepted", reason: "",
			first: good(nil, writeSplit, preambleThen(nil)),
			check: func(t *testing.T, res Result, d *fakeDialer) {
				if !res.PreambleOK || !res.StopAck {
					t.Fatalf("%+v", res)
				}
			}},
		{name: "reply stalls forever", reason: ReasonTimeout, first: func(s net.Conn, _ <-chan struct{}) {
			readStart(s)
			io.Copy(io.Discard, s)
		}, check: func(t *testing.T, res Result, d *fakeDialer) {
			if d.dials() != 1 {
				t.Fatalf("%d dials, a stop must not follow an unanswered start", d.dials())
			}
		}},
		{name: "server closes at once", reason: ReasonBadReply, first: func(s net.Conn, _ <-chan struct{}) {}},
		{name: "reply cut in the header", reason: ReasonBadReply, first: func(s net.Conn, _ <-chan struct{}) {
			readStart(s)
			s.Write(okReply(7)[:11])
		}},
		{name: "error reply unsupported", reason: ReasonUnsupported, first: func(s net.Conn, _ <-chan struct{}) {
			readStart(s)
			b, _ := hexDecode(goldenErr)
			s.Write(b)
			io.Copy(io.Discard, s)
		}, check: func(t *testing.T, res Result, d *fakeDialer) {
			if res.Code != 0x80131515 || d.dials() != 1 {
				t.Fatalf("%+v dials %d", res, d.dials())
			}
		}},
		{name: "infinite stream stops at the cap", reason: "",
			first: good(nil, whole, preambleThen(func(s net.Conn) {
				chunk := bytes.Repeat([]byte{0x41}, 4096)
				for {
					if _, err := s.Write(chunk); err != nil {
						return
					}
				}
			})),
			check: func(t *testing.T, res Result, d *fakeDialer) {
				if res.BytesBefore+res.BytesAfter > uint64(hostileOptions().StreamCap)+4096 {
					t.Fatalf("read %d+%d bytes, cap %d", res.BytesBefore, res.BytesAfter, hostileOptions().StreamCap)
				}
				if res.BytesBefore < uint64(hostileOptions().StreamCap) || !res.StopAck {
					t.Fatalf("%+v", res)
				}
			}},
		{name: "garbage preamble", reason: ReasonBadStream,
			first: good(nil, whole, func(s net.Conn) { s.Write(bytes.Repeat([]byte{0xAA}, 300)) }),
			check: func(t *testing.T, res Result, d *fakeDialer) {
				if !res.StopAck || res.PreambleOK {
					t.Fatalf("a bad preamble must still stop the session: %+v", res)
				}
			}},
		{name: "empty stream even after stop", reason: ReasonBadStream,
			first: good(nil, whole, func(net.Conn) {})},
		{name: "stop reply with another session id", reason: ReasonStopFailed,
			first: func(s net.Conn, stopped <-chan struct{}) {
				good(nil, whole, preambleThen(nil))(s, stopped)
			},
			check: func(t *testing.T, res Result, d *fakeDialer) {
				if res.StopSession {
					t.Fatalf("%+v", res)
				}
			}},
		{name: "stop connection refused", reason: ReasonStopFailed,
			first: good(nil, whole, preambleThen(nil)),
			dialEr: func(i int) error {
				if i == 1 {
					return errFakeRefused
				}
				return nil
			},
			check: func(t *testing.T, res Result, d *fakeDialer) {
				if !d.conn(0).Closed() {
					t.Fatal("first connection must be closed at once")
				}
			}},
		{name: "runtime closes the stream early", reason: "",
			first: func(s net.Conn, _ <-chan struct{}) {
				readStart(s)
				s.Write(okReply(7))
				s.Write(buildPreamble(pid, 8, 4))
				s.Write(bytes.Repeat([]byte{1}, 500))
			},
			check: func(t *testing.T, res Result, d *fakeDialer) {
				if !res.EndedEarly || !res.PreambleOK || d.dials() != 1 {
					t.Fatalf("%+v dials %d: no stop may be sent after an early end", res, d.dials())
				}
			}},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			handler, _ := twoConn(c.first)
			if c.name == "stop reply with another session id" {
				stopped := make(chan struct{})
				var once sync.Once
				handler = func(i int, s net.Conn) {
					if i == 0 {
						c.first(s, stopped)
						return
					}
					buf := make([]byte, 28)
					if _, err := io.ReadFull(s, buf); err != nil {
						return
					}
					s.Write(okReply(8))
					once.Do(func() { close(stopped) })
				}
			}
			d := &fakeDialer{handler: handler, dialErr: c.dialEr}
			type out struct {
				res Result
				err error
			}
			done := make(chan out, 1)
			go func() {
				r, err := Probe(context.Background(), d, Target{PID: pid}, hostileOptions())
				done <- out{r, err}
			}()
			select {
			case o := <-done:
				if got := reasonOf(t, o.err); got != c.reason || o.res.Reason != c.reason {
					t.Fatalf("reason %q (result %q), want %q; %+v", got, o.res.Reason, c.reason, o.res)
				}
				if c.check != nil {
					c.check(t, o.res, d)
				}
				for i := 0; i < d.dials(); i++ {
					if cn := d.conn(i); cn != nil && !cn.Closed() {
						t.Errorf("connection %d left open", i)
					}
				}
				// Whatever the server did, only the two golden messages were written.
				if w := d.conn(0).Written(); len(w) != 0 && !bytes.Equal(w, encodeStart()) {
					t.Errorf("conn 0 got %x", w)
				}
				if cn := d.conn(1); cn != nil {
					if w := cn.Written(); len(w) != 0 && !bytes.Equal(w, encodeStop(7)) {
						t.Errorf("conn 1 got %x", w)
					}
				}
			case <-time.After(2 * time.Second):
				t.Fatal("Probe did not return within 2 s")
			}
		})
	}
}

func TestProbeCancelRunsStop(t *testing.T) {
	pid := testPID()
	h := newHonest(7, pid)
	h.chunks, h.gap = 100000, time.Millisecond
	d := &fakeDialer{handler: h.handle}
	ctx, cancel := context.WithCancel(context.Background())
	o := hostileOptions()
	o.Duration = 30 * time.Second
	go func() { time.Sleep(60 * time.Millisecond); cancel() }()
	start := time.Now()
	res, err := Probe(ctx, d, Target{PID: pid}, o)
	if time.Since(start) > 2*time.Second {
		t.Fatalf("cancel took %v", time.Since(start))
	}
	if reasonOf(t, err) != ReasonCancelled || res.Reason != ReasonCancelled {
		t.Fatalf("err %v %+v", err, res)
	}
	if d.dials() != 2 || !bytes.Equal(d.conn(1).Written(), encodeStop(7)) {
		t.Fatalf("dials %d: the stop message must still be sent", d.dials())
	}
	if d.ctxErrs[1] != nil {
		t.Fatalf("the stop dial used the cancelled context: %v", d.ctxErrs[1])
	}
	if !res.StopAck || !res.StopSession {
		t.Fatalf("%+v", res)
	}
}

func TestProbeCancelBeforeDial(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d := &fakeDialer{dialErr: func(int) error { return context.Canceled }}
	_, err := Probe(ctx, d, Target{PID: testPID()}, hostileOptions())
	if reasonOf(t, err) != ReasonCancelled {
		t.Fatal(err)
	}
}

func TestProbePanicRunsStop(t *testing.T) {
	pid := testPID()
	h := newHonest(7, pid)
	d := &fakeDialer{handler: h.handle, panicOn: func(i int) bool { return i == 0 }}
	res, err := Probe(context.Background(), d, Target{PID: pid}, hostileOptions())
	if reasonOf(t, err) != ReasonOther || res.Reason != ReasonOther {
		t.Fatalf("err %v %+v", err, res)
	}
	if d.dials() != 2 || !bytes.Equal(d.conn(1).Written(), encodeStop(7)) {
		t.Fatalf("dials %d: the stop must still be attempted after a panic", d.dials())
	}
	if !d.conn(0).Closed() {
		t.Fatal("first connection not closed")
	}
}

func FuzzProbeHostileServer(f *testing.F) {
	pid := testPID()
	honestStream := append(append(okReply(7), buildPreamble(pid, 8, 4)...), bytes.Repeat([]byte{0x55}, 2000)...)
	f.Add(honestStream)
	f.Add(honestStream[:30])
	f.Add([]byte{})
	f.Add(bytes.Repeat([]byte{0xFF}, 300))
	f.Add(append(okReply(1<<63), bytes.Repeat([]byte{0xAA}, 300)...))
	for _, c := range rejectCases(f) {
		f.Add(c.in)
	}
	o := Options{Duration: 20 * time.Millisecond, ReplyTimeout: 100 * time.Millisecond,
		StopTimeout: 100 * time.Millisecond, StreamCap: 8 << 10}
	o.ReplyTimeout = 40 * time.Millisecond
	f.Fuzz(func(t *testing.T, in []byte) {
		handler, _ := twoConn(func(s net.Conn, stopped <-chan struct{}) {
			if !readStart(s) {
				return
			}
			if _, err := s.Write(in); err != nil {
				return
			}
			waitStopShort(stopped)
		})
		d := &fakeDialer{handler: handler}
		done := make(chan struct{})
		var res Result
		go func() {
			defer close(done)
			res, _ = Probe(context.Background(), d, Target{PID: pid}, o)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("Probe did not return within 2 s")
		}
		if w := d.conn(0).Written(); !bytes.Equal(w, encodeStart()) {
			t.Fatalf("conn 0 got %x", w)
		}
		if cn := d.conn(1); cn != nil {
			if res.SessionID == "" {
				t.Fatal("a stop was sent without an accepted session")
			}
			id, err := strconv.ParseUint(res.SessionID, 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			if w := cn.Written(); !bytes.Equal(w, encodeStop(id)) {
				t.Fatalf("conn 1 got %x", w)
			}
		}
		if res.BytesBefore+res.BytesAfter > uint64(o.StreamCap)+4096 {
			t.Fatalf("read %d+%d bytes", res.BytesBefore, res.BytesAfter)
		}
	})
}

func waitStopShort(stopped <-chan struct{}) {
	select {
	case <-stopped:
	case <-time.After(300 * time.Millisecond):
	}
}
