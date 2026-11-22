package dotnetdiag

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"runtime"
	"strconv"
	"time"
)

// ownPID is this process id; tests replace it to point the probe at an
// in-process server.
var ownPID = func() uint32 { return uint32(os.Getpid()) }

// Target names the process to probe. StartTicks 0 means no expectation.
type Target struct {
	PID        uint32
	StartTicks int64
}

// Peer is what a dialer verified about the server end of a connection.
type Peer struct {
	PID        uint32
	StartTicks int64
}

// Conn is one connection to a diagnostic port.
type Conn interface {
	io.ReadWriteCloser
	SetDeadline(time.Time) error
}

// Dialer opens ONE connection. Before it returns, it has checked that the
// server of the connection is process t.PID and, when t.StartTicks != 0, that
// the process was created at t.StartTicks. On a failed check it closes the
// connection and returns ReasonPeerMismatch. It has written no byte. Probe
// checks the returned Peer again before its first write.
type Dialer interface {
	Dial(ctx context.Context, t Target) (Conn, Peer, error)
}

// DefaultDialer is the transport of this platform.
func DefaultDialer() Dialer { return platformDialer() }

// Options bound the probe. A zero field takes its default.
type Options struct {
	Duration     time.Duration // default 20 s; the command allows 5 s..60 s
	ReplyTimeout time.Duration // default 5 s
	StopTimeout  time.Duration // default 5 s (connect + reply, and the final drain)
	StreamCap    int64         // default 4 MiB read in total, then stop early
}

// DefaultOptions returns the defaults.
func DefaultOptions() Options {
	return Options{
		Duration:     20 * time.Second,
		ReplyTimeout: 5 * time.Second,
		StopTimeout:  5 * time.Second,
		StreamCap:    4 << 20,
	}
}

func (o Options) withDefaults() Options {
	d := DefaultOptions()
	if o.Duration > 0 {
		d.Duration = o.Duration
	}
	if o.ReplyTimeout > 0 {
		d.ReplyTimeout = o.ReplyTimeout
	}
	if o.StopTimeout > 0 {
		d.StopTimeout = o.StopTimeout
	}
	if o.StreamCap > 0 {
		d.StreamCap = o.StreamCap
	}
	return d
}

// Result holds only fixed numeric, boolean and fixed-word fields. Nothing from
// the stream and no OS text is ever stored in it.
type Result struct {
	OS            string `json:"os"`
	Transport     string `json:"transport"`
	RunAs         string `json:"runAs"` // set by the caller (system|elevated|user|unknown)
	PID           uint32 `json:"pid"`
	PipeOpened    bool   `json:"pipeOpened"`
	ServerPID     uint32 `json:"serverPid"`
	ServerStart   int64  `json:"serverStart"`
	StartMatch    *bool  `json:"startMatch"` // nil when no start ticks were given
	SessionID     string `json:"sessionId"`  // decimal string
	PreambleOK    bool   `json:"preambleOk"`
	TraceVersion  uint32 `json:"traceVersion"`
	TracePID      uint32 `json:"tracePid"`
	PointerSize   uint32 `json:"pointerSize"`
	TracePIDMatch bool   `json:"tracePidMatch"` // informational only (pid namespaces differ in containers)
	BytesBefore   uint64 `json:"bytesBefore"`   // read before Stop was sent
	BytesAfter    uint64 `json:"bytesAfter"`    // read after Stop, until end of stream
	EndedEarly    bool   `json:"endedEarly"`    // the runtime closed the stream before the time was up
	StopAck       bool   `json:"stopAck"`
	StopSession   bool   `json:"stopSession"` // the session id in the Stop reply equals the start id
	ElapsedMs     int64  `json:"elapsedMs"`
	Reason        Reason `json:"reason"` // "" on success
	Code          uint32 `json:"code"`
}

const (
	readChunk   = 4096
	headKeep    = 128
	zeroReadMax = 1000
)

// Probe runs the read-only probe described in the BL-110 spec 3.1: peer check,
// one fixed start message, one reply, the stream read and thrown away (the
// first 128 bytes kept only to check the preamble), a second connection with
// one fixed stop message, a bounded drain. It returns the Result always (zero
// Result with ErrBadTarget for a bad target) and a non-nil error exactly when
// Result.Reason is not "". The stop path runs on cancel, on a failed
// preamble and on a panic.
func Probe(ctx context.Context, d Dialer, t Target, o Options) (res Result, err error) {
	if t.PID == 0 || t.PID == ownPID() {
		return Result{}, ErrBadTarget
	}
	p := &prober{d: d, t: t, o: o.withDefaults(), began: time.Now()}
	p.res = Result{OS: runtime.GOOS, Transport: transportName(), PID: t.PID}
	defer func() {
		if r := recover(); r != nil {
			p.fail(newErr(ReasonOther, 0))
		}
		p.finish()
		res = p.res
		err = nil
		if p.err != nil {
			err = p.err
		}
	}()
	p.run(ctx)
	return
}

func transportName() string {
	if runtime.GOOS == "windows" {
		return "pipe"
	}
	return "socket"
}

type prober struct {
	d     Dialer
	t     Target
	o     Options
	began time.Time

	conn1   Conn
	peer1   Peer
	session uint64
	started bool // a start reply with a session id was accepted
	stopped bool // the stop step ran (or was skipped on purpose)

	total int64
	head  [headKeep]byte
	hn    int

	res Result
	err *Error // first failure wins
}

func (p *prober) fail(e *Error) {
	if p.err == nil {
		p.err = e
	}
}

func (p *prober) take(b []byte) {
	p.total += int64(len(b))
	if p.hn < headKeep {
		p.hn += copy(p.head[p.hn:], b)
	}
}

// asErr turns any error from a dialer, read or write into an *Error. The text
// of the original is dropped.
func asErr(ctx context.Context, err error, fallback Reason) *Error {
	var de *Error
	if errors.As(err, &de) {
		return de
	}
	if ctx != nil && ctx.Err() != nil {
		return newErr(ReasonCancelled, 0)
	}
	if isTimeout(err) {
		return newErr(ReasonTimeout, 0)
	}
	return newErr(fallback, 0)
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.Is(err, os.ErrDeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) ||
		(errors.As(err, &ne) && ne.Timeout())
}

func writeAll(c Conn, b []byte) error {
	for len(b) > 0 {
		n, err := c.Write(b)
		if err != nil {
			return err
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		b = b[n:]
	}
	return nil
}

// run is steps 1-6 of the spec: dial, peer check, start, reply, stream.
func (p *prober) run(ctx context.Context) {
	dctx, cancel := context.WithTimeout(ctx, p.o.ReplyTimeout)
	c, peer, err := p.d.Dial(dctx, p.t)
	cancel()
	if err != nil {
		p.fail(asErr(ctx, err, ReasonOther))
		return
	}
	if c == nil {
		p.fail(newErr(ReasonOther, 0))
		return
	}
	p.conn1, p.peer1 = c, peer
	p.res.PipeOpened = true
	p.res.ServerPID, p.res.ServerStart = peer.PID, peer.StartTicks

	// Peer check, before the first write: the server end must be the process
	// asked for, and (when given) created at the time asked for.
	if peer.PID != p.t.PID || (p.t.StartTicks != 0 && peer.StartTicks != p.t.StartTicks) {
		if p.t.StartTicks != 0 {
			m := peer.StartTicks == p.t.StartTicks
			p.res.StartMatch = &m
		}
		p.fail(newErr(ReasonPeerMismatch, 0))
		return
	}
	if p.t.StartTicks != 0 {
		m := true
		p.res.StartMatch = &m
	}

	c.SetDeadline(time.Now().Add(p.o.ReplyTimeout))
	if err := writeAll(c, encodeStart()); err != nil {
		p.fail(asErr(ctx, err, ReasonBadReply))
		return
	}
	r, err := decodeReply(c)
	if err != nil {
		p.fail(asErr(ctx, err, ReasonBadReply))
		return
	}
	if e := r.err(); e != nil {
		p.fail(e.(*Error))
		return
	}
	p.session, p.started = r.SessionID, true
	p.res.SessionID = strconv.FormatUint(r.SessionID, 10)

	// Stream: read and throw away. A cancelled context interrupts the read by
	// moving the deadline into the past.
	streamEnd := time.Now().Add(p.o.Duration)
	c.SetDeadline(streamEnd)
	fired := make(chan struct{})
	stopAfter := context.AfterFunc(ctx, func() {
		defer close(fired)
		c.SetDeadline(time.Unix(1, 0))
	})
	buf := make([]byte, readChunk)
	zero := 0
	for p.total < p.o.StreamCap {
		n, rerr := c.Read(buf)
		p.take(buf[:n])
		if n == 0 && rerr == nil {
			if zero++; zero > zeroReadMax {
				p.fail(newErr(ReasonBadStream, 0))
				break
			}
		} else {
			zero = 0
		}
		if rerr != nil {
			if !isTimeout(rerr) && time.Now().Before(streamEnd) && ctx.Err() == nil {
				p.res.EndedEarly = true
			}
			break
		}
	}
	if !stopAfter() {
		<-fired
	}
	if ctx.Err() != nil {
		p.fail(newErr(ReasonCancelled, 0))
	}
	p.res.BytesBefore = uint64(p.total)
}

// finish always runs: stop path, drain, close, preamble check. Each step is
// guarded so one panic cannot skip the next.
func (p *prober) finish() {
	if p.started && !p.stopped {
		p.stopped = true
		safe(p.stop)
	}
	safe(p.drainAndClose)
	safe(p.checkPreamble)
	p.res.ElapsedMs = time.Since(p.began).Milliseconds()
	if p.err != nil {
		p.res.Reason, p.res.Code = p.err.Reason, p.err.Code
	}
}

func safe(f func()) {
	defer func() { recover() }()
	f()
}

// stop is step 7: a second connection, one fixed stop message, one reply.
// It is skipped when the runtime already closed the stream. It always uses a
// fresh context, so a cancelled run still stops its session.
func (p *prober) stop() {
	if p.res.EndedEarly {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), p.o.StopTimeout)
	defer cancel()
	failed := func(code uint32) {
		p.fail(newErr(ReasonStopFailed, code))
		p.closeFirst() // close the first connection at once
	}
	c, peer, err := p.d.Dial(ctx, Target{PID: p.t.PID, StartTicks: p.peer1.StartTicks})
	if err != nil {
		failed(asErr(nil, err, ReasonOther).Code)
		return
	}
	if c == nil {
		failed(0)
		return
	}
	defer c.Close()
	if peer.PID != p.t.PID || peer.StartTicks != p.peer1.StartTicks {
		failed(0)
		return
	}
	c.SetDeadline(time.Now().Add(p.o.StopTimeout))
	if err := writeAll(c, encodeStop(p.session)); err != nil {
		failed(asErr(nil, err, ReasonOther).Code)
		return
	}
	r, err := decodeReply(c)
	if err != nil {
		failed(asErr(nil, err, ReasonBadReply).Code)
		return
	}
	if r.IsError {
		failed(r.HRESULT)
		return
	}
	p.res.StopAck = true
	p.res.StopSession = r.SessionID == p.session
	if !p.res.StopSession {
		failed(0)
	}
}

func (p *prober) closeFirst() {
	if c := p.conn1; c != nil {
		p.conn1 = nil
		c.Close()
	}
}

// drainAndClose is step 8: read the first connection to its end for at most
// StopTimeout and StreamCap bytes, counting only, then close it.
func (p *prober) drainAndClose() {
	c := p.conn1
	if c == nil {
		return
	}
	defer p.closeFirst()
	if !p.started || p.res.EndedEarly || p.total >= p.o.StreamCap {
		return
	}
	c.SetDeadline(time.Now().Add(p.o.StopTimeout))
	buf := make([]byte, readChunk)
	zero := 0
	for p.total < p.o.StreamCap {
		n, err := c.Read(buf)
		p.take(buf[:n])
		p.res.BytesAfter += uint64(n)
		if err != nil {
			return
		}
		if n == 0 {
			if zero++; zero > zeroReadMax {
				return
			}
		} else {
			zero = 0
		}
	}
}

// checkPreamble judges the first bytes of the stream, wherever they arrived.
func (p *prober) checkPreamble() {
	if !p.started {
		return
	}
	pre, err := ParsePreamble(p.head[:p.hn])
	if err != nil {
		p.fail(err.(*Error))
		return
	}
	p.res.PreambleOK = true
	p.res.TraceVersion, p.res.TracePID, p.res.PointerSize = pre.Version, pre.PID, pre.PointerSize
	p.res.TracePIDMatch = pre.PID == p.t.PID
}
