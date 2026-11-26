// Command unravis-sandbox is the code-mode sandbox worker (spec 04, BL-362).
//
// It runs exactly one script in QuickJS-ng compiled to WebAssembly, inside the wazero interpreter, and talks to the broker over
// stdin and stdout with the 4-byte-length frames of internal/codemode/wire. It has no file, network, environment or process access of
// its own: the only things the script can reach are the console, the clock, Math.random and the unravis.* host calls, and every one of
// those is a function this process answers. See docs/desktop/spikes/BL-362-worker.md.
//
// Exit codes: 0 after done was sent, 2 for a protocol violation (the broker sent something the protocol does not allow, no done is
// sent), 3 for a failure of the worker itself (no done is sent either). The broker reads both as a crashed worker.
package main

import (
	"bufio"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"

	"api-audit-explorer/internal/codemode/wire"
)

// wasmBytes is the engine. Its SHA-256 goes into hello; tools/codemode/BUILD-WASM.md says how it is built and qjs.wasm.sha256 pins it.
//
//go:embed qjs.wasm
var wasmBytes []byte

const (
	exitOK       = 0
	exitProtocol = 2
	exitInternal = 3

	runtimeName = "wazero-interp"
)

// errProtocol is a frame or an answer the protocol does not allow. It ends the worker with exitProtocol and no done frame.
type errProtocol struct{ what string }

func (e *errProtocol) Error() string { return "protocol: " + e.what }

func violation(format string, args ...any) error {
	return &errProtocol{what: fmt.Sprintf(format, args...)}
}

func main() {
	// Nothing the worker or a library prints may reach the broker's pipe or anywhere else.
	log.SetOutput(io.Discard)
	os.Exit(serve(os.Stdin, os.Stdout))
}

// serve is the whole life of the worker: hello, one run frame, the run, done.
func serve(stdin io.Reader, stdout io.Writer) int {
	c := &conn{in: bufio.NewReaderSize(stdin, 64<<10), out: stdout}
	if err := c.send(helloFrame()); err != nil {
		return exitInternal
	}
	data, err := c.recv()
	if err != nil {
		return exitCode(err)
	}
	if t, err := wire.PeekType(data); err != nil || t != wire.TypeRun {
		return exitProtocol
	}
	var run wire.Run
	if err := wire.Decode(data, &run); err != nil {
		return exitProtocol
	}

	s, err := newSession(c, run)
	if err != nil {
		return exitCode(err)
	}
	done, err := s.drive()
	s.close()
	if err != nil {
		return exitCode(err)
	}
	if err := c.send(done); err != nil {
		return exitInternal
	}
	return exitOK
}

func exitCode(err error) int {
	var p *errProtocol
	if errors.As(err, &p) {
		return exitProtocol
	}
	return exitInternal
}

func helloFrame() wire.Hello {
	sum := sha256.Sum256(wasmBytes)
	return wire.Hello{
		T:       wire.TypeHello,
		V:       wire.Version,
		Runtime: runtimeName,
		// The worker cannot see its own process mitigations and does not claim any: the spawner (BL-366) sets them, and the broker
		// requires the names it needs. Until then every one of them is false and a broker that requires them refuses the run.
		Mitigations: map[string]bool{"acg": false, "win32kOff": false, "childRestricted": false},
		WasmSHA256:  hex.EncodeToString(sum[:]),
	}
}

// conn is the frame stream. Only the goroutine that runs the script uses it, so no frame is ever interleaved.
type conn struct {
	in  *bufio.Reader
	out io.Writer
}

func (c *conn) send(v any) error {
	b, err := wire.Marshal(v)
	if err != nil {
		return err
	}
	return wire.WriteFrame(c.out, b)
}

// recv reads one frame. A stream that ends, a length over the maximum and a frame that is empty are all protocol violations.
func (c *conn) recv() ([]byte, error) {
	data, err := wire.ReadFrame(c.in, wire.MaxFrame)
	if err != nil {
		return nil, violation("%v", err)
	}
	return data, nil
}
