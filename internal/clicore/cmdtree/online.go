package cmdtree

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/spf13/cobra"

	"api-audit-explorer/internal/clicore"
)

// Annotations of the online commands.
const (
	onlineAnnotation  = "unravis.online"  // the command talks to the running app
	timeoutAnnotation = "unravis.timeout" // its default --timeout when neither the flag nor UNRAVIS_TIMEOUT is set
)

// CompareTimeout is the default --timeout of audit compare: the owner's own five-minute limit plus 30 seconds (spec 3.2).
const CompareTimeout = 5*time.Minute + 30*time.Second

func online(op, timeout string) map[string]string {
	m := emitting(op)
	m[onlineAnnotation] = "1"
	if timeout != "" {
		m[timeoutAnnotation] = timeout
	}
	return m
}

// attach opens the session with the app: the injected one in a test, the real one otherwise.
func (st *state) attach(ctx context.Context) (*clicore.Session, *clicore.Error) {
	if st.io.Attach != nil {
		return st.io.Attach(ctx)
	}
	return nil, clicore.NewError(clicore.CodeEndpointUnavailable, "This build of the command line has no way to reach Unravis.", "")
}

func newCallID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// runOp attaches, runs one operation with the given arguments and writes the result. args is a JSON object.
func (st *state) runOp(op string, args json.RawMessage, table func(w io.Writer, data json.RawMessage) error) error {
	sess, e := st.attach(st.ctx)
	if e != nil {
		return e
	}
	return st.runWith(sess, op, args, table)
}

// callOp runs one operation on an open session and returns what the owner answered.
func (st *state) callOp(sess *clicore.Session, op string, args json.RawMessage) (*clicore.Result, error) {
	callID, err := newCallID()
	if err != nil {
		return nil, clicore.NewError(clicore.CodeInternal, "No random source for the call id.", "")
	}
	res, e := sess.Do(st.ctx, clicore.Call{Op: op, Args: args, RequestID: st.reqID, CallID: callID, Deadline: st.opts.timeout})
	if e != nil {
		return nil, e
	}
	return res, nil
}

// runWith runs one operation on an open session and writes the result.
func (st *state) runWith(sess *clicore.Session, op string, args json.RawMessage, table func(w io.Writer, data json.RawMessage) error) error {
	res, err := st.callOp(sess, op, args)
	if err != nil {
		return err
	}
	return st.emit(op, res.Data, res.Meta, func(w io.Writer) error { return table(w, res.Data) })
}

// requestArgs reads --request (a file or "-" for the input) as the arguments of the operation. It checks that the text is one JSON
// object; the owner checks the rest, field by field. A byte order mark and a UTF-16 file (what PowerShell 5.1 writes with ">") are read.
func (st *state) requestArgs(path string) (json.RawMessage, error) {
	var raw []byte
	if path == "-" {
		var err error
		if raw, err = st.readLimited(st.io.In, 2*clicore.MaxRequestBytes+2, "The --request input cannot be read."); err != nil {
			return nil, err
		}
	} else {
		// Look before opening: a named pipe or a device would block the open past --timeout (and on Windows a \\.\pipe\ path is another program's).
		if fi, err := os.Stat(path); err != nil || !fi.Mode().IsRegular() {
			return nil, usage("The --request file cannot be read.")
		}
		f, err := os.Open(path) //nolint:gosec // the file the person named with --request
		if err != nil {
			return nil, usage("The --request file cannot be read.")
		}
		defer func() { _ = f.Close() }()
		if raw, err = st.readLimited(f, 2*clicore.MaxRequestBytes+2, "The --request input cannot be read."); err != nil {
			return nil, err
		}
	}
	b, ok := decodeText(raw)
	if !ok {
		return nil, usage("The --request input is not valid text (UTF-8 or UTF-16).")
	}
	if len(b) > clicore.MaxRequestBytes {
		return nil, clicore.NewError(clicore.CodeRequestTooLarge, "The request is larger than 1 MiB.", "")
	}
	b = bytes.TrimSpace(b)
	if len(b) == 0 || b[0] != '{' || !json.Valid(b) {
		return nil, usage("The --request input must be one JSON object.")
	}
	return json.RawMessage(b), nil
}

// readLimited reads an input of the command, at most n bytes (the callers allow twice the limit of the text, since UTF-16 doubles it), and
// stops when the command's own context ends: a reader that never answers (a terminal, a stalled pipe) cannot hold the program past --timeout.
// unreadable is the usage error when the read fails.
func (st *state) readLimited(r io.Reader, n int64, unreadable string) ([]byte, error) {
	type result struct {
		b   []byte
		err error
	}
	ch := make(chan result, 1)
	go func() {
		b, err := io.ReadAll(io.LimitReader(r, n))
		ch <- result{b, err}
	}()
	select {
	case res := <-ch:
		if res.err != nil {
			return nil, usage(unreadable)
		}
		return res.b, nil
	case <-st.ctx.Done():
		if errors.Is(st.ctx.Err(), context.DeadlineExceeded) {
			return nil, clicore.NewError(clicore.CodeTimeout, "The --request input did not arrive in time.", "")
		}
		return nil, clicore.NewError(clicore.CodeCanceled, "Canceled.", "")
	}
}

// decodeText returns UTF-8 text for UTF-8 (with or without a byte order mark) and for UTF-16 with a byte order mark. ok is false for a
// UTF-16 text of odd length or with a lone surrogate: it is cut or damaged, and the request is not guessed.
func decodeText(b []byte) ([]byte, bool) {
	switch {
	case bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}):
		return b[3:], true
	case bytes.HasPrefix(b, []byte{0xFF, 0xFE}), bytes.HasPrefix(b, []byte{0xFE, 0xFF}):
		le := b[0] == 0xFF
		b = b[2:]
		if len(b)%2 != 0 {
			return nil, false
		}
		u := make([]uint16, 0, len(b)/2)
		for i := 0; i+1 < len(b); i += 2 {
			if le {
				u = append(u, uint16(b[i])|uint16(b[i+1])<<8)
			} else {
				u = append(u, uint16(b[i])<<8|uint16(b[i+1]))
			}
		}
		for i := 0; i < len(u); i++ {
			switch {
			case u[i] >= 0xD800 && u[i] < 0xDC00:
				if i+1 >= len(u) || u[i+1] < 0xDC00 || u[i+1] > 0xDFFF {
					return nil, false
				}
				i++
			case u[i] >= 0xDC00 && u[i] <= 0xDFFF:
				return nil, false
			}
		}
		return []byte(string(utf16.Decode(u))), true
	}
	return b, true
}

// requestOr returns the --request arguments when given, else build() from the field flags. The two never mix: a field flag next to
// --request is a usage error (spec 3.2).
func (st *state) requestOr(c *cobra.Command, fieldFlags []string, build func() (json.RawMessage, error)) (json.RawMessage, error) {
	if st.opts.request == "" {
		return build()
	}
	for _, f := range fieldFlags {
		if c.Flags().Changed(f) {
			return nil, usage("--request cannot be combined with --" + f + ".")
		}
	}
	return st.requestArgs(st.opts.request)
}

// preOnline is the check of the global flags that only the online commands take.
func (st *state) preOnline(c *cobra.Command) error {
	online := c.Annotations[onlineAnnotation] == "1"
	if c.Flags().Changed("request") && st.opts.request == "" {
		return usage("--request needs a file name, or - for the input.")
	}
	if st.opts.request != "" && !online {
		return usage("--request goes with a command that calls the running app.")
	}
	if online && (st.opts.profile != "" || st.io.Getenv("UNRAVIS_PROFILE") != "") {
		return usage("Automation profiles are not available in this version of the command line.")
	}
	if !c.Flags().Changed("timeout") && st.io.Getenv("UNRAVIS_TIMEOUT") == "" {
		if d := c.Annotations[timeoutAnnotation]; d != "" {
			if v, err := time.ParseDuration(d); err == nil {
				st.opts.timeout = v
			}
		}
	}
	return nil
}

var errNotObject = errors.New("not an object")

// pretty prints JSON data as indented lines (the table form of an operation that has no table of its own).
func pretty(w io.Writer, data json.RawMessage) error {
	var out bytes.Buffer
	if err := json.Indent(&out, data, "", "  "); err != nil {
		return errNotObject
	}
	for _, line := range strings.Split(out.String(), "\n") {
		if _, err := io.WriteString(w, clicore.Printable(line)+"\n"); err != nil {
			return err
		}
	}
	return nil
}
