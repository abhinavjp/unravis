package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/sys"

	"api-audit-explorer/internal/codemode/privacy"
	"api-audit-explorer/internal/codemode/wire"
)

// The numbers the worker holds to, whatever the run frame says.
const (
	minMemPages = 192  // 12 MiB: the engine's stack, data and a heap worth having
	maxMemPages = 4096 // 256 MiB
	// memReserveBytes is what stays free of script data inside the memory limit: the 1 MiB engine stack, the data segment, the malloc
	// bookkeeping and the slack a growing array needs while it moves.
	memReserveBytes = 6 << 20
	minHeapBytes    = 1 << 20
	maxHeapBytes    = 256 << 20
	minStackBytes   = 16 << 10
	maxStackBytes   = 1 << 20 // the engine's own stack is 1 MiB (BUILD-WASM.md); the glue keeps a margin below it
	maxTicks        = 1<<31 - 1
	maxCallBytes    = 1 << 20
	maxLogBytes     = 1 << 20
	maxToolCount    = 64
	// Engine time: the worker stops the engine after this long in total (waits for host calls do not count). Built-ins such as
	// Array.prototype.join on a huge array-like poll no tick, so only a deadline stops them.
	defaultEngineMs = 125_000
	minEngineMs     = 50
	maxEngineMs     = 300_000
	maxToolName     = 64
	maxQueuedBytes  = 4 << 20 // argument bytes waiting for an in-flight slot
	maxLogLines     = 2000
	maxInFlight     = 4
	maxCallsCeiling = 1000
	maxLineBytes    = 8 << 10 // the glue cuts a console line here (MAX_LINE in glue.c)
	maxTextBytes    = 16 << 10
	// tickPolls is how many interrupt polls of the engine make one tick (see BL-362-worker.md, "Measured").
	tickPolls = 10
)

// Host call answers (glue.c).
const (
	callOK        = 0
	callBudget    = 1
	callQueueFull = 2
)

// Fatal codes the glue passes to host_fatal.
const (
	fatalTicks = 1
	fatalOOM   = 2
)

// Error codes of the done frame.
const (
	codeScriptError = "script_error"
	codeTicks       = "ticks"
	codeOOM         = "oom"
	codeStack       = "stack"
	codeTooLarge    = "output_too_large"
)

var toolNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// params are the validated numbers of the run frame.
type params struct {
	memPages   uint32
	heapBytes  uint32
	stackBytes uint32
	ticks      uint32
	callBytes  uint32
	logBytes   int
	maxCalls   int
	inFlight   int
	engineTime time.Duration
}

// checkRun validates the run frame. Anything out of range is a protocol violation: the broker builds these numbers, so a bad one is a
// bug or an attack on the other side and the worker does not guess.
func checkRun(run wire.Run) (params, *privacy.ScriptClock, *privacy.Rand, error) {
	var p params
	l := run.Limits
	switch {
	case run.JS == "":
		return p, nil, nil, violation("empty script")
	case len(run.JS) > wire.MaxFrame:
		return p, nil, nil, violation("script too long")
	case l.MemPages < minMemPages || l.MemPages > maxMemPages:
		return p, nil, nil, violation("memPages out of range")
	case l.HeapBytes < minHeapBytes || l.HeapBytes > maxHeapBytes:
		return p, nil, nil, violation("heapBytes out of range")
	case l.StackBytes < minStackBytes || l.StackBytes > maxStackBytes:
		return p, nil, nil, violation("stackBytes out of range")
	case l.Ticks < 1 || l.Ticks > maxTicks:
		return p, nil, nil, violation("ticks out of range")
	case l.CallBytes < 1 || l.CallBytes > maxCallBytes:
		return p, nil, nil, violation("callBytes out of range")
	case l.LogBytes < 0 || l.LogBytes > maxLogBytes:
		return p, nil, nil, violation("logBytes out of range")
	case l.MaxCalls < 1 || l.MaxCalls > maxCallsCeiling:
		return p, nil, nil, violation("maxCalls out of range")
	case l.InFlight < 1 || l.InFlight > maxInFlight:
		return p, nil, nil, violation("inFlight out of range")
	case l.EngineMs != 0 && (l.EngineMs < minEngineMs || l.EngineMs > maxEngineMs):
		return p, nil, nil, violation("engineMs out of range")
	case len(run.Tools) > maxToolCount:
		return p, nil, nil, violation("too many tools")
	}
	seen := map[string]bool{}
	for _, t := range run.Tools {
		if len(t) == 0 || len(t) > maxToolName || !toolNamePattern.MatchString(t) || seen[t] {
			return p, nil, nil, violation("bad tool name")
		}
		seen[t] = true
	}
	mem := uint64(l.MemPages) * 65536
	heap := uint64(l.HeapBytes)
	if limit := mem - memReserveBytes; heap > limit {
		heap = limit
	}
	p = params{
		memPages:   uint32(l.MemPages),
		heapBytes:  uint32(heap),
		stackBytes: uint32(l.StackBytes),
		ticks:      uint32(l.Ticks),
		callBytes:  uint32(l.CallBytes),
		logBytes:   l.LogBytes,
		maxCalls:   l.MaxCalls,
		inFlight:   l.InFlight,
		engineTime: time.Duration(l.EngineMs) * time.Millisecond,
	}
	if p.engineTime == 0 {
		p.engineTime = defaultEngineMs * time.Millisecond
	}
	var mode privacy.ClockMode
	switch run.Clock.Mode {
	case string(privacy.ClockLive):
		mode = privacy.ClockLive
	case string(privacy.ClockFrozen):
		mode = privacy.ClockFrozen
	default:
		return p, nil, nil, violation("unknown clock mode")
	}
	clock, err := privacy.NewScriptClock(mode, run.Clock.BaseMs, nil)
	if err != nil {
		return p, nil, nil, violation("bad clock")
	}
	rnd, err := privacy.NewRand(run.Seed)
	if err != nil {
		return p, nil, nil, violation("bad seed")
	}
	return p, clock, rnd, nil
}

// pendingCall is a host call the script made that has not been sent to the broker yet.
type pendingCall struct {
	id   uint32
	tool int
	args []byte
}

// host holds everything the engine's imports touch. One goroutine uses it (the engine calls its imports on the goroutine that called the
// export), so it has no lock.
type host struct {
	p     params
	tools []string
	clock *privacy.ScriptClock
	rnd   *privacy.Rand
	mem   api.Memory

	queue       []pendingCall
	queuedBytes int
	forwarded   int
	inflight    map[uint32]struct{}

	logs          []string
	logBytes      int
	logsTruncated bool
	logsFull      bool

	fatalCode  int32
	fatalTicks int64
	fatalHeap  int64

	// broken is set when the engine does something its own glue never does (a bad pointer, a bad tool index): the engine is
	// compromised or buggy, and the run ends as a worker failure.
	broken error
}

func (h *host) read(ptr, n uint32) ([]byte, bool) {
	if n == 0 {
		return nil, true
	}
	b, ok := h.mem.Read(ptr, n)
	if !ok {
		return nil, false
	}
	return b, true
}

// hostCall is import env.host_call(tool, id, ptr, len) -> status.
func (h *host) hostCall(_ context.Context, _ api.Module, st []uint64) {
	tool, id, ptr, n := uint32(st[0]), uint32(st[1]), uint32(st[2]), uint32(st[3]) //nolint:gosec // wasm i32 parameters
	st[0] = callOK
	if h.forwarded >= h.p.maxCalls+1 {
		st[0] = callBudget
		return
	}
	if int(tool) >= len(h.tools) || n == 0 || n > h.p.callBytes {
		h.broken = fmt.Errorf("host_call with tool %d, length %d", tool, n)
		st[0] = callBudget
		return
	}
	b, ok := h.read(ptr, n)
	if !ok || b[0] != '{' || !json.Valid(b) {
		h.broken = fmt.Errorf("host_call with unreadable or invalid arguments")
		st[0] = callBudget
		return
	}
	if h.queuedBytes+len(b) > maxQueuedBytes {
		st[0] = callQueueFull
		return
	}
	h.queue = append(h.queue, pendingCall{id: id, tool: int(tool), args: append([]byte(nil), b...)})
	h.queuedBytes += len(b)
	h.forwarded++
}

// hostLog is import env.host_log(ptr, len) -> 0 when the line was kept, 1 when the log is full (the engine then stops formatting).
func (h *host) hostLog(_ context.Context, _ api.Module, st []uint64) {
	ptr, n := uint32(st[0]), uint32(st[1]) //nolint:gosec // wasm i32 parameters
	st[0] = 0
	if h.logsFull {
		st[0] = 1
		return
	}
	b, ok := h.read(ptr, n)
	if !ok || n > maxLineBytes {
		h.broken = fmt.Errorf("host_log with a bad range")
		st[0] = 1
		return
	}
	line := string(b)
	if !utf8.ValidString(line) {
		line = strings.ToValidUTF8(line, "�")
	}
	if n >= maxLineBytes {
		h.logsTruncated = true // the engine cut this line
	}
	if len(h.logs) >= maxLogLines || h.logBytes+len(line)+1 > h.p.logBytes {
		h.logsTruncated = true
		h.logsFull = true
		st[0] = 1
		return
	}
	h.logs = append(h.logs, line)
	h.logBytes += len(line) + 1
}

// hostFatal is import env.host_fatal(code, ticks, heapPeak): the engine ends the run (tick limit, memory limit). It never returns.
func (h *host) hostFatal(ctx context.Context, mod api.Module, st []uint64) {
	h.fatalCode = int32(st[0])        //nolint:gosec // wasm i32 parameter
	h.fatalTicks = int64(st[1])       //nolint:gosec // wasm i64 parameter
	h.fatalHeap = int64(st[2])        //nolint:gosec // wasm i64 parameter
	_ = mod.CloseWithExitCode(ctx, 1) // the module is dead for any later call
	panic(sys.NewExitError(1))
}

func (h *host) hostRandom(_ context.Context, _ api.Module, st []uint64) {
	st[0] = api.EncodeF64(h.rnd.Float64())
}

func (h *host) hostPerfNow(_ context.Context, _ api.Module, st []uint64) {
	st[0] = api.EncodeF64(h.clock.PerfNowMs())
}

// The WASI surface the engine imports (the allow-list test pins it): a clock, and the four file calls the C library's stdio start-up
// makes, which all answer "bad file descriptor". There is no file, directory, socket, environment, argument or random call at all, so
// the worker does not even instantiate wazero's WASI implementation.
const (
	errnoSuccess = 0
	errnoBadf    = 8
	errnoInval   = 28
)

func (h *host) clockTimeGet(_ context.Context, _ api.Module, st []uint64) {
	id, ptr := uint32(st[0]), uint32(st[2]) //nolint:gosec // wasm i32 parameters
	var ns uint64
	switch id {
	case 0: // realtime
		ns = uint64(h.clock.DateNowMs()) * uint64(time.Millisecond) //nolint:gosec // base is checked non-negative
	case 1: // monotonic
		ns = uint64(h.clock.PerfNowMs()) * uint64(time.Millisecond)
	default:
		st[0] = errnoInval
		return
	}
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], ns)
	if !h.mem.Write(ptr, buf[:]) {
		h.broken = fmt.Errorf("clock_time_get with a bad pointer")
		st[0] = errnoInval
		return
	}
	st[0] = errnoSuccess
}

func badf(_ context.Context, _ api.Module, st []uint64) { st[0] = errnoBadf }

// fdWrite reports zero bytes written and a bad descriptor: a C library message goes nowhere.
func (h *host) fdWrite(_ context.Context, _ api.Module, st []uint64) {
	nwritten := uint32(st[3]) //nolint:gosec // wasm i32 parameter
	_ = h.mem.WriteUint32Le(nwritten, 0)
	st[0] = errnoBadf
}

// engine is one instance of the wasm module and the exports the worker drives.
type engine struct {
	rt  wazero.Runtime
	mod api.Module
	h   *host
	// spent is the engine time used so far; call stops the engine when it reaches h.p.engineTime.
	spent time.Duration

	fInit, fRun, fRet, fPump, fAlloc, fFree, fReport api.Function
}

type zeroReader struct{}

func (zeroReader) Read(b []byte) (int, error) {
	clear(b)
	return len(b), nil
}

var _ io.Reader = zeroReader{}

// newEngine compiles and starts the module with no capability beyond the imports above.
func newEngine(ctx context.Context, h *host) (*engine, error) {
	cfg := wazero.NewRuntimeConfigInterpreter().WithMemoryLimitPages(h.p.memPages).WithCloseOnContextDone(true)
	rt := wazero.NewRuntimeWithConfig(ctx, cfg)
	e := &engine{rt: rt, h: h}

	i32, i64, f64 := api.ValueTypeI32, api.ValueTypeI64, api.ValueTypeF64
	fn := func(b wazero.HostModuleBuilder, name string, f api.GoModuleFunc, in, out []api.ValueType) {
		b.NewFunctionBuilder().WithGoModuleFunction(f, in, out).Export(name)
	}
	env := rt.NewHostModuleBuilder("env")
	fn(env, "host_call", h.hostCall, []api.ValueType{i32, i32, i32, i32}, []api.ValueType{i32})
	fn(env, "host_log", h.hostLog, []api.ValueType{i32, i32}, []api.ValueType{i32})
	fn(env, "host_fatal", h.hostFatal, []api.ValueType{i32, i64, i64}, nil)
	fn(env, "host_random", h.hostRandom, nil, []api.ValueType{f64})
	fn(env, "host_perf_now", h.hostPerfNow, nil, []api.ValueType{f64})
	if _, err := env.Instantiate(ctx); err != nil {
		return nil, fmt.Errorf("env module: %w", err)
	}
	wasi := rt.NewHostModuleBuilder("wasi_snapshot_preview1")
	fn(wasi, "clock_time_get", h.clockTimeGet, []api.ValueType{i32, i64, i32}, []api.ValueType{i32})
	fn(wasi, "fd_close", badf, []api.ValueType{i32}, []api.ValueType{i32})
	fn(wasi, "fd_fdstat_get", badf, []api.ValueType{i32, i32}, []api.ValueType{i32})
	fn(wasi, "fd_seek", badf, []api.ValueType{i32, i64, i32, i32}, []api.ValueType{i32})
	fn(wasi, "fd_write", h.fdWrite, []api.ValueType{i32, i32, i32, i32}, []api.ValueType{i32})
	if _, err := wasi.Instantiate(ctx); err != nil {
		return nil, fmt.Errorf("wasi module: %w", err)
	}

	compiled, err := rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		return nil, fmt.Errorf("compile: %w", err)
	}
	mc := wazero.NewModuleConfig().WithName("qjs").WithStartFunctions("_initialize").WithRandSource(zeroReader{})
	mod, err := rt.InstantiateModule(ctx, compiled, mc)
	if err != nil {
		return nil, fmt.Errorf("instantiate: %w", err)
	}
	e.mod = mod
	h.mem = mod.Memory()
	for name, dst := range map[string]*api.Function{
		"qjs_init": &e.fInit, "qjs_run": &e.fRun, "qjs_ret": &e.fRet, "qjs_pump": &e.fPump,
		"qjs_alloc": &e.fAlloc, "qjs_free": &e.fFree, "qjs_report": &e.fReport,
	} {
		f := mod.ExportedFunction(name)
		if f == nil {
			return nil, fmt.Errorf("the engine has no export %s", name)
		}
		*dst = f
	}
	return e, nil
}

func (e *engine) close(ctx context.Context) { _ = e.rt.Close(ctx) }

// call runs one export. The error is wazero's (a trap, or the exit that host_fatal raised).
func (e *engine) call(ctx context.Context, f api.Function, args ...uint64) (uint64, error) {
	left := e.h.p.engineTime - e.spent
	if left <= 0 {
		return 0, errEngineTime
	}
	cctx, cancel := context.WithTimeout(ctx, left)
	start := time.Now()
	res, err := f.Call(cctx, args...)
	e.spent += time.Since(start)
	timedOut := cctx.Err() != nil
	cancel()
	if err != nil {
		if timedOut {
			return 0, errEngineTime
		}
		return 0, err
	}
	if len(res) == 0 {
		return 0, nil
	}
	return res[0], nil
}

// put copies b into the engine's memory with a terminating zero byte and returns the address (0 when the engine is out of memory).
func (e *engine) put(ctx context.Context, b []byte) (uint32, error) {
	r, err := e.call(ctx, e.fAlloc, uint64(len(b)))
	if err != nil {
		return 0, err
	}
	ptr := uint32(r) //nolint:gosec // wasm i32 result
	if ptr == 0 {
		return 0, nil
	}
	buf := make([]byte, len(b)+1)
	copy(buf, b)
	if !e.h.mem.Write(ptr, buf) {
		return 0, fmt.Errorf("alloc returned a bad pointer")
	}
	return ptr, nil
}

func (e *engine) free(ctx context.Context, ptr uint32) {
	if ptr != 0 {
		_, _ = e.call(ctx, e.fFree, uint64(ptr))
	}
}

// report is the engine's account of how the run ended (struct Report in glue.c).
type report struct {
	ticks, heapPeak uint64
	kind            uint32
	result          []byte
	message, stack  string
}

const (
	kindOK       = 0
	kindError    = 1
	kindTooLarge = 2
	kindStack    = 3
)

func (e *engine) report(ctx context.Context, callBytes uint32) (report, error) {
	var r report
	addr, err := e.call(ctx, e.fReport)
	if err != nil {
		return r, err
	}
	raw, ok := e.h.mem.Read(uint32(addr), 48) //nolint:gosec // wasm i32 result
	if !ok {
		return r, fmt.Errorf("bad report pointer")
	}
	le := binary.LittleEndian
	r.ticks = le.Uint64(raw[0:])
	r.heapPeak = le.Uint64(raw[8:])
	r.kind = le.Uint32(raw[16:])
	text := func(ptrOff int, limit uint32) ([]byte, error) {
		ptr, n := le.Uint32(raw[ptrOff:]), le.Uint32(raw[ptrOff+4:])
		if n == 0 {
			return nil, nil
		}
		if n > limit {
			return nil, fmt.Errorf("report text too long")
		}
		b, ok := e.h.mem.Read(ptr, n)
		if !ok {
			return nil, fmt.Errorf("bad report text pointer")
		}
		return append([]byte(nil), b...), nil
	}
	res, err := text(20, callBytes)
	if err != nil {
		return r, err
	}
	msg, err := text(28, maxTextBytes)
	if err != nil {
		return r, err
	}
	stk, err := text(36, maxTextBytes)
	if err != nil {
		return r, err
	}
	r.result = res
	r.message = strings.ToValidUTF8(string(msg), "�")
	r.stack = strings.ToValidUTF8(string(stk), "�")
	return r, nil
}
