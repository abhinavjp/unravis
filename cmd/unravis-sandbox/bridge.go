package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"api-audit-explorer/internal/codemode/wire"
)

// session is one run: the engine, the host state and the frame stream. It is single-threaded; nothing here runs in the background.
type session struct {
	ctx context.Context
	c   *conn
	run wire.Run
	h   *host
	e   *engine
	// trap is the error that ended an engine call inside deliver.
	trap error
}

func newSession(c *conn, run wire.Run) (*session, error) {
	p, clock, rnd, err := checkRun(run)
	if err != nil {
		return nil, err
	}
	h := &host{p: p, tools: run.Tools, clock: clock, rnd: rnd, inflight: map[uint32]struct{}{}}
	s := &session{ctx: context.Background(), c: c, run: run, h: h}
	e, err := newEngine(s.ctx, h)
	if err != nil {
		return nil, err
	}
	s.e = e
	return s, nil
}

func (s *session) close() {
	if s.e != nil {
		s.e.close(s.ctx)
	}
}

// drive runs the script to the end and returns the done frame. An error is a protocol violation or a failure of the worker: no done frame.
func (s *session) drive() (wire.Done, error) {
	h, e := s.h, s.e
	if _, err := e.call(s.ctx, e.fInit,
		uint64(h.p.heapBytes), uint64(h.p.stackBytes), uint64(h.p.ticks), tickPolls, uint64(h.p.callBytes), uint64(h.p.maxCalls)+1); err != nil { //nolint:gosec // range checked in validation
		return wire.Done{}, fmt.Errorf("engine init: %w", err)
	}
	if h.broken != nil {
		return wire.Done{}, h.broken
	}
	rc, err := s.start()
	if err != nil {
		return s.afterTrap(err)
	}
	if int32(rc) != 0 { //nolint:gosec // wasm i32 result
		return wire.Done{}, fmt.Errorf("engine refused the run (%d)", int32(rc)) //nolint:gosec // wasm i32 result
	}
	for {
		if err := s.flush(); err != nil {
			return wire.Done{}, err
		}
		st, err := e.call(s.ctx, e.fPump)
		if err != nil {
			return s.afterTrap(err)
		}
		if h.broken != nil {
			return wire.Done{}, h.broken
		}
		switch int32(st) { //nolint:gosec // wasm i32 result
		case 1, -1: // settled, or never started (a compile error): the report says which
			return s.finish()
		case 0:
		default:
			return wire.Done{}, fmt.Errorf("engine pump answered %d", int32(st)) //nolint:gosec // wasm i32 result
		}
		if err := s.flush(); err != nil {
			return wire.Done{}, err
		}
		if len(h.inflight) == 0 && len(h.queue) == 0 {
			// Nothing can ever settle the promise: no call is out, none is waiting and the engine has no job left.
			return s.failure(codeScriptError, "the script never finished: it waits for something that cannot happen")
		}
		if err := s.receive(); err != nil {
			if errors.Is(err, errRunEnded) {
				return s.afterTrap(s.trap)
			}
			return wire.Done{}, err
		}
	}
}

// start gives the engine the script and the tool names.
func (s *session) start() (uint64, error) {
	e := s.e
	js, err := e.put(s.ctx, []byte(s.run.JS))
	if err != nil {
		return 0, err
	}
	if js == 0 {
		return 0, errOutOfMemory
	}
	joined := []byte(strings.Join(s.run.Tools, "\n"))
	names, err := e.put(s.ctx, joined)
	if err != nil {
		return 0, err
	}
	if names == 0 {
		return 0, errOutOfMemory
	}
	rc, err := e.call(s.ctx, e.fRun, uint64(js), uint64(len(s.run.JS)), uint64(names), uint64(len(joined)))
	if err != nil {
		return 0, err
	}
	// The engine compiled the script and copied the names; neither buffer is needed again.
	e.free(s.ctx, js)
	e.free(s.ctx, names)
	return rc, nil
}

var (
	errOutOfMemory = errors.New("the engine is out of memory")
	errEngineTime  = errors.New("the engine ran out of time")
	errRunEnded    = errors.New("the run ended inside the engine")
)

// flush sends queued calls to the broker, oldest first, while fewer than inFlight are out.
func (s *session) flush() error {
	h := s.h
	for len(h.queue) > 0 && len(h.inflight) < h.p.inFlight {
		call := h.queue[0]
		h.queue[0] = pendingCall{}
		h.queue = h.queue[1:]
		h.queuedBytes -= len(call.args)
		frame := wire.Call{T: wire.TypeCall, ID: int64(call.id), Tool: h.tools[call.tool], Args: json.RawMessage(call.args)}
		if err := s.c.send(frame); err != nil {
			return fmt.Errorf("write call: %w", err)
		}
		h.inflight[call.id] = struct{}{}
	}
	return nil
}

// receive reads one frame from the broker, which must be the answer to a call in flight, and hands it to the engine.
func (s *session) receive() error {
	data, err := s.c.recv()
	if err != nil {
		return err
	}
	t, err := wire.PeekType(data)
	if err != nil {
		return violation("bad frame")
	}
	if t != wire.TypeRet {
		return violation("unexpected frame %q", clip(t))
	}
	var ret wire.Ret
	if err := wire.Decode(data, &ret); err != nil {
		return violation("bad ret frame")
	}
	if ret.ID <= 0 || ret.ID > int64(^uint32(0)) {
		return violation("ret for an unknown call")
	}
	id := uint32(ret.ID)
	if _, ok := s.h.inflight[id]; !ok {
		return violation("ret for a call that is not in flight")
	}
	var text []byte
	switch {
	case ret.OK && ret.Error == nil:
		text = ret.Result
		if len(text) == 0 {
			text = []byte("null")
		}
	case !ret.OK && ret.Error != nil:
		text, err = json.Marshal(ret.Error)
		if err != nil {
			return fmt.Errorf("ret error: %w", err)
		}
	default:
		return violation("ret is not exactly one of result and error")
	}
	delete(s.h.inflight, id)
	return s.deliver(id, ret.OK, text)
}

// deliver hands one answer to the engine (qjs_ret) and runs nothing else: the loop pumps next.
func (s *session) deliver(id uint32, ok bool, text []byte) error {
	e := s.e
	ptr, err := e.put(s.ctx, text)
	if err != nil {
		s.trap = err
		return errRunEnded
	}
	if ptr == 0 {
		s.trap = errOutOfMemory
		return errRunEnded
	}
	flag := uint64(0)
	if ok {
		flag = 1
	}
	rc, err := e.call(s.ctx, e.fRet, uint64(id), flag, uint64(ptr), uint64(len(text)))
	if err != nil {
		s.trap = err
		return errRunEnded
	}
	e.free(s.ctx, ptr)
	if int32(rc) != 0 { //nolint:gosec // wasm i32 result
		return fmt.Errorf("engine refused ret %d", id)
	}
	if s.h.broken != nil {
		return s.h.broken
	}
	return nil
}

func clip(s string) string {
	if len(s) > 32 {
		return s[:32]
	}
	return s
}

// afterTrap turns the end of an engine call by an error into the done frame. The module is dead after any trap, so only what Go kept
// (the logs, the numbers host_fatal recorded) goes into the frame.
func (s *session) afterTrap(err error) (wire.Done, error) {
	h := s.h
	switch {
	case h.fatalCode == fatalOOM, errors.Is(err, errOutOfMemory):
		return s.failure(codeOOM, "the script used more memory than it is allowed")
	case errors.Is(err, errEngineTime):
		// The broker's done codes have no wall code for a worker to send; this is the nearest: the script ran more than it is allowed.
		return s.failure(codeTicks, "the script ran longer than the engine is allowed to run")
	case h.fatalCode == fatalTicks:
		return s.failure(codeTicks, "the script ran more steps than it is allowed")
	case strings.Contains(err.Error(), "stack overflow"):
		return s.failure(codeStack, "the script recursed too deeply")
	}
	return wire.Done{}, fmt.Errorf("engine trap: %w", err)
}

// failure makes the done frame of a run the worker ended itself.
func (s *session) failure(code, msg string) (wire.Done, error) {
	h := s.h
	d := wire.Done{
		T:             wire.TypeDone,
		OK:            false,
		Logs:          h.logs,
		LogsTruncated: h.logsTruncated,
		Ticks:         h.fatalTicks,
		HeapPeak:      h.fatalHeap,
		Error:         &wire.DoneError{Code: code, Message: msg},
	}
	return s.fit(d), nil
}

// finish reads the engine's report and makes the done frame of a script that ended by itself.
func (s *session) finish() (wire.Done, error) {
	rep, err := s.e.report(s.ctx, s.h.p.callBytes)
	if err != nil {
		return wire.Done{}, err
	}
	h := s.h
	d := wire.Done{
		T:             wire.TypeDone,
		Logs:          h.logs,
		LogsTruncated: h.logsTruncated,
		Ticks:         clampInt64(rep.ticks),
		HeapPeak:      clampInt64(rep.heapPeak),
	}
	switch rep.kind {
	case kindOK:
		if len(rep.result) > 0 && !json.Valid(rep.result) {
			d.Error = &wire.DoneError{Code: codeScriptError, Message: "the result is not JSON"}
			break
		}
		d.OK = true
		if len(rep.result) > 0 {
			d.Result = json.RawMessage(rep.result)
		}
	case kindTooLarge:
		d.Error = &wire.DoneError{Code: codeTooLarge, Message: "the result is larger than the limit"}
	case kindStack:
		d.Error = &wire.DoneError{Code: codeStack, Message: "the script recursed too deeply"}
	case kindError:
		d.Error = &wire.DoneError{Code: codeScriptError, Message: rep.message, Stack: rep.stack}
	default:
		return wire.Done{}, fmt.Errorf("engine report kind %d", rep.kind)
	}
	return s.fit(d), nil
}

func clampInt64(v uint64) int64 {
	if v > 1<<62 {
		return 1 << 62
	}
	return int64(v)
}

// fit makes sure the done frame can be sent: if it is over the frame maximum the logs go first (with logsTruncated), and a result that
// is still too large becomes output_too_large.
func (s *session) fit(d wire.Done) wire.Done {
	if _, err := wire.Marshal(d); err == nil {
		return d
	}
	d.Logs = nil
	d.LogsTruncated = true
	if _, err := wire.Marshal(d); err == nil {
		return d
	}
	d.OK = false
	d.Result = nil
	d.Error = &wire.DoneError{Code: codeTooLarge, Message: "the result is larger than the limit"}
	if _, err := wire.Marshal(d); err == nil {
		return d
	}
	d.Error = &wire.DoneError{Code: codeTooLarge, Message: "the result is larger than the limit"}
	d.Ticks, d.HeapPeak = 0, 0
	return d
}
