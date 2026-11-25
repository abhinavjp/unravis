// Package mcpagent is the host side of MCP access (BL-345, the part that runs and is tested without Windows): it enrolls the host with
// the backend, keeps the credential the person's approval produced, and sends tool calls to POST /mcp/v1/call with it.
//
// The transport is a Dialer, the named pipe on Windows and any net.Conn in tests. Order on every connection, and the reason for it:
//
//  1. hello: the backend says its pid and generation; they must equal the locator record the caller read. A peer that is not the owner
//     the locator names gets no credential, no nonce and no tool arguments.
//  2. enroll or claim (only with no secret yet), or call with the secret.
//
// Every failure before an answer is a tool error in the envelope shape of the stdio server (backend_unavailable, permission_required),
// never a crash and never a line on stdout. A call whose bytes were sent is not repeated: the outcome is unknown and a comparison is
// heavy. Nothing here logs; the secret, the nonce and the arguments are never put in an error text.
package mcpagent

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"api-audit-explorer/internal/locator"
	"api-audit-explorer/internal/mcp"
)

// Dialer opens one connection to the backend's ag endpoint.
type Dialer func(ctx context.Context) (net.Conn, error)

// Expect is what the locator says about the owner the caller means to talk to.
type Expect struct {
	PID int
	Gen string
}

// ExpectFrom turns the locator record into what hello must say: the owner's pid and its start time, written the way the owner writes
// it in hello (UTC, RFC 3339 with nanoseconds).
func ExpectFrom(r locator.Record) Expect {
	return Expect{PID: r.PID, Gen: r.StartTime.UTC().Format(time.RFC3339Nano)}
}

const (
	maxAnswer   = 8 << 20 // an answer is cut by the backend long before this
	maxHello    = 4 << 10
	maxRequest  = 1 << 20
	scheme      = "Unravis-Grant "
	notRunning  = "Unravis is not running."
	startAction = "Start Unravis, then retry."
)

// Agent is one host installation's client. It is safe for concurrent use: each call has its own connection.
type Agent struct {
	Dial    Dialer
	Locate  func() (Expect, error) // reads the locator and checks the owner is alive; ErrNoOwner when it is not
	Store   Store
	Profile string

	HostName, HostVersion, Exe string
	PPID                       int
	RequestIdentifiers         bool

	// Rand and Now may be nil.
	Rand io.Reader
	Now  func() time.Time
}

type helloAnswer struct {
	V   int    `json:"v"`
	PID int    `json:"pid"`
	Gen string `json:"gen"`
}

// Answer mirrors the body of POST /mcp/v1/call (internal/mcpsrv.Answer). It is declared here so the host's binary does not link the store.
type answer struct {
	OK      bool            `json:"ok"`
	IsError bool            `json:"isError"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Action  string `json:"action"`
		Reason  string `json:"reason"`
	} `json:"error"`
}

func (a *Agent) rand() io.Reader {
	if a.Rand != nil {
		return a.Rand
	}
	return rand.Reader
}

func (a *Agent) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// CallEnvelope runs one tool at the backend and returns the envelope and whether it is an error answer: the same two values as
// mcp.Bridge.CallEnvelope, so the stdio server can use either.
func (a *Agent) CallEnvelope(ctx context.Context, tool string, args json.RawMessage) ([]byte, bool) {
	fail := func(code, msg, action string) ([]byte, bool) { return mcp.ErrorEnvelope(tool, code, msg, action), true }
	if !mcp.KnownTool(tool) {
		return fail(mcp.WireInvalidArguments, "unknown tool; only the advertised read-only audit tools are available", "")
	}
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}
	if len(args) > maxRequest-512 || !mcp.StrictJSON(args) {
		return fail(mcp.WireInvalidArguments, "The arguments are not valid.", "")
	}
	want, err := a.Locate()
	if err != nil {
		return fail(mcp.WireBackendUnavailable, notRunning, startAction)
	}
	s, err := a.connect(ctx, want)
	if err != nil {
		return fail(mcp.WireBackendUnavailable, "Unravis did not answer.", startAction)
	}
	defer s.close()

	cred, err := a.credential(ctx, s)
	switch {
	case errors.Is(err, errWaiting):
		return fail(mcp.WirePermissionRequired, "This host is waiting for approval in Unravis.", "Open Unravis > AI access and approve this host.")
	case errors.Is(err, errNeedReissue):
		return fail(mcp.WirePermissionRequired, "This host could not collect its credential.", "Open Unravis > AI access, reissue this host, then retry.")
	case errors.Is(err, errUnavailable):
		return fail(mcp.WireBackendUnavailable, "Unravis could not take this host's request just now.", "Retry in a moment. If it keeps failing, open Unravis > AI access.")
	case err != nil:
		return fail(mcp.WireBackendUnavailable, "Unravis could not store this host's credential.", "Check that this Windows user can write to the Unravis folder, then retry.")
	}

	idBytes := make([]byte, 16)
	if _, err := io.ReadFull(a.rand(), idBytes); err != nil {
		return fail(mcp.WireInternal, "This host could not make a call id.", "Retry once.")
	}
	body, _ := json.Marshal(map[string]any{"tool": tool, "args": args, "callId": hex.EncodeToString(idBytes), "deadlineMs": deadlineMs(ctx, a.now())})
	resp, sent, err := s.do(ctx, http.MethodPost, "/mcp/v1/call", scheme+cred.Profile+"."+cred.Secret, body)
	if err != nil {
		if sent {
			return fail(mcp.WireBackendUnavailable, "The connection ended before Unravis answered. The query may have run.", "Do not repeat a heavy query at once. Check Unravis, then retry once.")
		}
		return fail(mcp.WireBackendUnavailable, "Unravis did not answer.", startAction)
	}
	var ans answer
	if json.Unmarshal(resp.body, &ans) != nil {
		return fail(mcp.WireBackendUnavailable, "Unravis answered in a way this host does not understand.", "Update Unravis and the host, then retry.")
	}
	if ans.OK && len(ans.Result) > 0 && json.Valid(ans.Result) {
		return []byte(ans.Result), ans.IsError
	}
	if ans.Error == nil {
		return fail(mcp.WireInternal, "Unravis refused the call.", "Retry once.")
	}
	if ans.Error.Reason == "pending" {
		// The person reissued this host: the old secret is dead, and the next call enrolls again.
		_ = a.Store.Delete(a.Profile)
	}
	return fail(ans.Error.Code, ans.Error.Message, ans.Error.Action)
}

var (
	errWaiting     = errors.New("waiting for approval")
	errNeedReissue = errors.New("claim refused")
	errUnavailable = errors.New("backend did not take the request") // transient: the nonce is kept and the next call tries again
)

// credential returns the secret of this host, enrolling and claiming when it has none. It runs on a connection that passed hello.
func (a *Agent) credential(ctx context.Context, s *session) (Cred, error) {
	c, err := a.Store.Load(a.Profile)
	if err == nil && c.Secret != "" {
		return c, nil
	}
	if err != nil && !errors.Is(err, ErrNoCred) {
		return Cred{}, err
	}
	if errors.Is(err, ErrNoCred) {
		nonce, nerr := newNonce(a.rand())
		if nerr != nil {
			return Cred{}, nerr
		}
		c = Cred{V: 1, Profile: a.Profile, Nonce: nonce}
		// The nonce is stored before it is sent: a run that dies in between still holds the only key to the claim.
		if err := a.Store.Save(c); err != nil {
			return Cred{}, err
		}
	}
	enroll, _ := json.Marshal(map[string]any{
		"profile": a.Profile, "hostName": a.HostName, "hostVersion": a.HostVersion, "exe": a.Exe, "ppid": a.PPID,
		"claimNonce": c.Nonce, "requestIdentifiers": a.RequestIdentifiers,
	})
	if r, _, err := s.do(ctx, http.MethodPost, "/mcp/v1/enroll", "", enroll); err != nil || r.status != http.StatusOK {
		return Cred{}, errUnavailable // not "reissue this host": nothing says the person's card is wrong
	}
	claim, _ := json.Marshal(map[string]string{"profile": a.Profile, "claimNonce": c.Nonce})
	r, _, err := s.do(ctx, http.MethodPost, "/mcp/v1/enroll/claim", "", claim)
	if err != nil {
		return Cred{}, errUnavailable // the nonce stays: a claim the server may have taken is not spent
	}
	switch r.status {
	case http.StatusOK:
		var got struct {
			Secret string `json:"secret"`
		}
		if json.Unmarshal(r.body, &got) != nil {
			return Cred{}, errNeedReissue
		}
		got2 := Cred{V: 1, Profile: a.Profile, Secret: got.Secret}
		if !got2.valid() {
			return Cred{}, errNeedReissue
		}
		if err := a.Store.Save(got2); err != nil {
			return Cred{}, err
		}
		return got2, nil
	case http.StatusConflict: // still pending
		return Cred{}, errWaiting
	case http.StatusGone, http.StatusForbidden, http.StatusNotFound:
		// Gone (already claimed, or the host was reissued), a nonce that is not ours, or a profile that no longer exists: the nonce is
		// spent. A new one is made next time.
		_ = a.Store.Delete(a.Profile)
		return Cred{}, errNeedReissue
	default:
		// A server error, a limit or anything else that says nothing about the nonce: keep it, so the claim can still be made. Deleting
		// it here would make the next call enroll with a new one and mark the person's card contested.
		return Cred{}, errUnavailable
	}
}

type session struct {
	conn net.Conn
	br   *bufio.Reader
	stop func() bool
}

func (s *session) close() {
	s.stop()
	_ = s.conn.Close()
}

type reply struct {
	status int
	body   []byte
}

// connect dials (one retry, only for a connect error: nothing was sent) and runs hello. A peer that fails any part of hello is dropped
// before anything else is sent.
func (a *Agent) connect(ctx context.Context, want Expect) (*session, error) {
	var conn net.Conn
	var err error
	for try := 0; try < 2; try++ {
		if conn, err = a.Dial(ctx); err == nil {
			break
		}
		if ctx.Err() != nil {
			return nil, err
		}
	}
	if err != nil {
		return nil, err
	}
	s := &session{conn: conn, br: bufio.NewReader(conn)}
	// The call ends with ctx: closing the connection is how a cancelled call stops waiting.
	s.stop = context.AfterFunc(ctx, func() { _ = conn.Close() })
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	r, _, err := s.do(ctx, http.MethodGet, "/mcp/v1/hello", "", nil)
	if err != nil || r.status != http.StatusOK {
		s.close()
		return nil, errors.New("hello failed")
	}
	var h helloAnswer
	if json.Unmarshal(r.body, &h) != nil || h.V != 1 || h.PID != want.PID || h.Gen != want.Gen || want.PID <= 0 {
		s.close()
		return nil, errors.New("peer is not the owner")
	}
	return s, nil
}

// do sends one request on the connection and reads one answer. sent says whether any byte of the request may have left (the caller
// must not repeat a call that was sent).
func (s *session) do(ctx context.Context, method, path, auth string, body []byte) (reply, bool, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://unravis.local"+path, rd)
	if err != nil {
		return reply{}, false, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	req.Header.Set("User-Agent", "unravis-mcp-agent")
	if err := req.Write(s.conn); err != nil {
		return reply{}, true, err
	}
	resp, err := http.ReadResponse(s.br, req)
	if err != nil {
		return reply{}, true, err
	}
	defer func() { _ = resp.Body.Close() }()
	limit := int64(maxAnswer)
	if path == "/mcp/v1/hello" {
		limit = maxHello
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil || int64(len(b)) > limit {
		return reply{}, true, fmt.Errorf("answer too large or cut")
	}
	return reply{resp.StatusCode, b}, true, nil
}

func deadlineMs(ctx context.Context, now time.Time) int64 {
	if dl, ok := ctx.Deadline(); ok {
		if d := dl.Sub(now).Milliseconds(); d > 0 {
			return d
		}
	}
	return 0
}
