/*
 * glue.c: the C side of the code-mode sandbox worker (BL-362).
 *
 * One translation unit that includes the unmodified QuickJS-ng quickjs.c (v0.18.0), so it can reach the context
 * internals it needs without a patch to upstream. It is built as a WASI reactor: no main, no event loop, no libc
 * module (quickjs-libc.c is NOT linked, so there is no std, os, print, setTimeout, file or process access).
 * Go (cmd/unravis-sandbox) drives it through the exports below and answers the imports.
 *
 * What this file guarantees, in the engine and not by hiding names:
 *   - eval, new Function and the Async/Generator/AsyncGenerator constructors cannot compile anything: the script is
 *     compiled once, then ctx->eval_internal (set only by JS_AddIntrinsicEval) is cleared, so the engine itself
 *     answers every later request with "eval is not supported".
 *   - no module loader is ever installed, so import() fails.
 *   - the intrinsics are an explicit list. There is no WeakRef, no DOMException, no atob/btoa, no performance
 *     object but ours.
 *   - the memory limit is a counting allocator that ends the run (uncatchably, through host_fatal) when it would
 *     pass the limit. The tick limit does the same from the interrupt handler.
 *   - unravis.* are C functions: the tool index is in the function, arguments are serialised by C
 *     (JS_JSONStringify) and results are parsed by C (JS_ParseJSON), so a script cannot change a tool name, an id
 *     or the JSON text by changing JSON, Object.prototype or Promise.prototype.
 */
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include "quickjs.c"

#define IMPORT(n) __attribute__((import_module("env"), import_name(n)))
#define EXPORT(n) __attribute__((export_name(n)))

/* Imports (answered by Go). */
IMPORT("host_call") int32_t host_call(int32_t tool, int32_t id, const char *args, int32_t len);
IMPORT("host_log") int32_t host_log(const char *line, int32_t len);
IMPORT("host_fatal") void host_fatal(int32_t code, int64_t ticks, int64_t heap_peak);
IMPORT("host_random") double host_random(void);
IMPORT("host_perf_now") double host_perf_now(void);

/* host_fatal codes. */
#define FATAL_TICKS 1
#define FATAL_OOM 2

/* host_call answers. */
#define CALL_OK 0
#define CALL_BUDGET 1
#define CALL_QUEUE_FULL 2

/* The shadow stack is fixed at link time (-Wl,-z,stack-size=LINKED_STACK_BYTES, see BUILD-WASM.md). */
#ifndef LINKED_STACK_BYTES
#define LINKED_STACK_BYTES 1048576
#endif
#define STACK_MARGIN 65536

#define MAX_LINE 8192
#define MAX_TEXT 8192
#define MAX_TOOLS 64
#define MAX_TOOL_NAME 64
#define MAX_IDS 4096

static JSRuntime *rt;
static JSContext *ctx;

static uint64_t ticks, ticks_limit;
static uint32_t tick_polls = 100;

static size_t heap_used, heap_limit, heap_peak;

static uint32_t call_bytes_limit = 1u << 20;
static uint32_t max_ids = 51;

static JSValue run_promise;
static int run_started;

/* ---------------------------------------------------------------- allocator */

typedef struct {
    size_t size;
    uint32_t pad[3];
} Hdr; /* 16 bytes, keeps the alignment of malloc */

static void fatal(int32_t code)
{
    host_fatal(code, (int64_t)ticks, (int64_t)heap_peak);
    __builtin_trap(); /* host_fatal never returns; this is the backstop */
}

static void *g_malloc(void *opaque, size_t size)
{
    Hdr *h;
    (void)opaque;
    if (size > heap_limit || heap_used > heap_limit - size)
        fatal(FATAL_OOM);
    h = malloc(size + sizeof(Hdr));
    if (!h)
        fatal(FATAL_OOM);
    h->size = size;
    heap_used += size;
    if (heap_used > heap_peak)
        heap_peak = heap_used;
    return h + 1;
}

static void g_free(void *opaque, void *ptr)
{
    Hdr *h;
    (void)opaque;
    if (!ptr)
        return;
    h = (Hdr *)ptr - 1;
    heap_used -= h->size;
    free(h);
}

static void *g_calloc(void *opaque, size_t count, size_t size)
{
    size_t n;
    void *p;
    if (__builtin_mul_overflow(count, size, &n))
        fatal(FATAL_OOM);
    p = g_malloc(opaque, n);
    memset(p, 0, n);
    return p;
}

static void *g_realloc(void *opaque, void *ptr, size_t size)
{
    Hdr *h, *nh;
    size_t old;
    if (!ptr)
        return g_malloc(opaque, size);
    if (size == 0) {
        g_free(opaque, ptr);
        return NULL;
    }
    h = (Hdr *)ptr - 1;
    old = h->size;
    if (size > old && (size > heap_limit || heap_used - old > heap_limit - size))
        fatal(FATAL_OOM);
    nh = realloc(h, size + sizeof(Hdr));
    if (!nh)
        fatal(FATAL_OOM);
    heap_used = heap_used - old + size;
    if (heap_used > heap_peak)
        heap_peak = heap_used;
    nh->size = size;
    return nh + 1;
}

static size_t g_usable(const void *ptr)
{
    return ((const Hdr *)ptr - 1)->size;
}

/* -------------------------------------------------------------------- ticks */

static int on_interrupt(JSContext *c, void *opaque)
{
    (void)c;
    (void)opaque;
    ticks++;
    ctx->interrupt_counter = (int)tick_polls;
    if (ticks > ticks_limit)
        fatal(FATAL_TICKS);
    return 0;
}

/* ------------------------------------------------------------------ helpers */

static void drop_exception(void)
{
    JS_FreeValue(ctx, JS_GetException(ctx));
}

/* The text of a value for a log line or an error message, never throwing: a hostile toString or getter is a fallback text. */
static char *safe_text(JSValueConst v, size_t *plen, size_t cap)
{
    const char *s;
    size_t n = 0;
    char *out;
    s = JS_ToCStringLen(ctx, &n, v);
    if (!s) {
        drop_exception();
        s = "[unprintable]";
        n = strlen(s);
        if (n > cap)
            n = cap;
        out = malloc(n + 1);
        if (!out)
            return NULL;
        memcpy(out, s, n);
        out[n] = 0;
        *plen = n;
        return out;
    }
    if (n > cap) {
        n = cap;
        while (n > 0 && ((unsigned char)s[n] & 0xC0) == 0x80)
            n--; /* do not cut a UTF-8 sequence */
    }
    out = malloc(n + 1);
    if (out) {
        memcpy(out, s, n);
        out[n] = 0;
        *plen = n;
    }
    JS_FreeCString(ctx, s);
    return out;
}

static JSValue make_error(const char *code, const char *msg)
{
    JSValue e = JS_NewError(ctx);
    JS_DefinePropertyValueStr(ctx, e, "message", JS_NewString(ctx, msg), JS_PROP_C_W_E & ~JS_PROP_ENUMERABLE);
    if (code)
        JS_DefinePropertyValueStr(ctx, e, "code", JS_NewString(ctx, code), JS_PROP_C_W_E);
    return e;
}

/* ------------------------------------------------------------------ console */

static JSValue js_console_log(JSContext *c, JSValueConst this_val, int argc, JSValueConst *argv)
{
    static int closed;
    char line[MAX_LINE];
    size_t used = 0;
    int i;
    (void)this_val;
    if (closed)
        return JS_UNDEFINED;
    for (i = 0; i < argc && used < MAX_LINE; i++) {
        JSValue s = JS_UNDEFINED;
        const char *p;
        size_t n = 0;
        if (i > 0)
            line[used++] = ' ';
        if (JS_IsObject(argv[i]) && !JS_IsFunction(c, argv[i]) && !JS_IsError(argv[i])) {
            s = JS_JSONStringify(c, argv[i], JS_UNDEFINED, JS_UNDEFINED);
            if (JS_IsException(s)) {
                drop_exception();
                s = JS_UNDEFINED;
            }
        }
        p = JS_ToCStringLen(c, &n, JS_IsString(s) ? s : argv[i]);
        if (!p) {
            drop_exception();
            p = NULL;
            n = 0;
        }
        if (p) {
            if (n > MAX_LINE - used)
                n = MAX_LINE - used;
            memcpy(line + used, p, n);
            used += n;
            JS_FreeCString(c, p);
        } else {
            static const char u[] = "[unprintable]";
            n = sizeof(u) - 1;
            if (n > MAX_LINE - used)
                n = MAX_LINE - used;
            memcpy(line + used, u, n);
            used += n;
        }
        JS_FreeValue(c, s);
    }
    /* a line cut at MAX_LINE may end inside a UTF-8 sequence; the host repairs it and notes that the line may be cut */
    if (host_log(line, (int32_t)used) != 0)
        closed = 1;
    return JS_UNDEFINED;
}

/* ------------------------------------------------------------- Math, clock */

static JSValue js_random(JSContext *c, JSValueConst this_val, int argc, JSValueConst *argv)
{
    (void)this_val;
    (void)argc;
    (void)argv;
    return JS_NewFloat64(c, host_random());
}

static JSValue js_perf_now_fn(JSContext *c, JSValueConst this_val, int argc, JSValueConst *argv)
{
    (void)this_val;
    (void)argc;
    (void)argv;
    return JS_NewFloat64(c, host_perf_now());
}

/* ------------------------------------------------------------ host calls */

typedef struct {
    JSValue resolve, reject;
    int active;
} Pending;

static Pending *pending;
static uint32_t next_id = 1;


static JSValue js_tool(JSContext *c, JSValueConst this_val, int argc, JSValueConst *argv, int magic, JSValueConst *func_data)
{
    JSValue text, promise, funcs[2];
    const char *args;
    size_t n = 0;
    uint32_t id;
    int32_t rc;
    (void)this_val;
    (void)func_data;
    if (argc < 1 || JS_IsUndefined(argv[0])) {
        text = JS_NewString(c, "{}");
    } else {
        text = JS_JSONStringify(c, argv[0], JS_UNDEFINED, JS_UNDEFINED);
        if (JS_IsException(text))
            return JS_EXCEPTION;
        if (!JS_IsString(text)) {
            JS_FreeValue(c, text);
            return JS_ThrowTypeError(c, "the arguments are not JSON");
        }
    }
    args = JS_ToCStringLen(c, &n, text);
    JS_FreeValue(c, text);
    if (!args)
        return JS_EXCEPTION;
    if (n == 0 || args[0] != '{') {
        JS_FreeCString(c, args);
        return JS_ThrowTypeError(c, "the arguments must be an object");
    }
    if (n > call_bytes_limit) {
        JS_FreeCString(c, args);
        return JS_ThrowRangeError(c, "the arguments are too large");
    }
    promise = JS_NewPromiseCapability(c, funcs);
    if (JS_IsException(promise)) {
        JS_FreeCString(c, args);
        return promise;
    }
    if (next_id > max_ids) {
        /* beyond the table: the worker's own copy of the call budget (the broker counts too) */
        rc = CALL_BUDGET;
        id = 0;
    } else {
        id = next_id++;
        pending[id].resolve = funcs[0];
        pending[id].reject = funcs[1];
        pending[id].active = 1;
        rc = host_call(magic, (int32_t)id, args, (int32_t)n);
    }
    JS_FreeCString(c, args);
    if (rc != CALL_OK) {
        JSValue e = make_error(rc == CALL_BUDGET ? "call_budget" : "call_queue_full",
                               rc == CALL_BUDGET ? "call budget used up" : "too many calls waiting");
        JSValue r = JS_Call(c, funcs[1], JS_UNDEFINED, 1, (JSValueConst *)&e);
        JS_FreeValue(c, r);
        JS_FreeValue(c, e);
        JS_FreeValue(c, funcs[0]);
        JS_FreeValue(c, funcs[1]);
        if (id)
            pending[id].active = 0;
    }
    return promise;
}

/* ----------------------------------------------------------------- reports */

typedef struct {
    uint64_t ticks;
    uint64_t heap_peak;
    uint32_t kind;
    uint32_t res_ptr, res_len;
    uint32_t msg_ptr, msg_len;
    uint32_t stk_ptr, stk_len;
    uint32_t pad;
} Report;

#define KIND_OK 0
#define KIND_ERROR 1
#define KIND_TOO_LARGE 2
#define KIND_STACK 3

static Report report;

static void set_text(uint32_t *pptr, uint32_t *plen, char *s, size_t n)
{
    *pptr = (uint32_t)(uintptr_t)s;
    *plen = (uint32_t)n;
}

/* Fills report.msg and report.stk from a thrown or rejected value. */
static void describe_error(JSValueConst ex)
{
    size_t n;
    char *s;
    int is_stack = 0;
    if (JS_IsError(ex)) {
        JSValue name = JS_GetPropertyStr(ctx, ex, "name");
        JSValue msg = JS_GetPropertyStr(ctx, ex, "message");
        JSValue stk = JS_GetPropertyStr(ctx, ex, "stack");
        const char *nm = NULL, *ms = NULL;
        size_t nl = 0, ml = 0;
        char *out;
        if (JS_IsException(name)) { drop_exception(); name = JS_UNDEFINED; }
        if (JS_IsException(msg)) { drop_exception(); msg = JS_UNDEFINED; }
        if (JS_IsException(stk)) { drop_exception(); stk = JS_UNDEFINED; }
        if (JS_IsString(name))
            nm = JS_ToCStringLen(ctx, &nl, name);
        if (JS_IsString(msg))
            ms = JS_ToCStringLen(ctx, &ml, msg);
        /*
         * The engine's stack check throws a plain RangeError (JS_ThrowStackOverflow is static in quickjs.c and is not patched).
         * Uncaught, it is the stack limit. A script can write the same words itself, which only labels its own failure.
         */
        if (nm && ms && nl == 10 && ml == 32 && memcmp(nm, "RangeError", 10) == 0 &&
            memcmp(ms, "Maximum call stack size exceeded", 32) == 0)
            is_stack = 1;
        if (nl > 64) nl = 64;
        if (ml > MAX_TEXT) ml = MAX_TEXT;
        out = malloc(nl + ml + 3);
        if (out) {
            size_t k = 0;
            if (nm && nl) { memcpy(out, nm, nl); k = nl; }
            if (ms && ml) {
                if (k) { out[k++] = ':'; out[k++] = ' '; }
                memcpy(out + k, ms, ml);
                k += ml;
            }
            if (k == 0) { memcpy(out, "Error", 5); k = 5; }
            out[k] = 0;
            set_text(&report.msg_ptr, &report.msg_len, out, k);
        }
        if (nm) JS_FreeCString(ctx, nm);
        if (ms) JS_FreeCString(ctx, ms);
        if (JS_IsString(stk)) {
            s = safe_text(stk, &n, MAX_TEXT);
            if (s)
                set_text(&report.stk_ptr, &report.stk_len, s, n);
        }
        JS_FreeValue(ctx, name);
        JS_FreeValue(ctx, msg);
        JS_FreeValue(ctx, stk);
        if (is_stack)
            report.kind = KIND_STACK;
        return;
    }
    s = safe_text(ex, &n, MAX_TEXT);
    if (s)
        set_text(&report.msg_ptr, &report.msg_len, s, n);
}

static void finish_with_error(JSValue ex)
{
    report.kind = KIND_ERROR;
    describe_error(ex);
    JS_FreeValue(ctx, ex);
}

static void finish_with_value(JSValue v)
{
    JSValue js;
    const char *s;
    size_t n = 0;
    char *copy;
    if (JS_IsUndefined(v)) {
        JS_FreeValue(ctx, v);
        return; /* result stays empty: the broker reads it as null */
    }
    js = JS_JSONStringify(ctx, v, JS_UNDEFINED, JS_UNDEFINED);
    JS_FreeValue(ctx, v);
    if (JS_IsException(js)) {
        finish_with_error(JS_GetException(ctx));
        return;
    }
    if (JS_IsUndefined(js))
        return; /* a function or a symbol: no JSON, like JSON.stringify */
    s = JS_ToCStringLen(ctx, &n, js);
    JS_FreeValue(ctx, js);
    if (!s) {
        finish_with_error(JS_GetException(ctx));
        return;
    }
    if (n > call_bytes_limit) {
        report.kind = KIND_TOO_LARGE;
        JS_FreeCString(ctx, s);
        return;
    }
    copy = malloc(n + 1);
    if (!copy)
        fatal(FATAL_OOM);
    memcpy(copy, s, n);
    copy[n] = 0;
    JS_FreeCString(ctx, s);
    set_text(&report.res_ptr, &report.res_len, copy, n);
}

/* ------------------------------------------------------------------ exports */

/*
 * Prelude: runs once, before any user code, while the Eval intrinsic is still present (it is switched off right after).
 * Function prototypes lose their constructor property, so that nothing reaches the AsyncFunction or GeneratorFunction
 * constructors by name. This is on top of the engine switch (eval_internal), not instead of it.
 */
static const char prelude[] =
    "(function(){\n"
    "'use strict';\n"
    "var gp=Object.getPrototypeOf,dp=Object.defineProperty;\n"
    "var ps=[gp(function(){}),gp(async function(){}),gp(function*(){}),gp(async function*(){})];\n"
    "for(var i=0;i<ps.length;i++)dp(ps[i],'constructor',{value:undefined,writable:false,configurable:false,enumerable:false});\n"
    "})()";

static int define_ro(JSValueConst obj, const char *name, JSValue v)
{
    return JS_DefinePropertyValueStr(ctx, obj, name, v, JS_PROP_ENUMERABLE);
}

EXPORT("qjs_alloc") void *qjs_alloc(uint32_t n)
{
    return malloc((size_t)n + 1);
}

EXPORT("qjs_free") void qjs_free(void *p)
{
    free(p);
}

EXPORT("qjs_report") Report *qjs_report(void)
{
    report.ticks = ticks;
    report.heap_peak = heap_peak;
    return &report;
}

EXPORT("qjs_init") int32_t qjs_init(uint32_t heap_bytes, uint32_t stack_bytes, uint32_t max_ticks, uint32_t polls_per_tick, uint32_t call_bytes, uint32_t max_calls)
{
    static const JSMallocFunctions mf = { g_calloc, g_malloc, g_free, g_realloc, g_usable };
    static const char *const drop[] = { "eval", "Function", "SharedArrayBuffer", "Atomics", NULL };
    JSValue g, math, console, perf, v;
    int i;

    if (rt)
        return -1;
    heap_limit = heap_bytes;
    ticks_limit = max_ticks;
    if (polls_per_tick > 0)
        tick_polls = polls_per_tick;
    if (call_bytes > 0)
        call_bytes_limit = call_bytes;
    max_ids = max_calls < MAX_IDS ? max_calls : MAX_IDS;
    if (stack_bytes > LINKED_STACK_BYTES - STACK_MARGIN)
        stack_bytes = LINKED_STACK_BYTES - STACK_MARGIN;

    run_promise = JS_UNDEFINED;
    rt = JS_NewRuntime2(&mf, NULL);
    if (!rt)
        return -2;
    JS_SetMaxStackSize(rt, stack_bytes);
    JS_SetCanBlock(rt, false);
    JS_SetInterruptHandler(rt, on_interrupt, NULL);

    ctx = JS_NewContextRaw(rt);
    if (!ctx)
        return -3;
    ctx->interrupt_counter = (int)tick_polls;
    /* The whole list. Not added on purpose: WeakRef, DOMException, atob/btoa, and everything of quickjs-libc. */
    if (JS_AddIntrinsicBaseObjects(ctx) || JS_AddIntrinsicDate(ctx))
        return -4;
    JS_AddIntrinsicEval(ctx); /* switched off again in qjs_run, after the script is compiled */
    JS_AddIntrinsicRegExpCompiler(ctx);
    if (JS_AddIntrinsicRegExp(ctx) || JS_AddIntrinsicJSON(ctx) || JS_AddIntrinsicProxy(ctx) || JS_AddIntrinsicMapSet(ctx) ||
        JS_AddIntrinsicTypedArrays(ctx) || JS_AddIntrinsicPromise(ctx) || JS_AddIntrinsicBigInt(ctx))
        return -5;

    pending = calloc((size_t)max_ids + 1, sizeof(Pending));
    if (!pending)
        return -6;

    v = JS_Eval(ctx, prelude, sizeof(prelude) - 1, "<prelude>", JS_EVAL_TYPE_GLOBAL);
    if (JS_IsException(v))
        return -7;
    JS_FreeValue(ctx, v);

    g = JS_GetGlobalObject(ctx);
    for (i = 0; drop[i]; i++) {
        JSAtom a = JS_NewAtom(ctx, drop[i]);
        JS_DeleteProperty(ctx, g, a, 0);
        JS_FreeAtom(ctx, a);
    }

    math = JS_GetPropertyStr(ctx, g, "Math");
    JS_DefinePropertyValueStr(ctx, math, "random", JS_NewCFunction(ctx, js_random, "random", 0), JS_PROP_C_W_E & ~JS_PROP_ENUMERABLE);
    JS_FreeValue(ctx, math);

    console = JS_NewObject(ctx);
    define_ro(console, "log", JS_NewCFunction(ctx, js_console_log, "log", 0));
    define_ro(console, "info", JS_NewCFunction(ctx, js_console_log, "info", 0));
    define_ro(console, "warn", JS_NewCFunction(ctx, js_console_log, "warn", 0));
    define_ro(console, "error", JS_NewCFunction(ctx, js_console_log, "error", 0));
    define_ro(console, "debug", JS_NewCFunction(ctx, js_console_log, "debug", 0));
    JS_PreventExtensions(ctx, console);
    JS_DefinePropertyValueStr(ctx, g, "console", console, 0);

    perf = JS_NewObject(ctx);
    define_ro(perf, "now", JS_NewCFunction(ctx, js_perf_now_fn, "now", 0));
    JS_PreventExtensions(ctx, perf);
    JS_DefinePropertyValueStr(ctx, g, "performance", perf, 0);

    JS_FreeValue(ctx, g);
    return 0;
}

/* Builds the frozen unravis object from the tool names (separated by \n), compiles the script and starts it. */
EXPORT("qjs_run") int32_t qjs_run(const char *js, uint32_t js_len, const char *names, uint32_t names_len)
{
    JSValue g, unravis, fn, v;
    uint32_t i = 0, idx = 0;

    if (!ctx)
        return -1;
    JS_UpdateStackTop(rt);
    g = JS_GetGlobalObject(ctx);
    unravis = JS_NewObject(ctx);
    while (i < names_len && idx < MAX_TOOLS) {
        char name[MAX_TOOL_NAME + 1];
        uint32_t j = i, n;
        JSValue f;
        while (j < names_len && names[j] != '\n')
            j++;
        n = j - i;
        if (n == 0 || n > MAX_TOOL_NAME)
            return -2;
        memcpy(name, names + i, n);
        name[n] = 0;
        f = JS_NewCFunctionData2(ctx, js_tool, name, 1, (int)idx, 0, NULL);
        JS_PreventExtensions(ctx, f);
        define_ro(unravis, name, f);
        idx++;
        i = j + 1;
    }

    JS_PreventExtensions(ctx, unravis);
    JS_DefinePropertyValueStr(ctx, g, "unravis", unravis, 0);
    JS_FreeValue(ctx, g);

    /* Compile first (the Eval intrinsic is still there), then take the compiler away for good, then run. */
    fn = JS_Eval(ctx, js, js_len, "<script>", JS_EVAL_TYPE_GLOBAL | JS_EVAL_FLAG_COMPILE_ONLY);
    ctx->eval_internal = NULL;
    if (JS_IsException(fn)) {
        finish_with_error(JS_GetException(ctx));
        return 0;
    }
    v = JS_EvalFunction(ctx, fn);
    if (JS_IsException(v)) {
        finish_with_error(JS_GetException(ctx));
        return 0;
    }
    run_promise = v;
    run_started = 1;
    if (JS_IsPromise(v))
        JS_PromiseMarkAsHandled(ctx, v);
    return 0;
}

/*
 * Delivers the broker's answer for call id. text is JSON (zero terminated by the caller): the tool result when ok, or
 * {"code":..,"message":..} when not. Returns 0, or -1 for an id that is not waiting (the caller ends the run).
 */
EXPORT("qjs_ret") int32_t qjs_ret(uint32_t id, int32_t ok, const char *text, uint32_t len)
{
    JSValue v, r;
    if (id == 0 || id >= next_id || id > max_ids || !pending[id].active)
        return -1;
    JS_UpdateStackTop(rt);
    pending[id].active = 0;
    v = JS_ParseJSON(ctx, text, len, "<ret>");
    if (ok) {
        if (JS_IsException(v)) {
            drop_exception();
            v = make_error("bad_result", "the answer is not JSON this script can read");
            r = JS_Call(ctx, pending[id].reject, JS_UNDEFINED, 1, (JSValueConst *)&v);
        } else {
            r = JS_Call(ctx, pending[id].resolve, JS_UNDEFINED, 1, (JSValueConst *)&v);
        }
    } else {
        JSValue code = JS_UNDEFINED, msg = JS_UNDEFINED, e;
        const char *cs = NULL, *ms = NULL;
        if (!JS_IsException(v) && JS_IsObject(v)) {
            code = JS_GetPropertyStr(ctx, v, "code");
            msg = JS_GetPropertyStr(ctx, v, "message");
        } else if (JS_IsException(v)) {
            drop_exception();
        }
        if (JS_IsString(code)) cs = JS_ToCString(ctx, code);
        if (JS_IsString(msg)) ms = JS_ToCString(ctx, msg);
        e = make_error(cs ? cs : "error", ms ? ms : "the call failed");
        if (cs) JS_FreeCString(ctx, cs);
        if (ms) JS_FreeCString(ctx, ms);
        JS_FreeValue(ctx, code);
        JS_FreeValue(ctx, msg);
        JS_FreeValue(ctx, v);
        v = e;
        r = JS_Call(ctx, pending[id].reject, JS_UNDEFINED, 1, (JSValueConst *)&v);
    }
    JS_FreeValue(ctx, r);
    JS_FreeValue(ctx, v);
    JS_FreeValue(ctx, pending[id].resolve);
    JS_FreeValue(ctx, pending[id].reject);
    return 0;
}

/*
 * Runs every job that is ready. Returns 0 while the run promise is pending, 1 when it is settled (the report is
 * filled), and -1 when the run never started (qjs_run already filled the report with the failure).
 */
EXPORT("qjs_pump") int32_t qjs_pump(void)
{
    JSContext *c1;
    int r;
    JSPromiseStateEnum st;
    JS_UpdateStackTop(rt);
    if (!run_started)
        return -1;
    for (;;) {
        r = JS_ExecutePendingJob(rt, &c1);
        if (r == 0)
            break;
        if (r < 0)
            drop_exception();
        /* a job that runs no bytecode (a native resolve function) polls nothing: count it here */
        if (js_poll_interrupts(ctx))
            drop_exception();
    }
    if (!JS_IsPromise(run_promise)) {
        finish_with_value(run_promise);
        run_started = 0;
        run_promise = JS_UNDEFINED;
        return 1;
    }
    st = JS_PromiseState(ctx, run_promise);
    if (st == JS_PROMISE_PENDING)
        return 0;
    if (st == JS_PROMISE_FULFILLED) {
        finish_with_value(JS_PromiseResult(ctx, run_promise));
    } else {
        finish_with_error(JS_PromiseResult(ctx, run_promise));
    }
    JS_FreeValue(ctx, run_promise);
    run_started = 0;
    run_promise = JS_UNDEFINED;
    return 1;
}
