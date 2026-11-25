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
	// BL-349 (evidence.go): the result's size and last use for the handle bounds, and the options of a host's bridge.
	resultSize int
	used       time.Time
	resultCap  int
	minter     EvidenceMinter
	cursors    CursorCodec
	now        func() time.Time
	// allowIdentifiers is the operator switch (--mcp-allow-identifiers). Off by
	// default. It is the only thing that decides whether identifiers are shown.
	allowIdentifiers bool
	// scoped says the bridge may reach only the sources in sources (a host's grant, BL-348). A nil set with scoped false is the
	// unscoped bridge of the stdio server and the command line. Scoped with an empty set reaches no source at all.
	// minCohort is the smallest count shown (suppress.go); zero shows every count. hideRecordIDs removes the audit record id from rows.
	minCohort     int
	hideRecordIDs bool
	scoped        bool
	sources       map[string]struct{}
	sessionFile   string
	envToken      string
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

// WithSources limits the bridge to the given source ids: the source scope of a host's grant. Every request that names sources is
// refused unless each one is in the list (checked at the one place that talks to the dashboard, so no tool can skip it), and the source
// list shows only these. An empty list reaches no source. Without this option the bridge is unscoped, as the stdio server and the
// command line are.
func WithSources(ids []string) Option {
	return func(b *Bridge) {
		b.scoped = true
		b.sources = make(map[string]struct{}, len(ids))
		for _, id := range ids {
			b.sources[id] = struct{}{}
		}
	}
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
			return "", "", coded(CodeBackendUnavailable, notRunning)
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
		return coded(CodeBackendUnavailable, notRunning)
	}
	owners, ok := listenerOwners(record.Port)
	if !ok {
		return nil
	}
	for _, owner := range owners {
		if owner != record.Pid {
			return coded(CodeBackendUnavailable, notOwner)
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

// noIdentifiers is the reason text of a refused identifier filter. On the stdio server the switch is the operator's flag; a bridge
// scoped to a host's grant has no flag, the permission is the person's choice in Unravis, so the text must not point at a flag that does
// nothing for that host.
func (b *Bridge) noIdentifiers() string {
	if b.scoped {
		return "the identifiers permission is not on for this host. Ask the person to allow it in Unravis > AI access."
	}
	return "the operator has not turned on --mcp-allow-identifiers."
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
	return codedWire(CodeInvalidArguments, WirePermissionRequired, "The "+strings.Join(names, ", ")+" filters are not available: "+b.noIdentifiers()+" User ids, user agents, IP addresses, URLs and audit record ids are hidden, and filtering on them would reveal them")
}

// scopeRefused is the answer to a request that names a source outside the grant. It never repeats the ids that were asked for.
const scopeRefused = "That request names a source this host is not approved for. Use audit_list_sources for the sources this host may read, or ask the person to widen the access in Unravis."

// checkScope refuses a request whose sources are not all in the scope of a scoped bridge. It reads the typed input the tools are about to
// send, so what is checked is exactly what the dashboard receives. Input of a type it does not know, or with no source at all, is
// refused on a scoped bridge: that fails closed if a new route is added without a scope rule. A request with no input (the source list
// and the info) names no source.
func (b *Bridge) checkScope(input any) error {
	if !b.scoped || input == nil {
		return nil
	}
	var ids []string
	switch in := input.(type) {
	case audit.Query:
		ids = in.SourceIDs
	case audit.ActivityInput:
		ids = in.SourceIDs
	case audit.PageInput:
		ids = in.Query.SourceIDs
	case audit.BreakdownInput:
		ids = in.Query.SourceIDs
	default:
		return codedWire(CodeInvalidArguments, WirePermissionRequired, scopeRefused)
	}
	if len(ids) == 0 {
		return codedWire(CodeInvalidArguments, WirePermissionRequired, scopeRefused)
	}
	for _, id := range ids {
		if _, ok := b.sources[id]; !ok {
			return codedWire(CodeInvalidArguments, WirePermissionRequired, scopeRefused)
		}
	}
	return nil
}

func boundedBody(r io.Reader, limit int) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, int64(limit)+1))
	if err != nil || len(data) > limit {
		return nil, coded(CodeResultTooLarge, "audit response exceeded its read budget")
	}
	return data, nil
}

// session locates the dashboard. When its token changed since the last call,
// the cached comparison belongs to another dashboard session and is dropped.
func (b *Bridge) session() (string, string, error) {
	if b.locate == nil {
		return "", "", coded(CodeBackendUnavailable, notRunning)
	}
	base, token, err := b.locate()
	if err != nil {
		return "", "", err
	}
	if token == "" || len(token) > maxTokenBytes {
		return "", "", coded(CodeBackendUnavailable, notRunning)
	}
	b.mu.Lock()
	changed := b.token != token
	if changed {
		b.token = token
		b.result = nil
		b.resultID = ""
		b.resultSize = 0
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
		return coded(CodeInternal, "audit operation is not allowed")
	}
	if err := b.checkScope(input); err != nil {
		return err
	}
	var payload []byte
	var err error
	if input != nil {
		payload, err = json.Marshal(input)
		if err != nil {
			return coded(CodeInternal, "invalid audit request")
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
		req.Header.Set("User-Agent", "unravis-mcp") // the dashboard counts an MCP client at work as "in use" for updates
		if input != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		res, err := b.client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					return coded(CodeDeadlineExceeded, "audit operation cancelled or its five-minute deadline expired")
				}
				return coded(CodeCancelled, "audit operation cancelled or its five-minute deadline expired")
			}
			if errors.Is(err, errUnproven) {
				return coded(CodeBackendUnavailable, unproven)
			}
			return coded(CodeBackendUnavailable, "Unravis did not answer. Start the dashboard, then retry")
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
				return coded(CodeBackendUnavailable, "Unravis refused the session. Restart the dashboard, then retry")
			case 413:
				return coded(CodeBudgetExceeded, "audit processing limit reached; increase bucketSeconds, narrow controller/action or sources, and retry explicitly in the dashboard for its detailed limit message")
			case 400:
				return coded(CodeInvalidArguments, "audit query rejected; verify source IDs, date windows, filters and chart interval")
			case 404:
				return codedWire(CodeNotFound, WireSourceUnavailable, "audit source or record is unavailable; list sources again")
			case 423:
				return coded(CodeLocked, "Unravis is locked; unlock it in the app")
			case 429:
				return coded(CodeBusy, "audit service is busy; wait and retry explicitly")
			case 504:
				return coded(CodeDeadlineExceeded, "audit operation exceeded its five-minute deadline")
			default:
				return coded(CodeInternal, fmt.Sprintf("local audit operation failed (HTTP %d); check the dashboard", res.StatusCode))
			}
		}
		if err = json.Unmarshal(data, output); err != nil {
			return coded(CodeInternal, "local audit response was not valid data")
		}
		return nil
	}
	return coded(CodeBackendUnavailable, "local audit session changed; list sources and retry")
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
