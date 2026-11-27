package releasedash

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"strings"

	"api-audit-explorer/internal/activitylog"
	"api-audit-explorer/internal/hsaction"
)

// The write channel of the Homestretch page: POST /api/action/<id> runs one action of internal/hsaction, the same
// registry `unravis-ci` uses, so the page can do nothing the command line cannot, and every attempt is in the activity
// log. Everything else this server answers stays GET and HEAD only.
//
// A request must pass, in this order (each check fails closed):
//   - a loopback Host (404), against DNS rebinding;
//   - the method POST (405);
//   - the session cookie (401), which only the start link sets;
//   - an Origin that is this server's own loopback origin (403). A request without an Origin is let on only when
//     Sec-Fetch-Site is absent, same-origin or none, so a browser that says a request is cross-site is refused;
//   - the per-launch action token in X-Homestretch-Token, compared in constant time (403). It is a second secret,
//     apart from the session code: the program puts it only in the fragment of the start link (#a=...), which a
//     browser never sends to any server, and the page keeps it in sessionStorage. No route serves it. Cookies are not
//     kept apart by port, so another loopback listener the browser talks to gets our cookie, and could read anything
//     the cookie opens; it cannot get the fragment;
//   - Content-Type application/json (415);
//   - an action id the registry has (404);
//   - a body of at most 16 KiB (413), one JSON object {"project": "<id>", "args": {...}} with no unknown fields (400).
//
// No CORS header is ever sent.

const (
	actionPrefix   = "/api/action/"
	tokenHeader    = "X-Homestretch-Token"
	maxActionBody  = 16 << 10
	actionTokenLen = 32
)

// NewActionToken makes the per-launch token of the write channel (Dash.ActionToken). An empty token (the system has
// no randomness) turns the write channel off.
func NewActionToken() string {
	b := make([]byte, actionTokenLen)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

// actionEnvelope is the body of POST /api/action/<id>.
type actionEnvelope struct {
	Project string          `json:"project"`
	Args    json.RawMessage `json:"args,omitempty"`
}

// actionRefuse is the answer of a request the write channel refused before it reached an action.
func actionRefuse(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(hsaction.Result{OK: false, Message: msg, Error: msg})
}

// sameOrigin reports whether the request comes from this server's own page.
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		switch r.Header.Get("Sec-Fetch-Site") {
		case "", "same-origin", "none":
			return true
		}
		return false
	}
	return origin == "http://"+r.Host
}

// serveAction answers POST /api/action/<id>. The caller has checked the Host and set the security headers.
func (d *Dash) serveAction(w http.ResponseWriter, r *http.Request, sessionToken, actionToken string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		actionRefuse(w, http.StatusMethodNotAllowed, "An action is sent with POST.")
		return
	}
	if c, err := r.Cookie(cookieName); err != nil || subtle.ConstantTimeCompare([]byte(c.Value), []byte(sessionToken)) != 1 {
		d.logRefusal(r, "no session cookie")
		actionRefuse(w, http.StatusUnauthorized, "Open the link printed by unravis-releases.")
		return
	}
	if d.OnRequest != nil {
		d.OnRequest()
	}
	if !sameOrigin(r) {
		d.logRefusal(r, "not from this page (Origin or Sec-Fetch-Site)")
		actionRefuse(w, http.StatusForbidden, "Actions are taken only from this page.")
		return
	}
	if actionToken == "" {
		d.logRefusal(r, "actions are off (no action token)")
		actionRefuse(w, http.StatusServiceUnavailable, "Actions are off: this PC could not make a secret for them. Restart unravis-releases.")
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get(tokenHeader)), []byte(actionToken)) != 1 {
		d.logRefusal(r, "missing or wrong action token")
		actionRefuse(w, http.StatusForbidden, "This page has no action key. Open the link that unravis-releases printed (the whole link, with the part after #) and try again.")
		return
	}
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		actionRefuse(w, http.StatusUnsupportedMediaType, "An action is sent as JSON.")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, actionPrefix)
	if _, ok := hsaction.Lookup(id); !ok {
		actionRefuse(w, http.StatusNotFound, "There is no such action.")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxActionBody))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			actionRefuse(w, http.StatusRequestEntityTooLarge, "The request is too large.")
			return
		}
		actionRefuse(w, http.StatusBadRequest, "The request could not be read.")
		return
	}
	var env actionEnvelope
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&env); err != nil {
		actionRefuse(w, http.StatusBadRequest, "The request is not one JSON object with project and args: "+err.Error())
		return
	}
	if _, err := dec.Token(); err != io.EOF {
		actionRefuse(w, http.StatusBadRequest, "The request has more than one JSON value.")
		return
	}
	if len(env.Project) > 40 {
		actionRefuse(w, http.StatusNotFound, "That project is not in the project list.")
		return
	}
	v, ok := d.ci(env.Project)
	if !ok {
		actionRefuse(w, http.StatusNotFound, "That project is not in the project list.")
		return
	}
	if !v.found {
		actionRefuse(w, http.StatusConflict, "This project has no CI store on this PC, so there is nothing to change.")
		return
	}
	args, err := hsaction.Decode(id, env.Args)
	if err != nil {
		actionRefuse(w, http.StatusBadRequest, "The action's arguments are not right: "+err.Error())
		return
	}
	res := hsaction.Run(d.actionEnv(v.dir), id, args)
	res.Message = v.rdLines(res.Message)
	res.Error = v.rdLines(res.Error)
	if res.LogErr != nil && !errors.Is(res.LogErr, hsaction.ErrNoStore) {
		d.logf("homestretch: action %s: the activity log could not be written: %v", id, res.LogErr)
	}
	if res.LogErr != nil && !res.Changed && !errors.Is(res.LogErr, hsaction.ErrNoStore) {
		// Nothing changed, so the answer stands, but the person must know the attempt is not in the log.
		res.Message += " (The activity log could not be written.)"
		if res.Error != "" {
			res.Error += " (The activity log could not be written.)"
		}
	}
	if res.LogErr != nil && res.Changed && !errors.Is(res.LogErr, hsaction.ErrNoStore) {
		res.OK = false
		res.Error = "The change was saved, but the activity log could not be written."
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(res)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(res.Status.HTTPStatus())
	_ = json.NewEncoder(w).Encode(res)
}

// actionEnv is the Env of an action taken from the page.
func (d *Dash) actionEnv(dir string) hsaction.Env {
	return hsaction.Env{Dir: dir, Actor: activitylog.ActorUI, Person: d.person(), Now: d.Now, ConfirmNeedsBase: true}
}

func (d *Dash) logf(format string, args ...any) {
	if d.Logf != nil {
		d.Logf(format, args...)
		return
	}
	log.Printf(format, args...)
}

// logRefusal notes a request refused before it reached an action: why, the method, the path (quoted, so it cannot
// forge lines) and the client address. No header value (cookie, token, Origin) is written.
func (d *Dash) logRefusal(r *http.Request, why string) {
	path := r.URL.Path
	if len(path) > 120 {
		path = path[:120] + "..."
	}
	d.logf("homestretch: refused an action request: %s (%s %q from %s)", why, r.Method, path, r.RemoteAddr)
}

// person is the user name of this PC, the person behind the page.
func (d *Dash) person() string {
	if d.Person != "" {
		return d.Person
	}
	for _, k := range []string{"USERNAME", "USER"} {
		if s := strings.TrimSpace(os.Getenv(k)); s != "" {
			return s
		}
	}
	return ""
}

// rdLines redacts every line of a message.
func (v *ciView) rdLines(s string) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = v.rd(l)
	}
	return strings.Join(lines, "\n")
}

// ActionInfo names one action for the page.
type ActionInfo struct {
	ID      string `json:"id"`
	Summary string `json:"summary"`
	CLI     string `json:"cli"` // the same action on the command line
}

func actionList() []ActionInfo {
	var out []ActionInfo
	for _, a := range hsaction.All() {
		out = append(out, ActionInfo{ID: a.ID, Summary: a.Summary, CLI: a.CLI})
	}
	return out
}
