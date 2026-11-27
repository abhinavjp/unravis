package releasedash

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"api-audit-explorer/internal/activitylog"
	"api-audit-explorer/internal/hsaction"
	"api-audit-explorer/internal/localci"
	"api-audit-explorer/internal/releasehold"
)

const fxOrigin = "http://127.0.0.1:80"

// actionToken reads the write channel's token the way the page does.
func (f *ciFixture) actionToken(t *testing.T) string {
	t.Helper()
	s := decode[struct {
		ActionToken string       `json:"actionToken"`
		Actions     []ActionInfo `json:"actions"`
	}](t, f.get(t, "/api/session"))
	if len(s.ActionToken) != 2*actionTokenLen || len(s.Actions) != len(hsaction.All()) {
		t.Fatalf("session: %+v", s)
	}
	return s.ActionToken
}

type actionReq struct {
	method, host, path, cookie, token, origin, fetchSite, ctype, body string
	noOrigin                                                          bool
}

func (f *ciFixture) post(t *testing.T, q actionReq) *httptest.ResponseRecorder {
	t.Helper()
	if q.method == "" {
		q.method = http.MethodPost
	}
	if q.host == "" {
		q.host = "127.0.0.1:80"
	}
	r := httptest.NewRequest(q.method, "http://"+q.host+q.path, strings.NewReader(q.body))
	if q.cookie != "" {
		r.AddCookie(&http.Cookie{Name: cookieName, Value: q.cookie}) //nolint:gosec // a request cookie in a test
	}
	if q.token != "" {
		r.Header.Set(tokenHeader, q.token)
	}
	if !q.noOrigin {
		o := q.origin
		if o == "" {
			o = fxOrigin
		}
		r.Header.Set("Origin", o)
	}
	if q.fetchSite != "" {
		r.Header.Set("Sec-Fetch-Site", q.fetchSite)
	}
	if q.ctype != "-" {
		ct := q.ctype
		if ct == "" {
			ct = "application/json"
		}
		r.Header.Set("Content-Type", ct)
	}
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, r)
	return w
}

func body(project string, args string) string {
	return `{"project":"` + project + `","args":` + args + `}`
}

func TestActionRejections(t *testing.T) {
	f := newCIFixture(t)
	tok := f.actionToken(t)
	good := actionReq{path: "/api/action/queue.pause", cookie: "tok", token: tok, body: body("unravis", "{}")}
	cases := []struct {
		name string
		mod  func(*actionReq)
		code int
	}{
		{"wrong host", func(q *actionReq) { q.host = "evil.example" }, 404},
		{"rebind host with port", func(q *actionReq) { q.host = "evil.example:80" }, 404},
		{"GET", func(q *actionReq) { q.method = http.MethodGet }, 405},
		{"PUT", func(q *actionReq) { q.method = http.MethodPut }, 405},
		{"no cookie", func(q *actionReq) { q.cookie = "" }, 401},
		{"bad cookie", func(q *actionReq) { q.cookie = "nope" }, 401},
		{"other origin", func(q *actionReq) { q.origin = "http://evil.example" }, 403},
		{"other port", func(q *actionReq) { q.origin = "http://127.0.0.1:8081" }, 403},
		{"https origin", func(q *actionReq) { q.origin = "https://127.0.0.1:80" }, 403},
		{"null origin", func(q *actionReq) { q.origin = "null" }, 403},
		{"no origin, cross-site", func(q *actionReq) { q.noOrigin, q.fetchSite = true, "cross-site" }, 403},
		{"no origin, same-site", func(q *actionReq) { q.noOrigin, q.fetchSite = true, "same-site" }, 403},
		{"no origin, no token", func(q *actionReq) { q.noOrigin, q.token = true, "" }, 403},
		{"missing token", func(q *actionReq) { q.token = "" }, 403},
		{"bad token", func(q *actionReq) { q.token = strings.Repeat("0", len(tok)) }, 403},
		{"session cookie as token", func(q *actionReq) { q.token = "tok" }, 403},
		{"no content type", func(q *actionReq) { q.ctype = "-" }, 415},
		{"form content type", func(q *actionReq) { q.ctype = "application/x-www-form-urlencoded" }, 415},
		{"text content type", func(q *actionReq) { q.ctype = "text/plain" }, 415},
		{"oversize body", func(q *actionReq) { q.body = body("unravis", `{"x":"`+strings.Repeat("a", maxActionBody)+`"}`) }, 413},
		{"unknown envelope field", func(q *actionReq) { q.body = `{"project":"unravis","args":{},"admin":true}` }, 400},
		{"unknown args field", func(q *actionReq) { q.body = body("unravis", `{"force":true}`) }, 400},
		{"two values", func(q *actionReq) { q.body = body("unravis", "{}") + "{}" }, 400},
		{"not json", func(q *actionReq) { q.body = "project=unravis" }, 400},
		{"unknown id", func(q *actionReq) { q.path = "/api/action/release.promote" }, 404},
		{"empty id", func(q *actionReq) { q.path = "/api/action/" }, 404},
		{"id with a path", func(q *actionReq) { q.path = "/api/action/queue.pause/x" }, 404},
		{"unknown project", func(q *actionReq) { q.body = body("other", "{}") }, 404},
	}
	for _, c := range cases {
		q := good
		c.mod(&q)
		w := f.post(t, q)
		if w.Code != c.code {
			t.Errorf("%s: %d, want %d: %s", c.name, w.Code, c.code, w.Body.String())
		}
		for _, h := range []string{"Access-Control-Allow-Origin", "Access-Control-Allow-Credentials", "Access-Control-Allow-Methods"} {
			if w.Header().Get(h) != "" {
				t.Errorf("%s: CORS header %s", c.name, h)
			}
		}
		if c.code != 404 || c.name == "unknown id" {
			if w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Content-Security-Policy") == "" {
				t.Errorf("%s: security headers missing", c.name)
			}
		}
	}
	if localci.IsPaused(f.dir) {
		t.Fatal("a refused request paused the queue")
	}
	// Transport refusals reach no action, so nothing is logged.
	es, _, _ := (activitylog.Log{Dir: f.dir}).Read(activitylog.Filter{})
	if len(es) != 0 {
		t.Fatalf("refused requests logged: %+v", es)
	}
	// The good request, and a request without Origin from a client that sends no Sec-Fetch-Site, work.
	if w := f.post(t, good); w.Code != 200 || !localci.IsPaused(f.dir) {
		t.Fatalf("good request: %d %s", w.Code, w.Body.String())
	}
	q := good
	q.noOrigin, q.fetchSite, q.path = true, "same-origin", "/api/action/queue.resume"
	if w := f.post(t, q); w.Code != 200 || localci.IsPaused(f.dir) {
		t.Fatalf("same-origin without Origin: %d %s", w.Code, w.Body.String())
	}
	// A POST to a read route stays refused.
	if c := f.do(t, "POST", "127.0.0.1:80", "/api/queue?project=unravis", "tok").Code; c != 405 {
		t.Fatalf("POST /api/queue: %d", c)
	}
	// The start link is not honoured on the action path.
	q = good
	q.path, q.cookie = "/api/action/queue.pause?t=tok", ""
	if w := f.post(t, q); w.Code != 401 {
		t.Fatalf("?t= on the action path: %d", w.Code)
	}
}

func TestEveryActionIsCallableOverHTTP(t *testing.T) {
	f := newCIFixture(t)
	f.d.Person = "pat"
	tok := f.actionToken(t)
	q, err := localci.OpenQueue(localci.QueueDir(f.dir))
	must(t, err)
	all, err := q.List()
	must(t, err)
	queued := ""
	for _, r := range all {
		if r.State == localci.StateQueued {
			queued = r.ID
		}
	}
	if queued == "" {
		t.Fatal("the fixture has no queued request")
	}
	holdID := func() string {
		hs, err := (releasehold.Store{Dir: f.dir}).List()
		must(t, err)
		if len(hs) == 0 {
			t.Fatal("no hold")
		}
		return hs[len(hs)-1].ID
	}
	// One row per action, in an order where each finds what it needs. want is the HTTP status.
	rows := []struct {
		id   string
		args func() string
		want int
	}{
		{"queue.top", func() string { return `{"id":"` + queued + `"}` }, 200},
		{"queue.pause", func() string { return `{}` }, 200},
		{"queue.resume", func() string { return `{}` }, 200},
		{"queue.cancel", func() string { return `{"id":"` + queued + `"}` }, 200},
		{"queue.retry", func() string { return `{"run":"` + f.passID + `"}` }, 200},
		{"machines.allow", func() string { return `{"name":"a1","project":"o/r"}` }, 200},
		{"machines.deny", func() string { return `{"name":"a1","project":"o/r","confirm":true}` }, 200},
		{"policy.set", func() string { return `{"project":"o/r","mode":"any"}` }, 200},
		{"machines.drain", func() string { return `{"name":"a1"}` }, 200},
		{"machines.resume", func() string { return `{"name":"a1"}` }, 200},
		{"hold.add", func() string { return `{"project":"unravis","until":"2h","reason":"freeze"}` }, 200},
		{"hold.lift", func() string { return `{"id":"` + holdID() + `","reason":"done"}` }, 200},
	}
	covered := map[string]bool{}
	for _, row := range rows {
		covered[row.id] = true
		w := f.post(t, actionReq{path: "/api/action/" + row.id, cookie: "tok", token: tok, body: body("unravis", row.args())})
		var res struct {
			OK      bool   `json:"ok"`
			Message string `json:"message"`
			Error   string `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
			t.Fatalf("%s: %v %s", row.id, err, w.Body.String())
		}
		if w.Code != row.want || res.OK != (row.want == 200) || res.Message == "" {
			t.Errorf("%s: %d %s", row.id, w.Code, w.Body.String())
		}
		if strings.Contains(res.Message, f.dir) {
			t.Errorf("%s: the message shows the folder: %s", row.id, res.Message)
		}
	}
	for _, a := range hsaction.All() {
		if !covered[a.ID] {
			t.Errorf("%s has no HTTP row", a.ID)
		}
	}
	es, _, err := (activitylog.Log{Dir: f.dir}).Read(activitylog.Filter{})
	must(t, err)
	logged := map[string]bool{}
	for _, e := range es {
		if e.Actor != activitylog.ActorUI || e.Person != "pat" {
			t.Errorf("entry %+v: actor or person", e)
		}
		logged[e.Action] = true
	}
	for _, a := range hsaction.All() {
		if !logged[a.ID] {
			t.Errorf("%s left no activity entry", a.ID)
		}
	}
	// The read API shows the outcome.
	act := decode[HSActivity](t, f.get(t, "/api/activity?project=unravis"))
	if len(act.Entries) != len(es) || act.Entries[0].Outcome != "done" {
		t.Fatalf("activity: %+v", act)
	}
}

func TestActionStrandingPolicyEditNeedsConfirmOverHTTP(t *testing.T) {
	f := newCIFixture(t)
	tok := f.actionToken(t)
	send := func(args string) (*httptest.ResponseRecorder, hsaction.Result) {
		w := f.post(t, actionReq{path: "/api/action/policy.set", cookie: "tok", token: tok, body: body("unravis", args)})
		var res hsaction.Result
		must(t, json.Unmarshal(w.Body.Bytes(), &res))
		return w, res
	}
	w, res := send(`{"project":"o/r","mode":"only","machines":["m9"],"dryRun":true}`)
	// As `policy set --dry-run` (exit 1): a dry run that would strand runs answers refused, with the impact.
	if w.Code != 409 || res.OK || res.Impact == nil || len(res.Impact.Stranded) == 0 || !res.DryRun {
		t.Fatalf("dry run: %d %s", w.Code, w.Body.String())
	}
	if es, _, _ := (activitylog.Log{Dir: f.dir}).Read(activitylog.Filter{}); len(es) != 0 {
		t.Fatalf("a dry run was logged: %+v", es)
	}
	if w, res = send(`{"project":"o/r","mode":"any","dryRun":true}`); w.Code != 200 || !res.OK || !res.DryRun {
		t.Fatalf("harmless dry run: %d %s", w.Code, w.Body.String())
	}
	w, res = send(`{"project":"o/r","mode":"only","machines":["m9"]}`)
	if w.Code != 409 || res.OK || res.Impact == nil || !strings.Contains(res.Message, "Confirm") {
		t.Fatalf("not refused: %d %s", w.Code, w.Body.String())
	}
	if p, err := localci.LoadPolicy(f.dir); err != nil || p.Projects["o/r"].Mode == localci.PolicyOnly {
		t.Fatalf("the refused edit was written: %v", err)
	}
	w, res = send(`{"project":"o/r","mode":"only","machines":["m9"],"confirm":true}`)
	if w.Code != 200 || !res.OK {
		t.Fatalf("confirm: %d %s", w.Code, w.Body.String())
	}
	es, _, err := (activitylog.Log{Dir: f.dir}).Read(activitylog.Filter{})
	must(t, err)
	if len(es) != 2 || es[0].Outcome != "refused" || es[1].Outcome != "done" {
		t.Fatalf("entries: %+v", es)
	}
}
