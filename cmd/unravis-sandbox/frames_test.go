package main

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"

	"api-audit-explorer/internal/codemode/wire"
)

// wantViolation asserts the worker ended the run as a protocol violation: no done frame, exit code 2, nothing on stderr.
func wantViolation(t *testing.T, w *worker) {
	t.Helper()
	for {
		data, err := w.next(30 * time.Second)
		if err != nil {
			break // EOF (the worker closed its pipe), or a read error after it died
		}
		if typ, _ := wire.PeekType(data); typ == wire.TypeDone {
			t.Fatalf("the worker sent a done frame for a protocol violation: %s", data)
		}
	}
	if code := w.waitExit(30 * time.Second); code != exitProtocol {
		t.Fatalf("exit code %d, want %d (stderr %q)", code, exitProtocol, w.stderr.String())
	}
	if w.stderr.Len() != 0 {
		t.Fatalf("stderr: %q", w.stderr.String())
	}
}

func head(n uint32) []byte {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], n)
	return b[:]
}

func rawFrame(payload []byte) []byte { return append(head(uint32(len(payload))), payload...) } //nolint:gosec // test sizes

// validRun is a run frame the worker accepts, with the given script.
func validRun(js string) wire.Run {
	return wire.Run{T: wire.TypeRun, JS: "(async()=>{" + js + `
})()`, Tools: allTools, Limits: testLimits(), Clock: wire.Clock{Mode: "live", BaseMs: 1700000000000}, Seed: "00112233445566778899aabbccddeeff"}
}

// A frame whose declared length is over MaxFrame (1 MiB + 4 KiB) is refused before it is read, in both directions of the run.
func TestFrameTooLargeRefused(t *testing.T) {
	t.Run("before the run", func(t *testing.T) {
		w := startWorker(t)
		w.hello()
		w.sendRaw(append(head(wire.MaxFrame+1), bytes.Repeat([]byte("x"), 1024)...))
		wantViolation(t, w)
	})
	t.Run("run frame at the maximum plus one byte, fully sent", func(t *testing.T) {
		w := startWorker(t)
		w.hello()
		body := append([]byte(`{"t":"run","js":"`), bytes.Repeat([]byte("a"), wire.MaxFrame)...)
		body = append(body, []byte(`"}`)...)
		w.sendRaw(rawFrame(body))
		wantViolation(t, w)
	})
	t.Run("during the run", func(t *testing.T) {
		w := startWorker(t)
		w.hello()
		w.sendFrame(validRun("await unravis.audit_compare({}); return 1"))
		if _, err := w.next(frameWait); err != nil { // the call
			t.Fatal(err)
		}
		w.sendRaw(append(head(wire.MaxFrame+1), bytes.Repeat([]byte("x"), 1024)...))
		wantViolation(t, w)
	})
	t.Run("a run that would make the worker answer with a frame over the maximum still ends cleanly", func(t *testing.T) {
		// The done frame is the worker's own frame. A result under callBytes plus a full log is still bounded by MaxFrame: the logs go first.
		l := testLimits()
		l.CallBytes = 1 << 20
		l.LogBytes = 1 << 20
		o := runScenario(t, scenario{limits: l, js: `
			for (let i = 0; i < 100; i++) console.log("y".repeat(8000));
			return "z".repeat(1000000);`})
		o.wantClean(t)
		if !o.done.OK && o.done.Error.Code != "output_too_large" {
			t.Fatalf("done %+v", o.done.Error)
		}
		if len(o.done.Logs) != 0 || !o.done.LogsTruncated {
			t.Fatalf("the logs must go first and say so: %d lines, truncated %v", len(o.done.Logs), o.done.LogsTruncated)
		}
		if len(o.doneRaw) > wire.MaxFrame {
			t.Fatalf("done frame of %d bytes", len(o.doneRaw))
		}
	})
}

// Anything that is not a well-formed frame of the right kind ends the run with no done frame.
func TestBadFrameEndsRun(t *testing.T) {
	good := validRun("return 1")
	badLimit := good
	badLimit.Limits.MemPages = 1
	badTool := good
	badTool.Tools = []string{"audit_compare", "audit_compare"}
	badTool2 := good
	badTool2.Tools = []string{"not a name"}
	badSeed := good
	badSeed.Seed = "zz"
	badClock := good
	badClock.Clock.Mode = "sideways"
	noJS := good
	noJS.JS = ""

	before := map[string][]byte{
		"empty frame":                 head(0),
		"not json":                    rawFrame([]byte("{not json")),
		"json array":                  rawFrame([]byte("[1]")),
		"json null":                   rawFrame([]byte("null")),
		"unknown t":                   rawFrame([]byte(`{"t":"bogus"}`)),
		"no t":                        rawFrame([]byte(`{"js":"1"}`)),
		"done as the first frame":     rawFrame([]byte(`{"t":"done","ok":true}`)),
		"ret as the first frame":      rawFrame([]byte(`{"t":"ret","id":1,"ok":true}`)),
		"hello as the first frame":    rawFrame([]byte(`{"t":"hello","v":1}`)),
		"invalid utf-8":               rawFrame([]byte("{\"t\":\"run\",\"js\":\"\xff\xfe\"}")),
		"duplicate top level name":    rawFrame([]byte(`{"t":"run","t":"run"}`)),
		"trailing data":               rawFrame([]byte(`{"t":"run"} {"t":"run"}`)),
		"memPages out of range":       nil,
		"duplicate tool":              nil,
		"tool name with a space":      nil,
		"bad seed":                    nil,
		"bad clock mode":              nil,
		"empty script":                nil,
		"header only then stdin ends": head(50),
	}
	runs := map[string]wire.Run{"memPages out of range": badLimit, "duplicate tool": badTool, "tool name with a space": badTool2, "bad seed": badSeed, "bad clock mode": badClock, "empty script": noJS}
	for name, raw := range before {
		t.Run("before the run/"+name, func(t *testing.T) {
			w := startWorker(t)
			w.hello()
			if r, ok := runs[name]; ok {
				w.sendFrame(r)
			} else {
				w.sendRaw(raw)
			}
			if name == "header only then stdin ends" {
				_ = w.stdin.Close()
			}
			wantViolation(t, w)
		})
	}
	t.Run("before the run/stdin closed without a frame", func(t *testing.T) {
		w := startWorker(t)
		w.hello()
		_ = w.stdin.Close()
		wantViolation(t, w)
	})

	during := map[string]string{
		"not json":                 `{not json`,
		"unknown t":                `{"t":"bogus"}`,
		"a second run frame":       `{"t":"run","js":"1"}`,
		"a call frame":             `{"t":"call","id":1,"tool":"audit_compare","args":{}}`,
		"hello":                    `{"t":"hello","v":1}`,
		"ret for an unknown id":    `{"t":"ret","id":99,"ok":true,"result":1}`,
		"ret with id 0":            `{"t":"ret","id":0,"ok":true,"result":1}`,
		"ret with a negative id":   `{"t":"ret","id":-1,"ok":true,"result":1}`,
		"ret with result and err":  `{"t":"ret","id":1,"ok":true,"result":1,"error":{"code":"x","message":"y"}}`,
		"ret with neither":         `{"t":"ret","id":1,"ok":false}`,
		"ret ok false with result": `{"t":"ret","id":1,"ok":false,"result":1}`,
		"ret with an id string":    `{"t":"ret","id":"1","ok":true,"result":1}`,
	}
	for name, payload := range during {
		t.Run("during the run/"+name, func(t *testing.T) {
			w := startWorker(t)
			w.hello()
			w.sendFrame(validRun("await unravis.audit_compare({}); return 1"))
			data, err := w.next(frameWait)
			if err != nil {
				t.Fatal(err)
			}
			var c wire.Call
			if err := wire.Decode(data, &c); err != nil || c.ID != 1 {
				t.Fatalf("call frame %s", data)
			}
			w.sendRaw(rawFrame([]byte(payload)))
			wantViolation(t, w)
		})
	}
	t.Run("during the run/a second ret for the same call", func(t *testing.T) {
		w := startWorker(t)
		w.hello()
		w.sendFrame(validRun("await unravis.audit_compare({}); await unravis.audit_compare({}); return 1"))
		if _, err := w.next(frameWait); err != nil {
			t.Fatal(err)
		}
		w.sendFrame(wire.Ret{T: wire.TypeRet, ID: 1, OK: true, Result: []byte(`1`)})
		if _, err := w.next(frameWait); err != nil { // the second call, id 2
			t.Fatal(err)
		}
		w.sendFrame(wire.Ret{T: wire.TypeRet, ID: 1, OK: true, Result: []byte(`1`)})
		wantViolation(t, w)
	})
	t.Run("during the run/stdin ends while a call is out", func(t *testing.T) {
		w := startWorker(t)
		w.hello()
		w.sendFrame(validRun("await unravis.audit_compare({}); return 1"))
		if _, err := w.next(frameWait); err != nil {
			t.Fatal(err)
		}
		_ = w.stdin.Close()
		wantViolation(t, w)
	})
}

// The worker runs one script, sends one done frame and exits 0. Nothing after the first run frame is read.
func TestOneRunThenExit(t *testing.T) {
	w := startWorker(t)
	w.hello()
	w.sendFrame(validRun("return 7"))
	// A second run frame right behind the first must not start a second run.
	w.sendFrame(validRun("return 8"))
	var dones []wire.Done
	for {
		data, err := w.next(frameWait)
		if err != nil {
			break
		}
		typ, _ := wire.PeekType(data)
		if typ != wire.TypeDone {
			t.Fatalf("unexpected frame %q", typ)
		}
		var d wire.Done
		if err := wire.Decode(data, &d); err != nil {
			t.Fatal(err)
		}
		dones = append(dones, d)
	}
	if len(dones) != 1 || !dones[0].OK || string(dones[0].Result) != "7" {
		t.Fatalf("done frames %+v", dones)
	}
	if code := w.waitExit(30 * time.Second); code != 0 {
		t.Fatalf("exit code %d (stderr %q)", code, w.stderr.String())
	}
	if w.stderr.Len() != 0 {
		t.Fatalf("stderr %q", w.stderr.String())
	}

	// A script that fails also ends with exactly one done frame and exit 0.
	o := runScenario(t, scenario{js: "throw new Error('boom')"})
	o.wantFail(t, "script_error")
	if o.done.Error.Message != "Error: boom" {
		t.Fatalf("message %q", o.done.Error.Message)
	}
}

func TestHelloIsFirstAndHonest(t *testing.T) {
	w := startWorker(t)
	h := w.hello() // read before the run frame exists: no user code can have run
	if h.V != wire.Version || h.Runtime != runtimeName {
		t.Fatalf("hello %+v", h)
	}
	for _, m := range []string{"acg", "win32kOff", "childRestricted"} {
		on, ok := h.Mitigations[m]
		if !ok || on {
			t.Fatalf("the worker must not claim mitigation %q it cannot see: %+v", m, h.Mitigations)
		}
	}
	if len(h.WasmSHA256) != 64 {
		t.Fatalf("wasmSha256 %q", h.WasmSHA256)
	}
}
