// Package mcpadmin is the person's side of MCP access (BL-347): the JSON routes of the AI access screen over the grant store.
// The host asks (enroll, over the ag pipe); only here does anyone approve, deny, revoke, reissue, renew or stop, and only with the
// session token, because internal/server mounts these routes in its own table (never on the ag pipe).
//
// Everything the host reported about itself (name, version, exe path) comes through as data: the store already refuses control,
// bidi, zero-width and private-use characters (mcpsrv validText), and the screen renders it as plain text. Nothing here builds HTML.
// Errors carry fixed text and a code, never the request's own text, a path or a driver message.
package mcpadmin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"time"

	"api-audit-explorer/internal/mcpaudit"
	"api-audit-explorer/internal/mcpsrv"
)

// maxBody bounds a request: an approval names at most 256 sources of 128 bytes.
const maxBody = 64 << 10

var idRe = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

// Deps is what the routes need. Now and Logf may be nil.
type Deps struct {
	Grants *mcpsrv.Grants
	Now    func() time.Time
	// Logf receives one fixed line per decision (approve, deny, revoke, reissue, renew, remove, stop, resume) with the profile id, which
	// the route has already checked against the id pattern. It never receives host text.
	Logf func(format string, args ...any)
}

type admin struct {
	g    *mcpsrv.Grants
	now  func() time.Time
	logf func(string, ...any)
}

// Handler serves the paths of internal/server/mcp_admin.go. The server has already checked the token, the Origin and the method.
func Handler(d Deps) (http.Handler, error) {
	if d.Grants == nil {
		return nil, errors.New("mcpadmin: no grant store")
	}
	a := &admin{g: d.Grants, now: d.Now, logf: d.Logf}
	if a.now == nil {
		a.now = time.Now
	}
	if a.logf == nil {
		a.logf = func(string, ...any) {}
	}
	return a, nil
}

// ProfileView is one host as the screen sees it. Digests never leave the store. The host's own text (HostName, HostVersion, Exe) is
// advisory: the screen shows it labelled "reported by the host".
type ProfileView struct {
	ID                   string     `json:"id"`
	State                string     `json:"state"` // pending, active, revoked, denied
	Expired              bool       `json:"expired"`
	Rev                  int        `json:"rev"`
	Enrolled             bool       `json:"enrolled"` // a host has connected: only then can the profile be approved
	Contested            int        `json:"contested"`
	HostName             string     `json:"hostName"`
	HostVersion          string     `json:"hostVersion"`
	Exe                  string     `json:"exe"`
	PPID                 int        `json:"ppid"`
	Tier                 string     `json:"tier"`
	Tools                []string   `json:"tools"`
	Sources              []string   `json:"sources"`
	Identifiers          bool       `json:"identifiers"`
	IdentifiersUntil     *time.Time `json:"identifiersUntil"`
	RequestedIdentifiers bool       `json:"requestedIdentifiers"`
	IdleMinutes          int        `json:"idleMinutes"`
	ExpiresAt            *time.Time `json:"expiresAt"`
	ApprovedAt           *time.Time `json:"approvedAt"`
	CreatedAt            *time.Time `json:"createdAt"`
	LastCallAt           *time.Time `json:"lastCallAt"`
	Claimed              bool       `json:"claimed"` // the host has collected its credential
}

type tierView struct {
	ID    string   `json:"id"`
	Tools []string `json:"tools"`
}

type limitsView struct {
	MaxProfiles        int `json:"maxProfiles"`
	DefaultIdleMinutes int `json:"defaultIdleMinutes"`
	MinIdleMinutes     int `json:"minIdleMinutes"`
	MaxIdleMinutes     int `json:"maxIdleMinutes"`
	DefaultIdentMin    int `json:"defaultIdentifiersMinutes"`
	MaxIdentMin        int `json:"maxIdentifiersMinutes"`
	MinCohort          int `json:"minCohort"`
}

type listView struct {
	Mode     string        `json:"mode"` // on
	Stopped  bool          `json:"stopped"`
	Now      time.Time     `json:"now"`
	Tiers    []tierView    `json:"tiers"`
	Limits   limitsView    `json:"limits"`
	Profiles []ProfileView `json:"profiles"`
}

func opt(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	t = t.UTC()
	return &t
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// View turns a stored profile into what the screen gets.
func View(p mcpsrv.Profile, now time.Time) ProfileView {
	return ProfileView{
		ID: p.ID, State: p.State, Rev: p.Rev, Expired: p.State == mcpaudit.StateActive && !p.ExpiresAt.IsZero() && !now.Before(p.ExpiresAt),
		Enrolled: len(p.ClaimDigest) > 0, Contested: p.Contested,
		HostName: p.HostName, HostVersion: p.HostVersion, Exe: p.Exe, PPID: p.PPID,
		Tier: p.Tier, Tools: nonNil(p.Tools), Sources: nonNil(p.Sources),
		Identifiers: p.Identifiers && mcpsrv.IdentifiersAllowed(p, now), IdentifiersUntil: opt(p.IdentifiersUntil), RequestedIdentifiers: p.RequestedIdentifiers,
		IdleMinutes: int(p.IdleTTL / time.Minute), ExpiresAt: opt(p.ExpiresAt), ApprovedAt: opt(p.ApprovedAt), CreatedAt: opt(p.CreatedAt),
		LastCallAt: opt(p.LastCallAt), Claimed: p.Claimed,
	}
}

type approveReq struct {
	// ExpectRev is the revision the card showed. It is required: an approval is only ever for the host the person looked at.
	ExpectRev          int      `json:"expectRev"`
	Tier               string   `json:"tier"`
	Tools              []string `json:"tools"`
	Sources            []string `json:"sources"`
	Identifiers        bool     `json:"identifiers"`
	IdentifiersMinutes int      `json:"identifiersMinutes"`
	IdleMinutes        int      `json:"idleMinutes"`
	AcceptContested    bool     `json:"acceptContested"`
}

type revReq struct {
	ExpectRev int `json:"expectRev"`
}

func (a *admin) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch p := r.URL.Path; {
	case r.Method == http.MethodGet && p == "/api/mcp/profiles":
		a.list(w, r)
	case r.Method == http.MethodPost && p == "/api/mcp/stop":
		a.stop(w, r)
	case r.Method == http.MethodPost && p == "/api/mcp/resume":
		a.resume(w, r)
	case r.Method == http.MethodPost && strings.HasPrefix(p, "/api/mcp/profiles/"):
		rest := strings.TrimPrefix(p, "/api/mcp/profiles/")
		id, action, ok := strings.Cut(rest, "/")
		if !ok || !idRe.MatchString(id) {
			fail(w, http.StatusNotFound, "not_found", "No such action.")
			return
		}
		a.act(w, r, id, action)
	default:
		fail(w, http.StatusNotFound, "not_found", "No such action.")
	}
}

func (a *admin) list(w http.ResponseWriter, r *http.Request) {
	ps, err := a.g.List(r.Context())
	if err != nil {
		fail(w, http.StatusInternalServerError, "internal", "The hosts could not be read.")
		return
	}
	now := a.now()
	out := listView{
		Mode: "on", Stopped: a.g.Stopped(), Now: now.UTC(), Profiles: make([]ProfileView, 0, len(ps)),
		Tiers: []tierView{{mcpsrv.TierAggregate, mcpsrv.ToolsForTier(mcpsrv.TierAggregate)}, {mcpsrv.TierEvidence, mcpsrv.ToolsForTier(mcpsrv.TierEvidence)}},
		Limits: limitsView{
			MaxProfiles: mcpsrv.MaxProfiles, DefaultIdleMinutes: int(mcpsrv.DefaultIdleTTL / time.Minute), MinIdleMinutes: int(mcpsrv.MinIdleTTL / time.Minute),
			MaxIdleMinutes: int(mcpsrv.MaxIdleTTL / time.Minute), DefaultIdentMin: int(mcpsrv.DefaultIdentifiers / time.Minute),
			MaxIdentMin: int(mcpsrv.MaxIdentifiers / time.Minute), MinCohort: mcpsrv.MinCohort,
		},
	}
	for _, p := range ps {
		out.Profiles = append(out.Profiles, View(p, now))
	}
	ok(w, out)
}

// act runs one decision on one host. approve and renew need the revision the card showed (expectRev, always sent by the screen); the
// others take it when sent. A stale revision is a 409 and nothing changes: the host may have been reissued or replaced since.
func (a *admin) act(w http.ResponseWriter, r *http.Request, id, action string) {
	switch action {
	case "approve":
		var req approveReq
		if !decode(w, r, &req) {
			return
		}
		if req.ExpectRev <= 0 {
			fail(w, http.StatusBadRequest, "invalid_arguments", "The request is not valid.")
			return
		}
		p, err := a.g.Approve(r.Context(), id, mcpsrv.Approval{
			Tier: req.Tier, Tools: req.Tools, Sources: req.Sources, Identifiers: req.Identifiers,
			IdentifiersFor: time.Duration(req.IdentifiersMinutes) * time.Minute, IdleTTL: time.Duration(req.IdleMinutes) * time.Minute,
			ExpectRev: req.ExpectRev, AcceptContested: req.AcceptContested,
		})
		a.done(w, id, "approved", p, err)
	case "deny", "revoke", "reissue", "renew", "remove":
		var req revReq
		if !decode(w, r, &req) {
			return
		}
		if (action == "renew" && req.ExpectRev <= 0) || req.ExpectRev < 0 {
			fail(w, http.StatusBadRequest, "invalid_arguments", "The request is not valid.")
			return
		}
		if req.ExpectRev > 0 {
			cur, err := a.current(r.Context(), id)
			if err != nil {
				a.done(w, id, action, mcpsrv.Profile{}, err)
				return
			}
			if cur.Rev != req.ExpectRev {
				a.done(w, id, action, mcpsrv.Profile{}, mcpsrv.ErrStaleApproval)
				return
			}
		}
		var p mcpsrv.Profile
		var err error
		switch action {
		case "deny":
			p, err = a.g.Deny(r.Context(), id)
		case "revoke":
			p, err = a.g.Revoke(r.Context(), id)
		case "reissue":
			p, err = a.g.Reissue(r.Context(), id)
		case "renew":
			p, err = a.g.Renew(r.Context(), id)
		case "remove":
			err = a.g.Remove(r.Context(), id)
		}
		a.done(w, id, action, p, err)
	default:
		fail(w, http.StatusNotFound, "not_found", "No such action.")
	}
}

func (a *admin) current(ctx context.Context, id string) (mcpsrv.Profile, error) {
	ps, err := a.g.List(ctx)
	if err != nil {
		return mcpsrv.Profile{}, err
	}
	for _, p := range ps {
		if p.ID == id {
			return p, nil
		}
	}
	return mcpsrv.Profile{}, mcpaudit.ErrNotFound
}

func (a *admin) done(w http.ResponseWriter, id, what string, p mcpsrv.Profile, err error) {
	if err != nil {
		status, code, msg := mapErr(err)
		a.logf("AI access: %s of host %s refused (%s)", what, id, code)
		fail(w, status, code, msg)
		return
	}
	a.logf("AI access: host %s %s", id, past(what))
	if p.ID == "" {
		ok(w, map[string]any{"ok": true})
		return
	}
	ok(w, map[string]any{"ok": true, "profile": View(p, a.now())})
}

func past(what string) string {
	switch what {
	case "approved":
		return "approved"
	case "deny":
		return "denied"
	case "revoke":
		return "revoked"
	case "reissue":
		return "reissued"
	case "renew":
		return "renewed"
	case "remove":
		return "removed"
	}
	return what
}

func (a *admin) stop(w http.ResponseWriter, r *http.Request) {
	if !decode(w, r, &struct{}{}) {
		return
	}
	// A stop that could not be stored is still in force in this run: say so, do not hide it as a failure.
	err := a.g.Stop(r.Context())
	saved := err == nil
	if err != nil && !errors.Is(err, mcpsrv.ErrStopNotSaved) {
		fail(w, http.StatusInternalServerError, "internal", "The stop could not be applied.")
		return
	}
	a.logf("AI access: stopped for every host (saved=%t)", saved)
	ok(w, map[string]any{"ok": true, "stopped": a.g.Stopped(), "saved": saved})
}

func (a *admin) resume(w http.ResponseWriter, r *http.Request) {
	if !decode(w, r, &struct{}{}) {
		return
	}
	if err := a.g.Resume(r.Context()); err != nil {
		// The stop stays in force.
		fail(w, http.StatusInternalServerError, "internal", "AI access could not be resumed. It stays stopped.")
		return
	}
	a.logf("AI access: resumed")
	ok(w, map[string]any{"ok": true, "stopped": a.g.Stopped(), "saved": true})
}

// mapErr gives every store error a status, a code and fixed text.
func mapErr(err error) (int, string, string) {
	switch {
	case errors.Is(err, mcpsrv.ErrStaleApproval):
		return http.StatusConflict, "stale", "This host changed since you opened it. Reload the list and look again."
	case errors.Is(err, mcpsrv.ErrContested):
		return http.StatusConflict, "contested", "Another program also tried to register as this host. Reissue it, or confirm that you accept the warning."
	case errors.Is(err, mcpsrv.ErrNoHostYet):
		return http.StatusConflict, "no_host", "No host has connected to this entry yet."
	case errors.Is(err, mcpsrv.ErrNotApprovable):
		return http.StatusConflict, "not_allowed", "This host is not in a state that allows that."
	case errors.Is(err, mcpsrv.ErrBadApproval):
		return http.StatusBadRequest, "invalid_arguments", "Those choices are not valid. Pick a tier, tools from it and at least one source."
	case errors.Is(err, mcpaudit.ErrNotFound):
		return http.StatusNotFound, "not_found", "No such host."
	}
	return http.StatusInternalServerError, "internal", "The request could not be processed."
}

// decode reads one JSON object (the screen sends {} when it has nothing to add) with the same strictness as the server's other routes.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		fail(w, http.StatusUnsupportedMediaType, "invalid_arguments", "Content-Type must be application/json.")
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var big *http.MaxBytesError
		if errors.As(err, &big) {
			fail(w, http.StatusRequestEntityTooLarge, "invalid_arguments", "The request is too large.")
		} else {
			fail(w, http.StatusBadRequest, "invalid_arguments", "The request is not valid.")
		}
		return false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		fail(w, http.StatusBadRequest, "invalid_arguments", "The request is not valid.")
		return false
	}
	return true
}

func ok(w http.ResponseWriter, v any) { send(w, http.StatusOK, v) }

func fail(w http.ResponseWriter, status int, code, msg string) {
	send(w, status, map[string]any{"error": msg, "code": code})
}

func send(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
