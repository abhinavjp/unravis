// Package codeapi is the HTTP side of code mode (BL-574 slice 1, spec 04): the handlers of /api/code/* over the Broker of
// internal/codemode, the case resolver, and the glue to the server's CodeMount.
//
// internal/server owns what every route needs (the session token, the host and Origin check, the method rules, the refusal of the
// MCP bridge, and the lease of a case against an erase); it cannot import codemode, because codemode's own tests import the server.
// So the server keeps a static route table and this package, which imports both, hangs its handler on the server's CodeMount.
//
// Who runs: a request that reaches these handlers carries the owner's own session token. It is an owner run: unscoped, no grant, no
// tier. The surface is "cli" when the unravis command line sent it (its User-Agent) and "ui" for everything else. The MCP bridge is
// refused by the server before it gets here; an MCP run (surface "mcp", scoped by a grant) goes through another entrance in a later slice.
package codeapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/codemode"
	"api-audit-explorer/internal/codemode/bindings"
	"api-audit-explorer/internal/codemode/settingsfile"
	"api-audit-explorer/internal/server"
)

// Limits of the routes.
const (
	// maxRunBody bounds the body of POST /api/code/run: a script of codemode.MaxSource bytes may grow six times when JSON escapes it
	// (a control character is six bytes), plus the small fields.
	maxRunBody = 6*codemode.MaxSource + 8<<10
	// maxSettingsBody bounds PUT /api/code/settings (three booleans).
	maxSettingsBody = 4 << 10
	// maxCaseIDBytes bounds the case id of a run; the store validates the shape.
	maxCaseIDBytes = 64
	// cliUserAgentPrefix is what the unravis command line sends (internal/clicore).
	cliUserAgentPrefix = "unravis-cli/"
)

// Stable error codes added by this layer (the broker's own are in internal/codemode/errors.go).
const (
	codeNotFound    = "not_found"
	codeBadRequest  = codemode.CodeBadRequest
	codeUnsupported = "unsupported_media_type"
)

var cancelRe = regexp.MustCompile(`^/api/code/runs/([A-Za-z0-9_-]{1,64})/cancel$`)

// Options is what Install needs.
type Options struct {
	Mount    *server.CodeMount
	Broker   *codemode.Broker
	Settings *settingsfile.Store
	// AllowIdentifiers is the operator's permission, the same value the Broker was built with. It is shown on the settings screen;
	// the Broker is what enforces it.
	AllowIdentifiers bool
}

// Install turns the code routes on: the mount serves them with this package's handler, and a case the app opens gets the broker's
// sweep of runs a crash left open.
func Install(o Options) {
	api := &api{broker: o.Broker, files: o.Settings, allowIdentifiers: o.AllowIdentifiers}
	o.Mount.SetCaseStopper(func(caseID string) { o.Broker.CancelCase(caseID) })
	o.Mount.Set(api, func(ctx context.Context, store casestore.CodeRunStore) error {
		_, err := o.Broker.OnCaseOpen(ctx, store)
		return err
	})
}

type api struct {
	broker           *codemode.Broker
	files            *settingsfile.Store
	allowIdentifiers bool
	settingsMu       sync.Mutex // a PUT reads, merges and writes: one at a time, so two switches do not lose each other
	typesOnce        sync.Once
	types            []byte
}

func (a *api) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch p := r.URL.Path; {
	case p == "/api/code/types" && r.Method == http.MethodGet:
		a.getTypes(w)
	case p == "/api/code/settings" && r.Method == http.MethodGet:
		a.getSettings(w)
	case p == "/api/code/settings" && r.Method == http.MethodPut:
		a.putSettings(w, r)
	case p == "/api/code/run" && r.Method == http.MethodPost:
		a.run(w, r)
	case r.Method == http.MethodPost && cancelRe.MatchString(p):
		a.cancel(w, cancelRe.FindStringSubmatch(p)[1])
	default:
		fail(w, http.StatusNotFound, codeNotFound, "That code-mode route does not exist.")
	}
}

/* ------------------------------------------------------------- replies */

type problem struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// reply writes v as JSON. A reply is never cached (the session routes say so too) and never sniffed.
func reply(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		body, status = []byte(`{"code":"internal_error","message":"The app hit an internal problem."}`), http.StatusInternalServerError
	}
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write(append(body, '\n'))
}

// fail answers with the stable {code, message}. message is always a fixed text of this package or of the broker; it never repeats the
// request, a path, a case title or anything from audit data.
func fail(w http.ResponseWriter, status int, code, message string) {
	reply(w, status, problem{Code: code, Message: message})
}

// statusOf is the HTTP status of a broker refusal. A script that ran and failed is not a refusal: it is a 200 with its status in the body.
func statusOf(code string) int {
	switch code {
	case codemode.CodeModeOff, codemode.CodeDenied:
		return http.StatusForbidden
	case codemode.CodeNoCase:
		return http.StatusConflict
	case codemode.CodeBusy:
		return http.StatusTooManyRequests
	case codemode.CodeSourceTooLarge:
		return http.StatusRequestEntityTooLarge
	case codemode.CodeSyntaxError, codemode.CodeBadRequest:
		return http.StatusBadRequest
	case codemode.CodeSandboxUnavailable, codemode.CodeSandboxDegradedNope:
		return http.StatusServiceUnavailable
	}
	return http.StatusInternalServerError // audit_failed, internal_error and anything new: not the caller's fault
}

// decode reads one JSON object from the body into v: application/json only, at most limit bytes, no unknown field, no second value.
// It answers the failure itself and reports false.
func decode(w http.ResponseWriter, r *http.Request, limit int64, v any) bool {
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		fail(w, http.StatusUnsupportedMediaType, codeUnsupported, "Send the request as application/json.")
		return false
	}
	body := http.MaxBytesReader(w, r.Body, limit)
	dec := json.NewDecoder(body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			fail(w, http.StatusRequestEntityTooLarge, codemode.CodeSourceTooLarge, "The request is too big.")
			return false
		}
		fail(w, http.StatusBadRequest, codeBadRequest, "The request is not valid JSON of the expected shape.")
		return false
	}
	if _, err := dec.Token(); err != io.EOF {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			fail(w, http.StatusRequestEntityTooLarge, codemode.CodeSourceTooLarge, "The request is too big.")
			return false
		}
		fail(w, http.StatusBadRequest, codeBadRequest, "The request must hold one JSON object.")
		return false
	}
	return true
}

/* --------------------------------------------------------------- types */

type limitsDTO struct {
	MaxInFlight       int   `json:"maxInFlight"`
	MaxCalls          int   `json:"maxCalls"`
	MaxResultBytes    int   `json:"maxResultBytes"`
	MaxLogBytes       int   `json:"maxLogBytes"`
	RunCapSeconds     int   `json:"runCapSeconds"`
	MaxSourceBytes    int   `json:"maxSourceBytes"`
	MaxConcurrentRuns int   `json:"maxConcurrentRuns"`
	DefaultTimeoutMS  int64 `json:"defaultTimeoutMs"`
	MinTimeoutMS      int64 `json:"minTimeoutMs"`
	MaxTimeoutMS      int64 `json:"maxTimeoutMs"`
}

type typesDTO struct {
	Namespace    string    `json:"namespace"`
	DTS          string    `json:"dts"`
	SchemaSHA256 string    `json:"schemaSha256"`
	Limits       limitsDTO `json:"limits"`
}

// buildTypes is the document of GET /api/code/types and of the command line's code.types: the typings, their hash and the limits.
func buildTypes() (typesDTO, error) {
	res, err := bindings.Types()
	if err != nil {
		return typesDTO{}, err
	}
	return typesDTO{Namespace: bindings.Namespace, DTS: res.DTS, SchemaSHA256: res.SchemaSHA256, Limits: limitsDTO{
		MaxInFlight: codemode.MaxInFlight, MaxCalls: codemode.MaxCalls, MaxResultBytes: codemode.MaxResultBytes, MaxLogBytes: codemode.MaxLogBytes,
		RunCapSeconds: int(codemode.RunCap.Seconds()), MaxSourceBytes: codemode.MaxSource, MaxConcurrentRuns: codemode.MaxConcurrentRuns,
		DefaultTimeoutMS: codemode.DefaultTimeout.Milliseconds(), MinTimeoutMS: codemode.MinTimeout.Milliseconds(), MaxTimeoutMS: codemode.MaxTimeout.Milliseconds(),
	}}, nil
}

func (a *api) getTypes(w http.ResponseWriter) {
	a.typesOnce.Do(func() {
		if dto, err := buildTypes(); err == nil {
			a.types, _ = json.Marshal(dto)
		}
	})
	if a.types == nil {
		fail(w, http.StatusInternalServerError, codemode.CodeInternal, "The app hit an internal problem.")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(append(append([]byte{}, a.types...), '\n'))
}

/* ------------------------------------------------------------ settings */

type settingsDTO struct {
	Enabled       bool `json:"enabled"`
	AllowDegraded bool `json:"allowDegraded"`
	KeepResults   bool `json:"keepResults"`
	// AllowIdentifiers is the operator's permission, read-only here: it is set when the app starts.
	AllowIdentifiers bool `json:"allowIdentifiers"`
	// Damaged is true when code-mode.json cannot be read or trusted: code mode is then off until the settings are saved again.
	Damaged bool `json:"damaged"`
}

func (a *api) settingsReply(set codemode.Settings, damaged bool) settingsDTO {
	return settingsDTO{Enabled: set.Enabled, AllowDegraded: set.AllowDegraded, KeepResults: set.KeepResults, AllowIdentifiers: a.allowIdentifiers, Damaged: damaged}
}

func (a *api) getSettings(w http.ResponseWriter) {
	set, err := a.files.Load()
	reply(w, http.StatusOK, a.settingsReply(set, err != nil))
}

// settingsPatch holds only the switches the caller sent; the others stay as they are.
type settingsPatch struct {
	Enabled       *bool `json:"enabled"`
	AllowDegraded *bool `json:"allowDegraded"`
	KeepResults   *bool `json:"keepResults"`
}

func (a *api) putSettings(w http.ResponseWriter, r *http.Request) {
	var in settingsPatch
	if !decode(w, r, maxSettingsBody, &in) {
		return
	}
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	// A damaged file reads as all off: the save below starts from that, which repairs it.
	set, lerr := a.files.Load()
	if lerr != nil && !errors.Is(lerr, settingsfile.ErrCorrupt) {
		// A read that failed (not a file that cannot be trusted): saving would turn off switches the caller did not send.
		fail(w, http.StatusInternalServerError, codemode.CodeInternal, "The settings could not be read. Nothing was changed.")
		return
	}
	if in.Enabled != nil {
		set.Enabled = *in.Enabled
	}
	if in.AllowDegraded != nil {
		set.AllowDegraded = *in.AllowDegraded
	}
	if in.KeepResults != nil {
		set.KeepResults = *in.KeepResults
	}
	if err := a.files.Save(set); err != nil {
		fail(w, http.StatusInternalServerError, codemode.CodeInternal, "The settings could not be saved. Nothing was changed.")
		return
	}
	log.Printf("Code mode settings were saved.")
	reply(w, http.StatusOK, a.settingsReply(set, false))
}

/* ----------------------------------------------------------------- run */

type runBody struct {
	Code             string `json:"code"`
	CaseID           string `json:"caseId"`
	TimeoutMS        int    `json:"timeoutMs"`
	Reason           string `json:"reason"`
	AllowIdentifiers bool   `json:"allowIdentifiers"`
}

// surfaceOf is the route-level rule for who is calling: the command line says so in its User-Agent, everything else is the app's own
// page. It only labels the audit record; the same session token is behind both and neither is scoped.
func surfaceOf(r *http.Request) string {
	if strings.HasPrefix(r.Header.Get("User-Agent"), cliUserAgentPrefix) {
		return "cli"
	}
	return "ui"
}

func (a *api) run(w http.ResponseWriter, r *http.Request) {
	var in runBody
	if !decode(w, r, maxRunBody, &in) {
		return
	}
	if len(in.CaseID) > maxCaseIDBytes || in.TimeoutMS < 0 {
		fail(w, http.StatusBadRequest, codeBadRequest, "The request is not valid.")
		return
	}
	// Every case handle and read share the resolver takes lives until the run is over, and is let go here, whatever way Run ended.
	ctx, release := WithLeases(r.Context())
	defer release()
	surface := surfaceOf(r)
	res, err := a.broker.Run(ctx, codemode.RunRequest{
		Code: in.Code, CaseID: in.CaseID, TimeoutMS: in.TimeoutMS, Reason: in.Reason, Surface: surface, CallerID: surface,
		AllowIdentifiers: in.AllowIdentifiers,
	})
	if err != nil {
		var ref *codemode.Refusal
		if !errors.As(err, &ref) {
			fail(w, http.StatusInternalServerError, codemode.CodeInternal, "The app hit an internal problem and stopped the run. The app is unaffected.")
			return
		}
		msg := ref.Message
		if ref.Code == codemode.CodeNoCase && leasesOf(ctx).many() {
			msg = "More than one case is open. Name the case with caseId."
		}
		fail(w, statusOf(ref.Code), ref.Code, msg)
		return
	}
	reply(w, http.StatusOK, res)
}

/* -------------------------------------------------------------- cancel */

func (a *api) cancel(w http.ResponseWriter, id string) {
	if !a.broker.Cancel(id) {
		fail(w, http.StatusNotFound, codeNotFound, "No run with that id is running.")
		return
	}
	reply(w, http.StatusOK, map[string]bool{"cancelled": true})
}

/* ------------------------------------------------------------ resolver */

// leaseScope collects what the resolver took for one run.
type leaseScope struct {
	mu       sync.Mutex
	releases []func()
	manyOpen bool
	closed   bool
}

type leaseKey struct{}

// WithLeases returns a context in which Resolver may take case handles, and the function that gives them all back. Call the function
// after Broker.Run has returned (the broker does not touch the store after that); calling it twice is harmless.
func WithLeases(ctx context.Context) (context.Context, func()) {
	l := &leaseScope{}
	return context.WithValue(ctx, leaseKey{}, l), l.releaseAll
}

func leasesOf(ctx context.Context) *leaseScope {
	l, _ := ctx.Value(leaseKey{}).(*leaseScope)
	return l
}

func (l *leaseScope) many() bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.manyOpen
}

func (l *leaseScope) releaseAll() {
	l.mu.Lock()
	rel := l.releases
	l.releases, l.closed = nil, true
	l.mu.Unlock()
	for i := len(rel) - 1; i >= 0; i-- {
		rel[i]()
	}
}

// Resolver is codemode.Config.Cases: the one open case by default, or the case named by caseID. It errors with codemode.ErrNoCase when
// none is open, when more than one is open and none is named, when the named case is not open, and when ctx has no WithLeases scope
// (a handle taken without a scope could never be given back).
func Resolver(m *server.CodeMount) codemode.CaseResolver {
	return func(ctx context.Context, caseID string) (casestore.CodeRunStore, string, error) {
		l := leasesOf(ctx)
		if l == nil {
			return nil, "", fmt.Errorf("%w: no lease scope", codemode.ErrNoCase)
		}
		store, id, release, err := m.OpenCase(ctx, caseID)
		if err != nil {
			if errors.Is(err, server.ErrCodeManyCases) {
				l.mu.Lock()
				l.manyOpen = true
				l.mu.Unlock()
			}
			return nil, "", fmt.Errorf("%w: %v", codemode.ErrNoCase, err)
		}
		l.mu.Lock()
		if l.closed {
			l.mu.Unlock()
			release()
			return nil, "", fmt.Errorf("%w: run is over", codemode.ErrNoCase)
		}
		l.releases = append(l.releases, release)
		l.mu.Unlock()
		return store, id, nil
	}
}

// ManyOpen says whether the resolver, in a context from WithLeases, found more than one case open and none named (the reason of a
// no_case refusal that the host can fix by naming one).
func ManyOpen(ctx context.Context) bool { return leasesOf(ctx).many() }
