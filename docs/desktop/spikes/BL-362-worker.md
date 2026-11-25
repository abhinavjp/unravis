# BL-362 design note: the sandbox worker `unravis-sandbox`

Date 2026-11-25. Branch `factory/bl362-worker` (from `origin/develop`). Spec: `docs/desktop/spec/04-code-mode.md` (D1, D2, D12, D24, section 3.3, BL-362). This note is written and committed BEFORE the worker, as the item asks. Facts measured while building are added at the end under "Measured"; nothing in the first part is a measurement. Where the build differed from this plan, the section "As built: changes from the plan" says so (the plan text above it is left as written, except the export list and the eval switch-off, which are corrected in place).

## Verdict: the spec's runtime, built here, no substitute

**Runtime: QuickJS-ng compiled to WebAssembly (wasi-sdk, wasm32-wasip1, our own C glue), run by the wazero interpreter, driven by Go.** This is exactly D1 / D2 / D24. The build is feasible on this machine, so no fallback runtime is chosen and none is silently substituted.

What was checked before deciding:

- A pinned wasi-sdk for Windows x64 was downloaded to a scratch folder outside the repo and hash-checked; `clang` from it targets `wasm32-unknown-wasip1`. Nothing of the toolchain goes into the repo.
- QuickJS-ng source was downloaded from the release tag and hash-checked; its CMake source list is four C files (`dtoa.c`, `libregexp.c`, `libunicode.c`, `quickjs.c`) plus the optional `quickjs-libc.c`. We do not build `quickjs-libc.c`, which is where `std`, `os`, `print`, timers and file access come from.
- wazero is published up to v1.12.0 (pure Go, no cgo). It is added with `go get github.com/tetratelabs/wazero@v1.12.0` and not through a broad tidy.

Pins (recorded again in `tools/codemode/BUILD-WASM.md`):

| Item | Pin |
|---|---|
| wasi-sdk | 34.0, `wasi-sdk-34.0-x86_64-windows.tar.gz`, SHA-256 `cccb5c323a9b34f0349a9b09e8804a0a7632c68c3310f4b5f437ed57d7e71d8f` (clang 23.1.0-rc3) |
| QuickJS-ng | v0.18.0, commit `d7215b0ad3fc2c562e8317fd9bca058fd4863804`, release tarball `qjs-v0.18.0.tar.gz` SHA-256 `28bcd89aa3683fd2a946537e5ce3f11cc46b5cbf3383c7561aaef3df5ff9f1dd` |
| wazero | v1.12.0, interpreter mode only (`NewRuntimeConfigInterpreter`), no compilation cache (a cache writes to disk) |

The wasm is vendored as `cmd/unravis-sandbox/qjs.wasm` with `qjs.wasm.sha256` (its size is recorded under "Measured"). It is `embed`ded into the worker; the hello frame carries the SHA-256 of the embedded bytes.

### Alternatives and why they are not used

- **Pure-Go in-process interpreter (goja and similar):** not built and not chosen. It would run in the worker's own address space with Go's allocator and no instruction budget of its own: it could enforce a tick count only if it offered an interrupt hook (goja has one), and a memory limit only approximately, because Go heap growth inside the process cannot be capped per run except by the job object around the process (BL-366). A stack limit would be the Go stack, which is bounded by the runtime's 1 GB default and is not catchable, so a deep recursion becomes a fatal Go stack overflow of the whole worker with no distinct code. It also has a much larger attack surface in the same process as the pipe handles. The wasm design puts the engine in a linear memory with a hard page cap, so a memory bomb cannot grow past `memPages` pages whatever the engine does.
- **Wasmtime/Rust (D1's last resort):** needs an owner go, and is not needed.

## Architecture

```
broker (unravis.exe) <== frames on stdin/stdout ==> worker (Go + wazero) ==> qjs.wasm (QuickJS-ng + glue.c)
```

The worker is a Go program with an event loop that owns all I/O. The wasm module is a WASI **reactor** (`-mexec-model=reactor`, no `main`, no Asyncify). Go calls exports; the module calls back into Go through a few host imports. JavaScript never blocks inside wasm waiting for the broker.

Exports (C, `glue.c`, as built): `qjs_init(heapBytes, stackBytes, maxTicks, pollsPerTick, callBytes, maxCalls)`, `qjs_run(js_ptr, js_len, names_ptr, names_len)`, `qjs_ret(id, ok, ptr, len)`, `qjs_pump()` (run pending promise jobs; says whether the run promise settled), `qjs_report()` (a pointer to a fixed report struct: kind, texts, flags), `qjs_alloc/qjs_free` for buffers Go writes into, and `_initialize`.

Imports (Go, module `env`): `host_call(tool, id, argsPtr, argsLen)` (queue a call; Go decides when it is sent), `host_log(ptr, len)`, `host_fatal(code)` (uncatchable end of run: Go records the code and closes the module), `host_random()` (float64), `host_perf_now()` (float64). The module also imports five `wasi_snapshot_preview1` functions (`clock_time_get`, `fd_close`, `fd_fdstat_get`, `fd_seek`, `fd_write`); the Go side provides its own minimal WASI module for them (see Measured). Imports are listed in an allow-list test; any other import (for example a wasi function we do not expect) fails the build of the test.

Event loop in Go: run the script (compile, evaluate, build the `unravis` object), then repeat: `qjs_pump` until nothing is runnable; send queued calls (FIFO, at most `inFlight`, at most `maxCalls + 1` forwarded in all so the broker can count and refuse the 51st); read one frame from stdin when something is in flight; deliver `ret` through `qjs_ret`; stop when the run promise is settled and the job queue is empty. If the promise is pending, nothing is in flight, nothing is queued and no job is left, the script has deadlocked (a promise that never resolves): done with `script_error` instead of hanging.

### Startup order (the memory limit must come first)

1. Build nothing user-controlled; send `hello` (runtime `wazero-interp`, mitigations all false, `wasmSha256` of the embedded bytes). The worker cannot honestly claim ACG, win32k off or child-process restriction: those belong to the spawner (BL-366), which reads the real token. They are all `false` here, and the broker's own check does the rest.
2. Read ONE `run` frame (a second `run` frame later is a protocol violation).
3. Build the wazero runtime with `NewRuntimeConfigInterpreter().WithMemoryLimitPages(memPages)` (the page limit has to be set before the module is compiled), compile and instantiate the embedded wasm, call `qjs_init`.
4. Evaluate the script.

### WASI and the frame stream

`wasi_snapshot_preview1` is instantiated with: no file system config (no preopens), no args, no environment, stdin empty, **stdout and stderr both `io.Discard`** (an fd 1 write from wasm must never reach the frame stream; `log.SetOutput(io.Discard)` for the Go side too), a zero random source, and the clock functions below. The test `TestWasmPinnedAndImports` lists every import of the module and fails on any name that is not in the allow-list.

### Clock and Math.random: the same code as replay

`internal/codemode/privacy` is stdlib plus `internal/secretscrub` and `internal/querytext` (both stdlib), so the worker imports it. `privacy.NewScriptClock(mode, baseMs, nil)` serves WASI wall time (so `Date.now()` and `new Date()` come out of it: 10 ms steps from `baseMs`, never backwards, constant in frozen mode) and `host_perf_now` (`performance.now()`, a small object the glue adds; QuickJS-ng has no `performance` unless `JS_AddPerformance` is called, and we do not call it). `privacy.NewRand(seed)` serves `host_random`, installed as `Math.random` by the glue. Replay (BL-370) therefore matches by construction; `TestMathRandomSeeded` compares the script's numbers with `privacy.Rand` directly.

## How eval and the Function constructors are made impossible

This is done in the engine, not by scrubbing globals.

QuickJS keeps the compiler behind one function pointer in the context (`ctx->eval_internal`), installed by `JS_AddIntrinsicEval`. The glue is one translation unit that does `#include "quickjs.c"` (no patch to upstream), so it can reach the field. **As built there is ONE context, not two** (the first plan above in this note, two contexts and a bytecode round trip, was dropped as more code for no gain): the glue creates the context with the intrinsics, compiles the script with `JS_Eval(... JS_EVAL_FLAG_COMPILE_ONLY)`, then sets `ctx->eval_internal = NULL`, and only then runs the compiled function. After that point `eval`, `new Function`, `AsyncFunction`, `GeneratorFunction` and `AsyncGeneratorFunction` all fail at the engine level, because no code path can compile text any more. Nothing is evaluated before the switch except the prelude (our own text, run before the script is compiled and before `eval_internal` is cleared). No module loader is set, so `import()` fails.

On top of that (belt and braces, and so the globals are visibly gone): `eval`, `Function`, `SharedArrayBuffer` and `Atomics` are deleted from the global object, and the `constructor` property of the four function prototypes is redefined as undefined, non-writable, non-configurable. No module loader is set (`JS_SetModuleLoaderFunc` is never called), so `import()` rejects/throws; `quickjs-libc.c` is not linked, so there is no `std`, `os`, `print`, `setTimeout`, `scriptArgs`. `fetch`, `WebAssembly`, `process`, `require` simply do not exist in QuickJS. A test checks every name in the spec list.

### The `unravis` object

Built in C. Each `audit_*` function is a `JS_NewCFunctionData` whose tool index is held in function data, so changing a prototype or a global (`JSON`, `Object.prototype`, `Promise.prototype.then`) cannot change the tool name or the call id. Arguments go through C `JS_JSONStringify` (a call whose arguments do not stringify throws a TypeError in the script), results through `JS_ParseJSON`. The returned value is a native Promise created in C; `qjs_ret` settles it. The object is defined with flags 0, `JS_PreventExtensions` is applied, and the global `unravis` is non-writable, non-configurable. The script is compiled sloppy inside a wrapper, so a write to a frozen property fails silently; tests assert the value did not change, not that a throw happened.

## Limits, each with its own code

| Limit | Mechanism | Code |
|---|---|---|
| ticks | The interrupt handler runs every `tickPolls` polls (writes `ctx->interrupt_counter`; QuickJS polls at loop back-edges, calls and some built-in loops, and the regexp engine calls the same handler). Each handler call is one tick. (Not as first planned: promise jobs do not count a tick of their own; they are bounded by the polls inside the jobs, and the promise-loop test checks it.) Reaching `ticks` calls `host_fatal`. | `ticks` |
| heap (oom) | A counting allocator under `JS_NewRuntime2` ends the run with `host_fatal(oom)` the moment a request would pass `heapBytes` (no sticky flag and no catchable error: the end is uncatchable, so a script cannot swallow it). A failed wasm `memory.grow` is also `oom`. `heapBytes` is clamped below `memPages * 64 KiB` (see As built). | `oom` |
| stack | `JS_SetMaxStackSize(min(stackBytes, linked stack - margin))`; the shadow stack is fixed at link time (`-Wl,-z,stack-size`), so the request is clamped to it. QuickJS throws a catchable `RangeError` (not an uncatchable InternalError as first planned); an uncaught one is reported with kind 3 and mapped to `stack`. If wazero's call-depth ceiling traps first, that trap is mapped to `stack` too. See As built and Measured. | `stack` |
| output | The result is serialised with `JSON.stringify` semantics in C; more than `callBytes` (1 MiB) is `output_too_large`. `undefined` is `null`. | `output_too_large` |
| logs | `console.log` joins its arguments, caps a line at 8 KiB, caps the total at `logBytes` (the broker counts len+1 per line) and 2000 lines; the rest is dropped and `logsTruncated` is set. A flood costs the script its own ticks, not memory. | not an error |
| script | A thrown error or a rejected run promise, a syntax error, a deadlock. The message and stack are cut to 8 KiB; the broker scrubs them. | `script_error` |

No precedence order among the limits is implemented (the first plan listed one): `oom`, `ticks` and a wasm stack trap end the run at the moment they happen through `host_fatal` or a trap, and the first one wins; a script that ends by itself is classified from the engine's report (`output_too_large`, `stack` from an uncaught `RangeError`, `script_error`, or ok).

Calls: the worker forwards at most `maxCalls + 1` calls in all (the broker counts the 51st and denies it); a call beyond that gets a local rejection and never reaches the pipe. FIFO at `inFlight` (4); the queued argument bytes are capped (4 MiB), more is a local rejection (`call_queue_full`), so a script cannot make the worker hold unbounded memory outside the wasm. Any single call over `callBytes` is rejected locally.

### What the worker cannot enforce

- Wall time: the broker owns it (run cap, kill). The worker has no timer of its own except that the tick count bounds JavaScript. A native built-in that does a very long loop without polling the interrupt could run longer than its tick count suggests; the broker's kill is the backstop. Measured cases are listed under "Measured".
- Process mitigations, job object, LPAC, handle list: BL-366/367.
- A wasm engine bug (a miscompile or a QuickJS memory-safety bug) is contained by the linear memory and by the absence of any import that gives the module a file, a socket or a clock beyond the quantised one; it can still corrupt the script's own results.

## Protocol violations and the worker's exit convention

The worker never trusts the broker end either. A frame over `MaxFrame` (checked from the length before anything is allocated), bad JSON or a repeated or unknown field, an unknown `t`, a `ret` for an unknown or repeated id, a second `run` frame, or a `run` frame whose limits are out of range: the worker **exits with a non-zero status and sends no `done`**. The broker reads the closed pipe as a crashed worker (`crashed`), which is also the right outcome for a misbehaving broker. Everything that crosses out of wasm (pointers, lengths, tool names, JSON) is treated as hostile: memory is read only through `api.Memory.Read` and its ok result is checked, size caps are applied before the read, a tool name must be in the `run` frame's list, and arguments must be valid JSON.

`done` is sized to fit `MaxFrame`: on `ErrFrameTooLarge` from `wire.Marshal` the logs are trimmed (`logsTruncated` set) and the frame is built again; if the result alone does not fit, the run is `output_too_large`.

The worker exits after one run, after the `done` frame is written and stdout closed.

## Build

`tools/codemode/BUILD-WASM.md` is the recipe: unpack the two pinned archives, compile `glue.c` (which includes `quickjs.c`, plus `dtoa.c`, `libregexp.c`, `libunicode.c`) with `-O2 -DNDEBUG -mexec-model=reactor -Wl,--export=... -Wl,-z,stack-size=... -ffile-prefix-map=<src>=.`, no `-lc` extras, check the hash twice. `cmd/unravis-sandbox/glue/` holds C and JS files and no `.go` file (Go refuses C files in a non-cgo package). The `.wasm` is `binary` in `.gitattributes` already, so checkout never rewrites it.

## Tests

The tests run the real built worker as a plain child process (no confined spawner, BL-366/367) with the broker-side `wire` package and a fake host that answers `call` frames from a table and records the largest number of calls in flight. Every read has a deadline and the process is killed in cleanup. Limits are sent small in the `run` frame so the tests are quick. The list is in the item plus the escape list; `TestWorkerDepsAllowList` and `TestAppDoesNotLinkWazero` use `go list -deps` and match on module path segments.

Not covered here (and not claimed): the Windows confinement, the signed exe, and the Windows acceptance run of the item (ticks stay unticked in `docs/BACKLOG.md`).

## As built: changes from the plan

- **One context, `eval_internal = NULL` after compile** (not two contexts); see the section on eval above.
- **Intrinsics.** BaseObjects, Date, RegExp (with the compiler), JSON, Proxy, MapSet, TypedArrays, Promise and BigInt are present; WeakRef, DOMException, atob/btoa and all of `quickjs-libc` are not. `Proxy` was not in the first plan's list.
- **Own minimal WASI module.** wazero's `wasi_snapshot_preview1` is not instantiated at all. The Go side registers the five functions the module imports; `clock_time_get` is served from `privacy.ScriptClock`, `fd_close`/`fd_fdstat_get`/`fd_seek`/`fd_write` return EBADF (a write to fd 1 or 2 from wasm goes nowhere). No preopen, args or environment exist because the functions that would list them are not imported.
- **Not implemented from the plan:** `WithCloseOnContextDone`, a sticky OOM flag, a precedence order between limits, promise jobs counting a tick each, and the `qjs_take_*` exports. Implemented as planned: the 4 MiB queued-argument cap with a local `call_queue_full` rejection (`maxQueuedBytes`, glue `CALL_QUEUE_FULL`), the local rejection of a call over `callBytes` (glue), the 2000-line and per-line 8 KiB log caps, the 8 KiB cut of error message and stack (`MAX_TEXT` in the glue), the deadlock `script_error`, and the `done` fit rule (logs first, then `output_too_large`).
- **Release build.** `CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -H=windowsgui"` gives a 7.4 MB exe; the whole test suite was run against it (set `UNRAVIS_SANDBOX_TEST_BIN` to the exe) and passes, so the GUI-subsystem exe speaks over inherited pipes. **`build.ps1` is NOT changed**: the spec says to add the worker build before the app build, but where the exe lands and how the installer and the spawner (BL-366) find it are not decided here; that line of BL-362 is open.
- **Heap clamp.** `heapBytes` is clamped to `memPages * 64 KiB` minus a 6 MiB reserve (`memReserveBytes`) for the linked stack, globals and allocator headers, so QuickJS fails before wasm does. A heap above that is a clamp, not a violation.
- **Exit codes.** 0 for a run that ended with a `done` frame (success or failure); 2 for a protocol violation or a run frame that failed validation (no `done`); 3 for a worker failure (no `done`). stderr is never written.
- **Stack code has two paths.** wazero has a call-frame ceiling of 2000; deep wasm recursion hits it first and the trap is an uncatchable "stack overflow", mapped to `stack`. QuickJS's own check (an uncaught `RangeError: Maximum call stack size exceeded`) is reported with kind 3 and also gives `stack`. A script can catch the `RangeError` and keep running (it is still bounded by ticks and heap); a script can also throw its own `RangeError` with that exact text and get the `stack` label, which only labels its own failure.
- **Call limit.** `qjs_init` receives `maxCalls + 1`: the broker sees (and denies) the 51st call; the 52nd is rejected in the worker without touching the pipe.
- **`hello`.** Mitigations are all `false` (the spawner owns them: BL-366). `wasmSha256` is the SHA-256 of the embedded bytes and a test compares it with `qjs.wasm.sha256`.

## Measured

All numbers are from one Windows 11 x64 machine, Go 1.27, wazero interpreter, on 2026-11-25. They are single runs, not a benchmark.

| What | Value |
|---|---|
| `qjs.wasm` | 1,021,359 bytes, SHA-256 `55c00c95...6c01cf8` (full value in `qjs.wasm.sha256`); three builds gave the same hash |
| worker `.exe` | 10.4 MB plain `go build`; 7.4 MB with `-trimpath -ldflags "-s -w"` (the wasm is embedded in both) |
| startup (hello to first script instruction) | about 65 ms warm, about 650 ms the first time after a rebuild (wazero compiles the module's function bodies for the interpreter every start; no cache, because a cache writes to disk) |
| interpreter speed | about 3 million empty-loop iterations in 9 s, i.e. a script has to be small; this is the cost of the interpreter and of no JIT |
| wasm stack | 1 MiB linked; with `stackBytes` = 256 KiB a plain recursion reaches about 776 JavaScript frames before QuickJS throws |
| tick | one tick = 10 interrupt polls (`tickPolls = 10`); the interrupt is polled at loop back-edges, calls and some builtin loops; the number 10 is a calibration choice, not derived: with it the limit tests finish in seconds on this machine |

### Known gaps (reviewed candidly)

1. **Ticks do not bound wall time inside native builtins.** `JSON.stringify`/`parse` on big data, `Array.prototype.flat`, `String.prototype.repeat` and similar do their loop in C without polling. A regexp polls only about every 10,000 backtracking steps, so one tick there is far more work than one tick of plain JavaScript: a catastrophic-backtracking pattern took 68 s to hit a 2000-tick limit, and the test uses 40 ticks. The memory limit still stops allocation, but a CPU-only native loop is stopped only by the **broker's wall timer and kill** (BL-363/366). The worker has no timer of its own.
2. **A caught stack overflow lets the script continue** (see above). It is still limited by ticks and heap.
3. **The interpreter is slow** (see table). Larger scripts or heavy post-processing will hit `ticks` or the broker's cap early. A JIT is not available in wazero under the no-cache, no-write rule and is not wanted for confinement.
4. **String concatenation uses ropes.** `s += s` fails with `script_error` ("string too long") rather than `oom` until the string is flattened. Both outcomes are bounded; the code differs.
5. **Intrinsics are not frozen.** A script can change `Object.prototype`, `Array.prototype` and `JSON` in its own world. It cannot reach the tool names or call ids (held in C function data), and it cannot change the frozen `unravis` object, but it can change what its own later code sees.
6. **Hello mitigations are all false** because the worker cannot see its token; BL-366 owns that and the broker must check it.
7. **Nothing here is verified on Windows confinement** (job object, LPAC, handle list, ACG, win32k, signed exe) nor on live data. BL-362 acceptance item 5 (the live PC with the real exe) is not run, so BACKLOG is not ticked.

### Tests run (all against the real built worker as a child process)

`TestFrameTooLargeRefused`, `TestBadFrameEndsRun`, `TestOneRunThenExit`, `TestHelloIsFirstAndHonest`, `TestLimitsDistinctCodes` (memory, stack, ticks, output, script, log cap cases), `TestClockQuantized10ms`, `TestMathRandomSeeded`, `TestGlobalsRemoved`, the escape and queue tests in `behavior_test.go`, `TestWorkerDepsAllowList`, `TestAppDoesNotLinkWazero`, `TestWasmPinnedAndImports`. The escape tests were checked by mutation: with the defenses taken out of the glue (the `eval_internal = NULL` switch-off and the prototype `constructor` removal) they fail; with the glue restored (byte-identical to the committed one) they pass.
