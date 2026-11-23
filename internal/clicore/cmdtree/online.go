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

// runWith runs one operation on an open session and writes the result.
func (st *state) runWith(sess *clicore.Session, op string, args json.RawMessage, table func(w io.Writer, data json.RawMessage) error) error {
	callID, err := newCallID()
	if err != nil {
		return clicore.NewError(clicore.CodeInternal, "No random source for the call id.", "")
	}
	res, e := sess.Do(st.ctx, clicore.Call{Op: op, Args: args, RequestID: st.reqID, CallID: callID, Deadline: st.opts.timeout})
	if e != nil {
		return e
	}
	return st.emit(op, res.Data, res.Meta, func(w io.Writer) error { return table(w, res.Data) })
}

// requestArgs reads --request (a file or "-" for the input) as the arguments of the operation. It checks that the text is one JSON
// object; the owner checks the rest, field by field. A byte order mark and a UTF-16 file (what PowerShell 5.1 writes with ">") are read.
func (st *state) requestArgs(path string) (json.RawMessage, error) {
	var r io.Reader
	if path == "-" {
		r = st.io.In
	} else {
		f, err := os.Open(path) //nolint:gosec // the file the person named with --request
		if err != nil {
			return nil, usage("The --request file cannot be read.")
		}
		defer func() { _ = f.Close() }()
		if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
			return nil, usage("The --request file cannot be read.")
		}
		r = f
	}
	b, err := io.ReadAll(io.LimitReader(r, clicore.MaxRequestBytes+1))
	if err != nil {
		return nil, usage("The --request input cannot be read.")
	}
	if len(b) > clicore.MaxRequestBytes {
		return nil, clicore.NewError(clicore.CodeRequestTooLarge, "The request is larger than 1 MiB.", "")
	}
	b = decodeText(b)
	b = bytes.TrimSpace(b)
	if len(b) == 0 || b[0] != '{' || !json.Valid(b) {
		return nil, usage("The --request input must be one JSON object.")
	}
	return json.RawMessage(b), nil
}

// decodeText returns UTF-8 text for UTF-8 (with or without a byte order mark) and for UTF-16 with a byte order mark.
func decodeText(b []byte) []byte {
	switch {
	case bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}):
		return b[3:]
	case bytes.HasPrefix(b, []byte{0xFF, 0xFE}), bytes.HasPrefix(b, []byte{0xFE, 0xFF}):
		le := b[0] == 0xFF
		b = b[2:]
		u := make([]uint16, 0, len(b)/2)
		for i := 0; i+1 < len(b); i += 2 {
			if le {
				u = append(u, uint16(b[i])|uint16(b[i+1])<<8)
			} else {
				u = append(u, uint16(b[i])<<8|uint16(b[i+1]))
			}
		}
		return []byte(string(utf16.Decode(u)))
	}
	return b
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
