// Package mcp exposes bounded read-only audit tools over MCP stdio.
package mcp

import (
	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/sessionproof"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maxBody = 16 << 20
const resultTTL = 10 * time.Minute

// locator returns the dashboard's base URL and session token. It runs before
// every request, so a dashboard restarted on a new port or with a new token is
// picked up without restarting the bridge.
type locator func() (base, token string, err error)

type Bridge struct {
	locate    locator
	client    *http.Client
	mu        sync.Mutex
	token     string
	result    *audit.Analysis
	resultID  string
	created   time.Time
	comparing bool
	now       func() time.Time
	// allowIdentifiers is the operator switch (--mcp-allow-identifiers). Off by
	// default. It is the only thing that decides whether identifiers are shown.
	allowIdentifiers bool
	sessionFile      string
	envToken         string
	// proven is set by New: the client's transport runs the session proof on
	// each connection, with the token passed through the request context.
	proven bool
}

// Option configures a Bridge.
type Option func(*Bridge)

// WithIdentifiers lets MCP return user ids, user agents and IP addresses, and
// accept filters on them and on URLs and audit record ids.
func WithIdentifiers(allowed bool) Option {
	return func(b *Bridge) { b.allowIdentifiers = allowed }
}

// WithSessionFile names the session file the dashboard writes (see
// SessionFilePath). The bridge reads the port and token from it per request.
func WithSessionFile(path string) Option {
	return func(b *Bridge) { b.sessionFile = path }
}

// WithToken supplies the session token directly (AUDIT_SESSION_TOKEN), for a
// dashboard started with that same variable. It overrides the file's token.
func WithToken(token string) Option {
	return func(b *Bridge) { b.envToken = token }
}

// unproven is the message when the peer on the dashboard's port could not prove
// it holds the session token. The token was not sent.
const unproven = "Unravis did not prove it is the dashboard started for this user, so the session token was not sent. Restart the dashboard, then retry"

// notOwner is the message when a live process other than the dashboard that
// wrote the session file listens on its port.
const notOwner = "Another program is listening on the dashboard's port. Restart the dashboard, then retry"

// errUnproven marks a connection whose peer failed the session proof.
var errUnproven = errors.New("session proof failed")

// proofTimeout bounds one session proof exchange.
const proofTimeout = 5 * time.Second

// maxProofBody bounds the proof response body.
const maxProofBody = 4096

type proofTokenKey struct{}

// provenDial dials addr and, before handing the connection to the HTTP client,
// runs the session proof on it with the token carried in ctx (set by request).
// A connection is used for X-Audit-Token only after its peer has shown it holds
// that token, so a process squatting a stale port never receives it.
func provenDial(dialer *net.Dialer) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		token, _ := ctx.Value(proofTokenKey{}).(string)
		if token == "" {
			return nil, errUnproven
		}
		conn, err := dialer.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		if err := proveConn(ctx, conn, addr, token); err != nil {
			conn.Close()
			return nil, err
		}
		return conn, nil
	}
}

// proveConn sends GET /api/session-proof with a fresh nonce on conn and checks,
// in constant time, that the answer is HMAC(token, the peer's address, nonce).
func proveConn(ctx context.Context, conn net.Conn, host, token string) error {
	nonce, err := sessionproof.NewNonce()
	if err != nil {
		return errUnproven
	}
	deadline := time.Now().Add(proofTimeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	defer conn.SetDeadline(time.Time{})
	request := "GET " + sessionproof.Path + "?n=" + nonce + " HTTP/1.1\r\nHost: " + host + "\r\nAccept: application/json\r\nUser-Agent: unravis-mcp\r\n\r\n"
	if _, err := io.WriteString(conn, request); err != nil {
		return errUnproven
	}
	reader := bufio.NewReader(conn)
	res, err := http.ReadResponse(reader, nil)
	if err != nil {
		return errUnproven
	}
	body, err := boundedBody(res.Body, maxProofBody)
	res.Body.Close()
	// Bytes past the response would be read as the next response: refuse them.
	if err != nil || res.StatusCode != http.StatusOK || res.Close || reader.Buffered() != 0 {
		return errUnproven
	}
	var answer struct {
		Proof string `json:"proof"`
	}
	if json.Unmarshal(body, &answer) != nil || !sessionproof.Verify(token, conn.RemoteAddr().String(), nonce, answer.Proof) {
		return errUnproven
	}
	return nil
}

// notRunning is the plain message for a missing, unreadable or invalid session
// file. It never names the path or echoes the file's content.
const notRunning = "Unravis is not running for this Windows user. Start the dashboard, then retry"

// New builds a bridge to the dashboard on loopback. With port 0 the port comes
// from the session file; an explicit port must match it. The token comes from
// WithToken when given, otherwise from the session file. Either way, every
// connection must pass the session proof before the token is sent on it.
func New(port int, opts ...Option) (*Bridge, error) {
	if port < 0 || port > 65535 {
		return nil, errors.New("MCP port must be between 1 and 65535, or omitted to use the running dashboard's session file")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = provenDial(&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second})
	transport.ForceAttemptHTTP2 = false
	client := &http.Client{Transport: transport, Timeout: audit.OperationTimeout + 5*time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }}
	b := newBridge(nil, client, opts...)
	b.proven = true
	if b.envToken != "" && !ValidToken(b.envToken) {
		return nil, errors.New("AUDIT_SESSION_TOKEN must be 32 to 4096 visible ASCII characters")
	}
	if b.envToken != "" && port != 0 {
		base, token := "http://127.0.0.1:"+strconv.Itoa(port), b.envToken
		b.locate = func() (string, string, error) { return base, token, nil }
		return b, nil
	}
	if b.sessionFile == "" {
		return nil, errors.New("MCP could not find the per-user application folder for the dashboard's session file; set AUDIT_CONFIG_DIR")
	}
	path, explicit, envToken := b.sessionFile, port, b.envToken
	b.locate = func() (string, string, error) {
		record, err := readSessionRecord(path)
		if err != nil {
			return "", "", errors.New(notRunning)
		}
		filePort, fileToken := record.Port, record.Token
		if err := checkSessionProcess(path, record); err != nil {
			return "", "", err
		}
		if explicit != 0 && explicit != filePort {
			return "", "", errors.New("the running dashboard is not on the --port given to --mcp; omit --port so MCP follows the dashboard")
		}
		if envToken != "" {
			fileToken = envToken
		}
		return "http://127.0.0.1:" + strconv.Itoa(filePort), fileToken, nil
	}
	return b, nil
}

// checkSessionProcess refuses a session file whose dashboard process is gone
// (and removes the stale file) or, on Windows, whose port is held by another
// process. Files without a pid skip the check; the session proof still applies.
func checkSessionProcess(path string, record sessionRecord) error {
	if record.Pid == 0 {
		return nil
	}
	if !processAlive(record.Pid) {
		_ = RemoveSessionFile(path, record.Token)
		return errors.New(notRunning)
	}
	owners, ok := listenerOwners(record.Port)
	if !ok {
		return nil
	}
	for _, owner := range owners {
		if owner != record.Pid {
			return errors.New(notOwner)
		}
	}
	return nil
}

func newBridge(locate locator, client *http.Client, opts ...Option) *Bridge {
	b := &Bridge{locate: locate, client: client, now: time.Now}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// identifiersAllowed reports whether user ids, user agents and IP addresses may
// be returned, and identifier filters accepted. It is the local operator switch
// and nothing else: no dashboard response, demo flag included, can relax it.
func (b *Bridge) identifiersAllowed() bool {
	return b.allowIdentifiers
}

// identifierFilterNames lists the record filters of q that match on a user id,
// a user agent, an IP address, a URL or an audit record id. URLs can carry
// personal data in paths and query strings, and an audit id pins one person's
// request, so each is treated as an identifier.
func identifierFilterNames(q audit.Query) []string {
	var names []string
	if len(q.UserIDs) > 0 {
		names = append(names, "userIds")
	}
	if q.UserAgentContains != "" {
		names = append(names, "userAgentContains")
	}
	if len(q.UserIPs) > 0 {
		names = append(names, "userIps")
	}
	if q.URLContains != "" {
		names = append(names, "urlContains")
	}
	if q.AuditID != "" {
		names = append(names, "auditId")
	}
	return names
}

// checkIdentifierFilters refuses identifier filters while identifiers are hidden.
// Otherwise a model could bisect a hidden user id, agent, address or URL through
// repeated filtered counts. It names the filters but never echoes their values.
func (b *Bridge) checkIdentifierFilters(q audit.Query) error {
	names := identifierFilterNames(q)
	if len(names) == 0 || b.identifiersAllowed() {
		return nil
	}
	return errors.New("The " + strings.Join(names, ", ") + " filters are not available: the operator has not turned on --mcp-allow-identifiers. User ids, user agents, IP addresses, URLs and audit record ids are hidden, and filtering on them would reveal them")
}

func boundedBody(r io.Reader, limit int) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, int64(limit)+1))
	if err != nil || len(data) > limit {
		return nil, errors.New("audit response exceeded its read budget")
	}
	return data, nil
}

// session locates the dashboard. When its token changed since the last call,
// the cached comparison belongs to another dashboard session and is dropped.
func (b *Bridge) session() (string, string, error) {
	if b.locate == nil {
		return "", "", errors.New(notRunning)
	}
	base, token, err := b.locate()
	if err != nil {
		return "", "", err
	}
	if token == "" || len(token) > maxTokenBytes {
		return "", "", errors.New(notRunning)
	}
	b.mu.Lock()
	changed := b.token != token
	if changed {
		b.token = token
		b.result = nil
		b.resultID = ""
	}
	b.mu.Unlock()
	if changed && b.client != nil {
		// Idle connections were proven for the previous token.
		b.client.CloseIdleConnections()
	}
	return base, token, nil
}

func (b *Bridge) request(ctx context.Context, path string, input, output any) error {
	switch path {
	case "/api/info", "/api/sources", "/api/analyze", "/api/requests", "/api/activity", "/api/slowest", "/api/breakdown":
	default:
		return errors.New("audit operation is not allowed")
	}
	var payload []byte
	var err error
	if input != nil {
		payload, err = json.Marshal(input)
		if err != nil {
			return errors.New("invalid audit request")
		}
	}
	for attempt := 0; attempt < 2; attempt++ {
		base, token, err := b.session()
		if err != nil {
			return err
		}
		method := http.MethodGet
		if input != nil {
			method = http.MethodPost
		}
		reqCtx := ctx
		if b.proven {
			reqCtx = context.WithValue(ctx, proofTokenKey{}, token)
		}
		req, _ := http.NewRequestWithContext(reqCtx, method, base+path, bytes.NewReader(payload))
		req.Header.Set("X-Audit-Token", token)
		if input != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		res, err := b.client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return errors.New("audit operation cancelled or its five-minute deadline expired")
			}
			if errors.Is(err, errUnproven) {
				return errors.New(unproven)
			}
			return errors.New("Unravis did not answer. Start the dashboard, then retry")
		}
		data, readErr := boundedBody(res.Body, maxBody)
		res.Body.Close()
		if readErr != nil {
			return readErr
		}
		if res.StatusCode == http.StatusUnauthorized && attempt == 0 {
			// The dashboard may have restarted between reading the session file
			// and this request: drop the cached result and locate it again.
			b.mu.Lock()
			b.token = ""
			b.result = nil
			b.resultID = ""
			b.mu.Unlock()
			continue
		}
		if res.StatusCode != http.StatusOK {
			// Never reflect arbitrary upstream text, URLs, credentials or driver errors.
			switch res.StatusCode {
			case 401:
				return errors.New("Unravis refused the session. Restart the dashboard, then retry")
			case 413:
				return errors.New("audit processing limit reached; increase bucketSeconds, narrow controller/action or sources, and retry explicitly in the dashboard for its detailed limit message")
			case 400:
				return errors.New("audit query rejected; verify source IDs, date windows, filters and chart interval")
			case 404:
				return errors.New("audit source or record is unavailable; list sources again")
			case 429:
				return errors.New("audit service is busy; wait and retry explicitly")
			case 504:
				return errors.New("audit operation exceeded its five-minute deadline")
			default:
				return fmt.Errorf("local audit operation failed (HTTP %d); check the dashboard", res.StatusCode)
			}
		}
		if err = json.Unmarshal(data, output); err != nil {
			return errors.New("local audit response was not valid data")
		}
		return nil
	}
	return errors.New("local audit session changed; list sources and retry")
}
func strictArgs(data json.RawMessage, target any) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		data = []byte(`{}`)
	}
	if len(data) == 0 || data[0] != '{' {
		return errors.New("tool arguments must be an object")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil {
		return errors.New("invalid or unknown tool arguments; follow the tool input schema")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("invalid trailing tool arguments")
	}
	return nil
}
