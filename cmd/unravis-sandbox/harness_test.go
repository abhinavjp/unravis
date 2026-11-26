package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"api-audit-explorer/internal/codemode/wire"
)

// workerBin is the real worker, built once by TestMain. The tests run it as a plain child process and speak the real wire protocol to it;
// nothing about the engine is faked.
var workerBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "unravis-sandbox-test")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	workerBin = filepath.Join(dir, "unravis-sandbox.exe")
	if prebuilt := os.Getenv("UNRAVIS_SANDBOX_TEST_BIN"); prebuilt != "" {
		// Run the suite against an exe built with the release flags (for example -H=windowsgui) instead of a plain go build.
		workerBin = prebuilt
	} else {
		build := exec.Command("go", "build", "-o", workerBin, ".") //nolint:gosec // fixed arguments, the package under test
		if out, err := build.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "build worker: %v\n%s", err, out)
			_ = os.RemoveAll(dir)
			os.Exit(1)
		}
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

const frameWait = 90 * time.Second

type incoming struct {
	data []byte
	err  error
}

// worker is one running worker process seen from the broker's side.
type worker struct {
	t      *testing.T
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	frames chan incoming
	stderr *bytes.Buffer
	exited chan struct{}
	exit   error
}

func startWorker(t *testing.T) *worker {
	t.Helper()
	cmd := exec.Command(workerBin)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	// The worker's stdout is a pipe this test owns, not cmd.StdoutPipe: Wait closes the read end of a StdoutPipe as soon as the process
	// has exited, which can race with the reader goroutine below and turn the worker's last frames (the done frame the worker writes just
	// before it exits) into "read |0: file already closed", or drop them unread. With our own pipe the reader always reaches the real EOF.
	stdout, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = stdoutW
	w := &worker{t: t, cmd: cmd, stdin: stdin, frames: make(chan incoming, 4096), stderr: &bytes.Buffer{}, exited: make(chan struct{})}
	cmd.Stderr = w.stderr
	if err := cmd.Start(); err != nil {
		_ = stdout.Close()
		_ = stdoutW.Close()
		t.Fatal(err)
	}
	_ = stdoutW.Close() // the child holds its own copy; the parent's copy must go so the reader sees EOF when the child exits
	t.Cleanup(w.kill)
	go func() {
		defer close(w.frames)
		defer stdout.Close() //nolint:errcheck // read end of our own pipe, nothing to flush
		for {
			data, err := wire.ReadFrame(stdout, wire.MaxFrame)
			if err != nil {
				if err != io.EOF {
					w.frames <- incoming{err: err}
				}
				return
			}
			w.frames <- incoming{data: data}
		}
	}()
	go func() {
		w.exit = cmd.Wait()
		close(w.exited)
	}()
	return w
}

func (w *worker) kill() {
	if w.cmd.Process != nil {
		_ = w.cmd.Process.Kill()
	}
	_ = w.stdin.Close()
	select {
	case <-w.exited:
	case <-time.After(10 * time.Second):
	}
}

// next returns the next frame the worker sent, or an error when it closed its pipe or took longer than wait.
func (w *worker) next(wait time.Duration) ([]byte, error) {
	select {
	case in, ok := <-w.frames:
		if !ok {
			return nil, io.EOF
		}
		return in.data, in.err
	case <-time.After(wait):
		return nil, fmt.Errorf("no frame within %v", wait)
	}
}

// sendRaw writes bytes as they are (a hand-made length and payload, for the bad-frame tests).
func (w *worker) sendRaw(b []byte) {
	w.t.Helper()
	_, _ = w.stdin.Write(b)
}

func (w *worker) sendFrame(v any) {
	w.t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		w.t.Fatal(err)
	}
	var head [4]byte
	binary.LittleEndian.PutUint32(head[:], uint32(len(b))) //nolint:gosec // test frames are small
	w.sendRaw(append(head[:], b...))
}

// waitExit returns the exit code of the worker (-1 when it did not exit in time).
func (w *worker) waitExit(wait time.Duration) int {
	select {
	case <-w.exited:
		if w.cmd.ProcessState == nil {
			return -1
		}
		return w.cmd.ProcessState.ExitCode()
	case <-time.After(wait):
		return -1
	}
}

// hello reads and checks the first frame.
func (w *worker) hello() wire.Hello {
	w.t.Helper()
	data, err := w.next(frameWait)
	if err != nil {
		w.t.Fatalf("no hello: %v (stderr %q)", err, w.stderr.String())
	}
	var h wire.Hello
	if err := wire.Decode(data, &h); err != nil || h.T != wire.TypeHello {
		w.t.Fatalf("first frame is not a hello: %s", data)
	}
	return h
}

// testLimits are small so the tests are quick; each test changes what it needs.
func testLimits() wire.Limits {
	return wire.Limits{MemPages: 256, HeapBytes: 8 << 20, StackBytes: 256 << 10, Ticks: 200000, CallBytes: 1 << 20, LogBytes: 64 << 10, MaxCalls: 50, InFlight: 4}
}

// answer is what the fake host replies to one call.
type answer func(args json.RawMessage) (any, *wire.RetError)

// scenario describes one run.
type scenario struct {
	js     string
	tools  []string
	limits wire.Limits
	clock  wire.Clock
	seed   string
	// answers is the table of the fake host, by tool name. A tool without an entry answers {"echo": args}.
	answers map[string]answer
	// hold makes the fake host wait until that many calls are out (or 300 ms pass) before it answers any, to build up calls in flight.
	hold int
}

// outcome is what the broker side saw.
type outcome struct {
	done        wire.Done
	doneRaw     []byte
	calls       []wire.Call
	maxInFlight int
	exit        int
	stderr      string
	hello       wire.Hello
}

var allTools = []string{"audit_list_sources", "audit_get_guide", "audit_get_requests", "audit_get_groups", "audit_get_activity", "audit_get_breakdown", "audit_get_slowest", "audit_get_timeline", "audit_compare", "audit_compare_timelines"}

// runScenario runs one script through a real worker and plays the broker. The script is wrapped like the broker does.
func runScenario(t *testing.T, sc scenario) outcome {
	t.Helper()
	w := startWorker(t)
	var out outcome
	out.hello = w.hello()
	if sc.tools == nil {
		sc.tools = allTools
	}
	if sc.limits == (wire.Limits{}) {
		sc.limits = testLimits()
	}
	if sc.clock.Mode == "" {
		sc.clock = wire.Clock{Mode: "live", BaseMs: 1700000000000}
	}
	if sc.seed == "" {
		sc.seed = "00112233445566778899aabbccddeeff"
	}
	wrapped := "(async()=>{" + sc.js + "\n})()"
	w.sendFrame(wire.Run{T: wire.TypeRun, JS: wrapped, Tools: sc.tools, Limits: sc.limits, Clock: sc.clock, Seed: sc.seed})

	type held struct {
		call wire.Call
	}
	var waiting []held
	inflight := 0
	reply := func(h held) {
		fn := sc.answers[h.call.Tool]
		var ret wire.Ret
		ret.T, ret.ID = wire.TypeRet, h.call.ID
		if fn == nil {
			ret.OK = true
			ret.Result = h.call.Args
			ret.Result, _ = json.Marshal(map[string]json.RawMessage{"echo": h.call.Args})
		} else {
			res, rerr := fn(h.call.Args)
			if rerr != nil {
				ret.Error = rerr
			} else {
				ret.OK = true
				ret.Result, _ = json.Marshal(res)
			}
		}
		w.sendFrame(ret)
		inflight--
	}
	for {
		wait := frameWait
		if len(waiting) > 0 {
			wait = 300 * time.Millisecond
		}
		data, err := w.next(wait)
		if err != nil {
			if len(waiting) > 0 && err != io.EOF {
				// nothing more arrives: answer what is waiting
				for _, h := range waiting {
					reply(h)
				}
				waiting = nil
				continue
			}
			t.Fatalf("worker stopped without done: %v (stderr %q)", err, w.stderr.String())
		}
		typ, err := wire.PeekType(data)
		if err != nil {
			t.Fatalf("bad frame from worker: %v", err)
		}
		switch typ {
		case wire.TypeCall:
			var c wire.Call
			if err := wire.Decode(data, &c); err != nil {
				t.Fatalf("bad call frame: %v", err)
			}
			out.calls = append(out.calls, c)
			inflight++
			if inflight > out.maxInFlight {
				out.maxInFlight = inflight
			}
			if sc.hold > 0 {
				waiting = append(waiting, held{c})
				if len(waiting) >= sc.hold {
					for _, h := range waiting {
						reply(h)
					}
					waiting = nil
				}
				continue
			}
			reply(held{c})
		case wire.TypeDone:
			out.doneRaw = data
			if err := wire.Decode(data, &out.done); err != nil {
				t.Fatalf("bad done frame: %v", err)
			}
			out.exit = w.waitExit(30 * time.Second)
			out.stderr = w.stderr.String()
			return out
		default:
			t.Fatalf("unexpected frame %q from the worker", typ)
		}
	}
}

// resultJSON decodes the result of a successful run.
func (o outcome) resultJSON(t *testing.T, v any) {
	t.Helper()
	if !o.done.OK {
		t.Fatalf("the run failed: %+v", o.done.Error)
	}
	if err := json.Unmarshal(o.done.Result, v); err != nil {
		t.Fatalf("result %s: %v", o.done.Result, err)
	}
}

// wantFail asserts the run ended with the given error code, no crash, nothing on stderr.
func (o outcome) wantFail(t *testing.T, code string) {
	t.Helper()
	if o.done.OK || o.done.Error == nil || o.done.Error.Code != code {
		t.Fatalf("want error %q, got ok=%v error=%+v result=%s", code, o.done.OK, o.done.Error, o.done.Result)
	}
	o.wantClean(t)
}

func (o outcome) wantClean(t *testing.T) {
	t.Helper()
	if o.exit != 0 {
		t.Fatalf("exit code %d, want 0", o.exit)
	}
	if o.stderr != "" {
		t.Fatalf("the worker wrote to stderr: %q", o.stderr)
	}
}
