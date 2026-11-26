# Rebuilding `cmd/unravis-sandbox/qjs.wasm`

The sandbox worker embeds one WebAssembly module: QuickJS-ng plus our own C glue
(`cmd/unravis-sandbox/glue/glue.c`), built as a WASI reactor with wasi-sdk. The built module is
committed (`qjs.wasm`, with `qjs.wasm.sha256`). No toolchain and no QuickJS source is committed.
Rebuild only to change the glue or to move a pin; a changed hash is a code change and needs review.

## Pins

| Item | Pin |
|---|---|
| wasi-sdk | 34.0, `wasi-sdk-34.0-x86_64-windows.tar.gz`, SHA-256 `cccb5c323a9b34f0349a9b09e8804a0a7632c68c3310f4b5f437ed57d7e71d8f` (clang 23.1.0-rc3) |
| QuickJS-ng | v0.18.0, commit `d7215b0ad3fc2c562e8317fd9bca058fd4863804`, `qjs-v0.18.0.tar.gz` SHA-256 `28bcd89aa3683fd2a946537e5ce3f11cc46b5cbf3383c7561aaef3df5ff9f1dd` |
| wazero | v1.12.0 (`go.mod`), interpreter only |

Download both archives from the official releases of WebAssembly/wasi-sdk and quickjs-ng/quickjs, check the
SHA-256 values above before unpacking, and unpack them OUTSIDE this repository (untrusted data: do not
run anything from inside the unpacked folders).

## Build

Set `SDK` to the unpacked wasi-sdk folder and `SRC` to the unpacked QuickJS-ng folder (the one that holds
`quickjs.c`). From the repository root:

```sh
$SDK/bin/clang --target=wasm32-wasip1 --sysroot=$SDK/share/wasi-sysroot \
  -O2 -DNDEBUG -D_GNU_SOURCE -D_WASI_EMULATED_PROCESS_CLOCKS -D_WASI_EMULATED_SIGNAL \
  -DLINKED_STACK_BYTES=1048576 -Wall -Wno-unused-function -Wno-unused-but-set-variable \
  -ffile-prefix-map=$SRC=. -I$SRC \
  -mexec-model=reactor -Wl,--stack-first -Wl,-z,stack-size=1048576 -Wl,--strip-all -Wl,--no-entry \
  cmd/unravis-sandbox/glue/glue.c $SRC/dtoa.c $SRC/libregexp.c $SRC/libunicode.c \
  -lwasi-emulated-process-clocks -lwasi-emulated-signal -o cmd/unravis-sandbox/qjs.wasm
```

`glue.c` does `#include "quickjs.c"`, so `quickjs.c` is not on the command line. `quickjs-libc.c` is NOT
built: it is where `std`, `os`, `print`, timers and file access come from. `LINKED_STACK_BYTES` must equal
`-z stack-size` (the glue clamps the script's stack limit to it).

Then write the hash and run the tests:

```sh
sha256sum cmd/unravis-sandbox/qjs.wasm | cut -d' ' -f1 > cmd/unravis-sandbox/qjs.wasm.sha256
go test -count=1 ./cmd/unravis-sandbox/...
```

(Keep `qjs.wasm.sha256` as the bare hash, the same format as the committed file.)

## Reproducibility check

Build twice into two different output paths and compare: the hashes must be identical. This was checked
three times on 2026-11-25 (same machine, same pins) and gave the same hash,
`55c00c952cd30c8a3afe21540a2135f7ea5746fb4dcaad7981677e23c6f01cf8`, 1,021,359 bytes. It was not checked on
a second machine or a second host OS; the build embeds no timestamp and `-ffile-prefix-map` removes the
source path. If a different host gives a different hash, say so in the PR; do not just commit the new file.

## Imports the module may have

`env.host_call`, `env.host_fatal`, `env.host_log`, `env.host_perf_now`, `env.host_random`, and from
`wasi_snapshot_preview1` only `clock_time_get`, `fd_close`, `fd_fdstat_get`, `fd_seek`, `fd_write`. The worker
provides its own minimal WASI module (the `fd_*` functions return EBADF; there is no file system, no
environment, no arguments). A test lists the imports and fails on any other name. If a rebuild adds one, the
glue or the SDK changed and that needs review before the allow-list is touched.
