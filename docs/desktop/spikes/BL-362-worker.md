# BL-362 design note: the sandbox worker `unravis-sandbox`

Date 2026-11-25. Branch `factory/bl362-worker` (from `origin/develop`). Spec: `docs/desktop/spec/04-code-mode.md` (D1, D2, D12, D24, section 3.3, BL-362). This note is written and committed BEFORE the worker, as the item asks. Facts measured while building are added at the end under "Measured" in a later commit; nothing in the first part is a measurement.

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

Exports (C, `glue.c`): `qjs_init(heapLimit, stackLimit)`, `qjs_run(js_ptr, js_len, tools_ptr, tools_len)`, `qjs_ret(id, ok, ptr, len)`, `qjs_pump()` (run pending promise jobs; says whether the run promise settled and where its value is), `qjs_alloc/qjs_free` for buffers Go writes into, and `qjs_take_*` for result, log and error text.

Imports (Go, module `env`): `host_call(tool, id, argsPtr, argsLen)` (queue a call; Go decides when it is sent), `host_log(ptr, len)`, `host_fatal(code)` (uncatchable end of run: Go records the code and closes the module), `host_random()` (float64), `host_perf_now()` (float64). Imports are listed in an allow-list test; any other import (for example a wasi function we do not expect) fails the build of the test.

Event loop in Go: run the script (compile, evaluate, build the `unravis` object), then repeat: `qjs_pump` until nothing is runnable; send queued calls (FIFO, at most `inFlight`, at most `maxCalls + 1` forwarded in all so the broker can count and refuse the 51st); read one frame from stdin when something is in flight; deliver `ret` through `qjs_ret`; stop when the run promise is settled and the job queue is empty. If the promise is pending, nothing is in flight, nothing is queued and no job is left, the script has deadlocked (a promise that never resolves): done with `script_error` instead of hanging.

### Startup order (the memory limit must come first)

1. Build nothing user-controlled; send `hello` (runtime `wazero-interp`, mitigations all false, `wasmSha256` of the embedded bytes). The worker cannot honestly claim ACG, win32k off or child-process restriction: those belong to the spawner (BL-366), which reads the real token. They are all `false` here, and the broker's own check does the rest.
2. Read ONE `run` frame (a second `run` frame later is a protocol violation).
3. Build the wazero runtime with `NewRuntimeConfigInterpreter().WithMemoryLimitPages(memPages).WithCloseOnContextDone(true)` (the page limit has to be set before the module is compiled), compile and instantiate the embedded wasm, call `qjs_init`.
4. Evaluate the script.

### WASI and the frame stream

`wasi_snapshot_preview1` is instantiated with: no file system config (no preopens), no args, no environment, stdin empty, **stdout and stderr both `io.Discard`** (an fd 1 write from wasm must never reach the frame stream; `log.SetOutput(io.Discard)` for the Go side too), a zero random source, and the clock functions below. The test `TestWasmImports` lists every import of the module and fails on any name that is not in the allow-list.

### Clock and Math.random: the same code as replay

`internal/codemode/privacy` is stdlib plus `internal/secretscrub` and `internal/querytext` (both stdlib), so the worker imports it. `privacy.NewScriptClock(mode, baseMs, nil)` serves WASI wall time (so `Date.now()` and `new Date()` come out of it: 10 ms steps from `baseMs`, never backwards, constant in frozen mode) and `host_perf_now` (`performance.now()`, a small object the glue adds; QuickJS-ng has no `performance` unless `JS_AddPerformance` is called, and we do not call it). `privacy.NewRand(seed)` serves `host_random`, installed as `Math.random` by the glue. Replay (BL-370) therefore matches by construction; `TestMathRandomSeeded` compares the script's numbers with `privacy.Rand` directly.

## How eval and the Function constructors are made impossible

This is done in the engine, not by scrubbing globals.

QuickJS keeps the compiler behind one function pointer in the context (`ctx->eval_internal`), installed only by `JS_AddIntrinsicEval`. The glue is one translation unit that does `#include "quickjs.c"` (no patch to upstream), so it can reach it. The glue builds TWO contexts:

- **Context A (compile only):** `JS_NewContextRaw` with the Eval and RegExp-compiler intrinsics. It only ever turns the script text into bytecode (`JS_Eval` with `JS_EVAL_FLAG_COMPILE_ONLY`), then `JS_WriteObject` serialises it. The script never runs here.
- **Context B (the script's world):** `JS_NewContextRaw` with exactly BaseObjects, Date, RegExp (the compiler is needed by the regexp engine, not by user code), JSON, MapSet, TypedArrays, Promise, BigInt. `JS_AddIntrinsicEval` is **not** added, so `eval_internal` is NULL, and `eval`, `new Function`, `AsyncFunction`, `GeneratorFunction`, `AsyncGeneratorFunction` all throw "eval is not supported" at the engine level. The bytecode from A is loaded into B with `JS_ReadObject` and run with `JS_EvalFunction`.

On top of that (belt and braces, and so the globals are visibly gone): `eval`, `Function`, `SharedArrayBuffer` and `Atomics` are deleted from the global object, and the `constructor` property of the four function prototypes is redefined as undefined, non-writable, non-configurable. No module loader is set (`JS_SetModuleLoaderFunc` is never called), so `import()` rejects/throws; `quickjs-libc.c` is not linked, so there is no `std`, `os`, `print`, `setTimeout`, `scriptArgs`. `fetch`, `WebAssembly`, `process`, `require` simply do not exist in QuickJS. A test checks every name in the spec list.

### The `unravis` object

Built in C. Each `audit_*` function is a `JS_NewCFunctionData` whose tool index is held in function data, so changing a prototype or a global (`JSON`, `Object.prototype`, `Promise.prototype.then`) cannot change the tool name or the call id. Arguments go through C `JS_JSONStringify` (a call whose arguments do not stringify throws a TypeError in the script), results through `JS_ParseJSON`. The returned value is a native Promise created in C; `qjs_ret` settles it. The object is defined with flags 0, `JS_PreventExtensions` is applied, and the global `unravis` is non-writable, non-configurable. The script is compiled sloppy inside a wrapper, so a write to a frozen property fails silently; tests assert the value did not change, not that a throw happened.

## Limits, each with its own code

| Limit | Mechanism | Code |
|---|---|---|
| ticks | The interrupt handler runs every N polls (N set in the handler by writing `ctx->interrupt_counter`; QuickJS polls at loop back-edges, calls and some built-in loops, and the regexp engine calls the same handler). Each handler call is one tick. Promise jobs also count one tick each. Reaching `ticks` calls `host_fatal`. | `ticks` |
| heap (oom) | A counting allocator under `JS_NewRuntime2` refuses a request that would pass `heapBytes` and sets a sticky flag; the handler and the end of run both check it, so a script that `catch`es the OutOfMemory error still ends as `oom`. A wasm trap on `memory.grow` past `memPages`, with the flag set or on an allocation failure, is also `oom`. `heapBytes` is kept well below `memPages * 64 KiB`, so QuickJS fails before wasm does. | `oom` |
| stack | `JS_SetMaxStackSize(min(stackBytes, linked stack - margin))`; the shadow stack is fixed at link time (`-Wl,-z,stack-size`), so the request is clamped to it. QuickJS throws InternalError "stack overflow", which the glue maps to `stack` (uncatchable: it goes through `host_fatal`). If wazero's own call-depth ceiling traps first, that trap is mapped to `stack` too, and the effective depth is recorded under "Measured". | `stack` |
| output | The result is serialised with `JSON.stringify` semantics in C; more than `callBytes` (1 MiB) is `output_too_large`. `undefined` is `null`. | `output_too_large` |
| logs | `console.log` joins its arguments, caps a line at 8 KiB, caps the total at `logBytes` (the broker counts len+1 per line) and 2000 lines; the rest is dropped and `logsTruncated` is set. A flood costs the script its own ticks, not memory. | not an error |
| script | A thrown error or a rejected run promise, a syntax error, a deadlock. The message and stack are cut to 8 KiB; the broker scrubs them. | `script_error` |

Precedence when more than one applies at the end: `oom` > `stack` > `ticks` > `output_too_large` > `script_error`.

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
