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
	"time"

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
	// CLIGate is the command-line window the broker was built with (Config.Authorize = CLIGate.Authorize). The settings route opens and
	// closes it. Nil: the route refuses cliMinutes, and the broker's own gate (if any) stays as it is.
	CLIGate *codemode.CLIGate
}

// Install turns the code routes on: the mount serves them with this package's handler, and a case the app opens gets the broker's
// sweep of runs a crash left open.
func Install(o Options) {
	api := &api{broker: o.Broker, files: o.Settings, allowIdentifiers: o.AllowIdentifiers, cliGate: o.CLIGate}
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
	cliGate          *codemode.CLIGate
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
	case codemode.CodeModeOff, codemode.CodeDenied, codemode.CodeCLIClosed:
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
	// CLIOpenUntil is when the window that lets the command line run scripts ends (RFC 3339, UTC); empty while it is closed. The window
	// is not a setting: it is kept in memory, so it is closed whenever the app starts.
	CLIOpenUntil string `json:"cliOpenUntil"`
	// CLIUnlimited is true while the command line is allowed with no end ("no time limit"). Always present, false when it is not on. Like
	// the window it is kept in memory only: it is false whenever the app starts or code mode is switched off, and while it is true
	// cliOpenUntil stays empty.
	CLIUnlimited bool `json:"cliUnlimited"`
	// Damaged is true when code-mode.json cannot be read or trusted: code mode is then off until the settings are saved again.
	Damaged bool `json:"damaged"`
}

func (a *api) settingsReply(set codemode.Settings, damaged bool) settingsDTO {
	d := settingsDTO{Enabled: set.Enabled, AllowDegraded: set.AllowDegraded, KeepResults: set.KeepResults, AllowIdentifiers: a.allowIdentifiers, Damaged: damaged}
	if a.cliGate != nil {
		until, open, unlimited := a.cliGate.Snapshot()
		if open {
			d.CLIOpenUntil = until.UTC().Format(time.RFC3339)
		}
		d.CLIUnlimited = unlimited
	}
	return d
}

func (a *api) getSettings(w http.ResponseWriter) {
	// Under the same lock as a PUT, so the read of the file and the state of the gate are one moment. A read that finds code mode off or
	// the file damaged shuts the command line (the answer is never "off" or "damaged" together with an open command line), so editing
	// the file back to "on" does not bring an open command line back.
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	set, err := a.files.Load()
	if err != nil || !set.Enabled {
		a.closeBecauseOff()
	}
	reply(w, http.StatusOK, a.settingsReply(set, err != nil))
}

// closeBecauseOff shuts the command line (and records it, when it was open) at a moment when code mode was seen to be off or its file
// damaged. Closing needs no disk and never fails.
func (a *api) closeBecauseOff() {
	if a.cliGate == nil {
		return
	}
	before := cliStateOf(a.cliGate)
	a.cliGate.Close()
	logCLIChange(before, "closed", " because code mode is off", 0)
}

// settingsPatch holds only the switches the caller sent; the others stay as they are.
type settingsPatch struct {
	Enabled       *bool `json:"enabled"`
	AllowDegraded *bool `json:"allowDegraded"`
	KeepResults   *bool `json:"keepResults"`
	// CLIMinutes opens the window that lets the command line run scripts: 1 to 60 minutes from now, or 0 to close it. Not saved to disk.
	CLIMinutes *int `json:"cliMinutes"`
	// CLIUnlimited true allows the command line with no end (until it is closed, code mode goes off, or the app stops). It cannot be sent
	// with cliMinutes above 0. False closes the command line (unless cliMinutes above 0 opens a timed window in the same request). Not
	// saved to disk.
	CLIUnlimited *bool `json:"cliUnlimited"`
}

func (a *api) putSettings(w http.ResponseWriter, r *http.Request) {
	var in settingsPatch
	if !decode(w, r, maxSettingsBody, &in) {
		return
	}
	if in.CLIMinutes != nil && (a.cliGate == nil || *in.CLIMinutes < 0 || *in.CLIMinutes > int(codemode.MaxCLIWindow/time.Minute)) {
		fail(w, http.StatusBadRequest, codeBadRequest, "The command line can be allowed for 1 to 60 minutes, or 0 to close it.")
		return
	}
	if in.CLIUnlimited != nil && *in.CLIUnlimited {
		if a.cliGate == nil {
			fail(w, http.StatusBadRequest, codeBadRequest, "The command line cannot be allowed in this build.")
			return
		}
		if in.CLIMinutes != nil && *in.CLIMinutes > 0 {
			fail(w, http.StatusBadRequest, codeBadRequest, "Choose either a number of minutes or no time limit, not both.")
			return
		}
	}
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	// Shutting needs no disk, so a request that shuts the command line (or leaves the no-time-limit state for a timed window) does it first:
	// a failed read or save below must never leave the command line open that the caller asked to close. Opening still waits for the save.
	closedNow := a.closeFirst(in)
	notChanged := "Nothing was changed."
	if closedNow {
		notChanged = "The command line was shut; nothing else was changed."
	}
	// A damaged file reads as all off: the save below starts from that, which repairs it.
	set, lerr := a.files.Load()
	if lerr != nil && !errors.Is(lerr, settingsfile.ErrCorrupt) {
		// A read that failed (not a file that cannot be trusted): saving would turn off switches the caller did not send.
		fail(w, http.StatusInternalServerError, codemode.CodeInternal, "The settings could not be read. "+notChanged)
		return
	}
	// Code mode was on before this request only if the file was read and said so. A missing, damaged or "off" file counts as off, so a
	// request that switches it on does not bring back a command line that was open before it went off (the file may have been edited by
	// hand, with no request or run in between to shut the gate).
	wasOn := lerr == nil && set.Enabled
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
		fail(w, http.StatusInternalServerError, codemode.CodeInternal, "The settings could not be saved. "+notChanged)
		return
	}
	// The window follows the save, so a failed save leaves it as it was. Switching code mode off closes it too: the next time it is
	// switched on, the command line starts shut again. The no-time-limit state follows the same rules (and a timed window replaces it).
	// Every case that is not a clear "open" closes: an unlimited request while code mode is off, or a window of 0 minutes, ends shut.
	if a.cliGate != nil {
		before := cliStateOf(a.cliGate)
		switch {
		case !set.Enabled:
			a.cliGate.Close()
			logCLIChange(before, cliStateOf(a.cliGate), " because code mode is off", 0)
		case in.CLIUnlimited != nil && *in.CLIUnlimited:
			a.cliGate.OpenUnlimited()
			logCLIChange(before, cliStateOf(a.cliGate), "", 0)
		case in.CLIMinutes != nil:
			a.cliGate.Open(time.Duration(*in.CLIMinutes) * time.Minute)
			logCLIChange(before, cliStateOf(a.cliGate), "", *in.CLIMinutes)
		case in.CLIUnlimited != nil, !wasOn:
			// "No time limit" off, or code mode just switched on from off without opening the command line in the same request.
			a.cliGate.Close()
			logCLIChange(before, cliStateOf(a.cliGate), "", 0)
		}
	}
	log.Printf("Code mode settings were saved.")
	reply(w, http.StatusOK, a.settingsReply(set, false))
}

// closeFirst shuts the command line before the settings are read or saved when the request asks to close it: code mode off, "no time
// limit" off, a window of 0 minutes, or a timed window replacing the no-time-limit state (the state that has no end must not outlive a
// failed request to leave it). It reports whether the gate was open and is now shut. The caller holds settingsMu.
func (a *api) closeFirst(in settingsPatch) bool {
	if a.cliGate == nil {
		return false
	}
	closes := (in.Enabled != nil && !*in.Enabled) ||
		(in.CLIUnlimited != nil && !*in.CLIUnlimited) ||
		(in.CLIMinutes != nil && (*in.CLIMinutes == 0 || a.cliGate.Unlimited()) && (in.CLIUnlimited == nil || !*in.CLIUnlimited))
	if !closes {
		return false
	}
	before := cliStateOf(a.cliGate)
	a.cliGate.Close()
	if before == "closed" {
		return false
	}
	if in.Enabled != nil && !*in.Enabled {
		log.Printf("Command line window: closed because code mode is off.")
	} else {
		log.Printf("Command line window: closed.")
	}
	return true
}

// cliStateOf names what the gate allows now: "closed", "timed" or "unlimited".
func cliStateOf(g *codemode.CLIGate) string {
	switch {
	case g.Unlimited():
		return "unlimited"
	case g.Allows():
		return "timed"
	}
	return "closed"
}

// logCLIChange is the record of a change to the command-line window (BL-371), one line in the app's log: opened for a time, opened with no
// time limit, or closed, and whether code mode going off was the reason. It holds no case, record or script text. It is called only when
// a request acted on the gate. A timed window that is opened again is a change (the end moves), so every "timed" result is recorded; a
// request that left the gate shut when it was already shut is not. The window running out by itself is not a settings change and is not
// recorded here.
func logCLIChange(before, after, why string, minutes int) {
	switch after {
	case "unlimited":
		log.Printf("Command line window: opened with no time limit.")
	case "timed":
		log.Printf("Command line window: opened for %d minutes.", minutes)
	default:
		if before != "closed" {
			log.Printf("Command line window: closed%s.", why)
		}
	}
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
