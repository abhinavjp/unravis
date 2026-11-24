package mcpsrv

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/mcp"
	"api-audit-explorer/internal/mcpaudit"
	"api-audit-explorer/internal/server"
)

// BL-349 tests on the real path: HTTP routes, the grants and aliases on a real SQLite file, the dashboard handler over the demo
// repository. Only the sources are demo data.

var aliasRE = regexp.MustCompile(`^ev1_[a-z2-7]{26}$`)

// evidenceHost enrols an evidence host for demo-west (with or without the identifiers grant) and runs one comparison. It returns the
// host and the result handle.
func (r *rig) evidenceHost(name string, identifiers bool) (host, string) {
	r.t.Helper()
	a := r.approval(TierEvidence)
	a.Sources = []string{"demo-west"}
	a.Identifiers = identifiers
	h := r.enroll(name, a)
	return h, r.compareHandle(h)
}

func (r *rig) compareHandle(h host) string {
	r.t.Helper()
	cmp := r.call(h, "audit_compare", r.compareFor("demo-west"))
	if !cmp.OK || cmp.IsError {
		r.t.Fatalf("compare: %.300s", cmp.Raw)
	}
	return cmp.env["resultId"].(string)
}

func rowsOf(t *testing.T, a reply) []map[string]any {
	t.Helper()
	if !a.OK || a.IsError {
		t.Fatalf("not ok: %d %.400s", a.Status, a.Raw)
	}
	var out []map[string]any
	for _, x := range a.env["rows"].([]any) {
		out = append(out, x.(map[string]any))
	}
	return out
}

func (r *rig) slowest(h host, id string, extra map[string]any) reply {
	args := map[string]any{"resultId": id, "limit": 3}
	for k, v := range extra {
		args[k] = v
	}
	return r.call(h, "audit_get_slowest", args)
}

func isGone(a reply) bool { return a.OK && a.IsError && errCode(a.env) == "result_expired" }

// A host without the identifiers grant gets an alias for each row and no record id; the person resolves the alias to the very record
// the identifiers host sees at the same place in the same query.
func TestRowsCarryAnAliasThatResolvesToTheRealRecord(t *testing.T) {
	r := newRig(t)
	plain, pid := r.evidenceHost("plain", false)
	truth, tid := r.evidenceHost("truth", true)
	a, b := rowsOf(t, r.slowest(plain, pid, nil)), rowsOf(t, r.slowest(truth, tid, nil))
	if len(a) != 3 || len(b) != 3 {
		t.Fatalf("%d and %d rows", len(a), len(b))
	}
	for i, row := range a {
		ref, _ := row["evidenceRef"].(string)
		if !aliasRE.MatchString(ref) {
			t.Fatalf("row %d evidenceRef %q", i, ref)
		}
		if _, has := row["id"]; has {
			t.Errorf("row %d of a host without identifiers carries the record id", i)
		}
		if b[i]["id"] == nil || b[i]["evidenceRef"] == nil {
			t.Fatalf("row %d of the identifiers host lacks id or evidenceRef: %v", i, b[i])
		}
		e, err := r.aliases.ResolveOwner(ctx, ref)
		if err != nil || e.RecordID != b[i]["id"] || e.SourceID != "demo-west" || e.ProfileID != "plain" {
			t.Fatalf("row %d resolves to %+v %v, the record is %v", i, e, err, b[i]["id"])
		}
		if strings.Contains(ref, e.RecordID) || strings.Contains(string(mustJSON(row)), e.RecordID) {
			t.Errorf("row %d shows the record id", i)
		}
	}
	// audit_get_requests: the same, for the rows of one group.
	groups := r.call(plain, "audit_get_groups", map[string]any{"resultId": pid, "limit": 1})
	key := groups.env["groups"].([]any)[0].(map[string]any)["key"].(string)
	req := rowsOf(t, r.call(plain, "audit_get_requests", map[string]any{"resultId": pid, "apiKey": key, "limit": 3}))
	if len(req) == 0 {
		t.Fatal("no request rows")
	}
	for _, row := range req {
		if ref, _ := row["evidenceRef"].(string); !aliasRE.MatchString(ref) || row["id"] != nil {
			t.Errorf("requests row: %v", row)
		}
	}
	// the same row again gets the same alias; no second row for it
	again := rowsOf(t, r.slowest(plain, pid, nil))
	for i := range a {
		if again[i]["evidenceRef"] != a[i]["evidenceRef"] {
			t.Errorf("row %d got another alias on the second read", i)
		}
	}
	if n, _ := r.db.CountAliases(ctx, "plain"); n != len(a)+len(req) {
		t.Errorf("%d alias rows for %d rows", n, len(a)+len(req))
	}
}

// The alias is bound to its host: another host cannot use it, and it ends when the host is revoked, reissued, stopped or removed.
func TestAliasBoundToProfileAndRevision(t *testing.T) {
	r := newRig(t)
	h1, id1 := r.evidenceHost("host-1", false)
	h2, _ := r.evidenceHost("host-2", false)
	ref := rowsOf(t, r.slowest(h1, id1, nil))[0]["evidenceRef"].(string)
	pr1, err := r.g.Authenticate(ctx, h1.header)
	if err != nil {
		t.Fatal(err)
	}
	pr2, err := r.g.Authenticate(ctx, h2.header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.aliases.Resolve(ctx, pr1, ref); err != nil {
		t.Fatalf("own alias: %v", err)
	}
	if _, err := r.aliases.Resolve(ctx, pr2, ref); err != ErrAliasNotFound {
		t.Errorf("another host's alias: %v", err)
	}
	// the same profile id at another revision
	if _, err := r.aliases.Resolve(ctx, Principal{ProfileID: "host-1", Rev: pr1.Rev + 1}, ref); err != ErrAliasNotFound {
		t.Errorf("another revision: %v", err)
	}
	// stop and resume: nothing from before is usable
	if err := r.g.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := r.aliases.ResolveOwner(ctx, ref); err != ErrAliasNotFound {
		t.Errorf("while stopped: %v", err)
	}
	if err := r.g.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := r.aliases.ResolveOwner(ctx, ref); err != ErrAliasNotFound {
		t.Errorf("after resume: %v", err)
	}
}

func TestAliasAfterRevokeAndReissue(t *testing.T) {
	for _, how := range []string{"revoke", "reissue", "revoke-remove-enroll-again"} {
		t.Run(how, func(t *testing.T) {
			r := newRig(t)
			h, id := r.evidenceHost("host-1", false)
			ref := rowsOf(t, r.slowest(h, id, nil))[0]["evidenceRef"].(string)
			pr, _ := r.g.Authenticate(ctx, h.header)
			if _, err := r.aliases.ResolveOwner(ctx, ref); err != nil {
				t.Fatal(err)
			}
			switch how {
			case "revoke":
				_, _ = r.g.Revoke(ctx, "host-1")
			case "reissue":
				_, _ = r.g.Reissue(ctx, "host-1")
			default:
				_, _ = r.g.Revoke(ctx, "host-1")
				if err := r.g.Remove(ctx, "host-1"); err != nil {
					t.Fatal(err)
				}
				h = r.enroll("host-1", r.approval(TierEvidence)) // the same id, enrolled again
			}
			if n, _ := r.db.CountAliases(ctx, "host-1"); n != 0 {
				t.Errorf("%d alias rows are left", n)
			}
			if _, err := r.aliases.ResolveOwner(ctx, ref); err != ErrAliasNotFound {
				t.Errorf("owner: %v", err)
			}
			if _, err := r.aliases.Resolve(ctx, pr, ref); err != ErrAliasNotFound {
				t.Errorf("host: %v", err)
			}
		})
	}
}

// Even if the delete after a revoke failed (a database error), the resolver checks the grant itself.
func TestAliasDeadWithoutTheDeleteHook(t *testing.T) {
	r := newRig(t)
	h, id := r.evidenceHost("host-1", false)
	ref := rowsOf(t, r.slowest(h, id, nil))[0]["evidenceRef"].(string)
	// end the profile's access directly in the store: no hook runs
	if _, err := r.db.Update(ctx, "host-1", func(p *mcpaudit.Profile) error { p.State, p.Rev = mcpaudit.StateRevoked, p.Rev+1; return nil }); err != nil {
		t.Fatal(err)
	}
	if n, _ := r.db.CountAliases(ctx, "host-1"); n == 0 {
		t.Fatalf("the test needs the rows to be still there: %d", n)
	}
	if _, err := r.aliases.ResolveOwner(ctx, ref); err != ErrAliasNotFound {
		t.Errorf("revoked without a hook: %v", err)
	}
}

// Each condition of the resolver on its own, with the delete hook out of the way (the rows are still there).
func TestResolverChecksEachGrantConditionAlone(t *testing.T) {
	setup := func(t *testing.T) (*rig, string) {
		r := newRig(t)
		h, id := r.evidenceHost("host-1", false)
		return r, rowsOf(t, r.slowest(h, id, nil))[0]["evidenceRef"].(string)
	}
	t.Run("state only", func(t *testing.T) {
		r, ref := setup(t)
		_, _ = r.db.Update(ctx, "host-1", func(p *mcpaudit.Profile) error { p.State = mcpaudit.StateRevoked; return nil })
		if _, err := r.aliases.ResolveOwner(ctx, ref); err != ErrAliasNotFound {
			t.Errorf("%v", err)
		}
	})
	t.Run("revision only", func(t *testing.T) {
		r, ref := setup(t)
		_, _ = r.db.Update(ctx, "host-1", func(p *mcpaudit.Profile) error { p.Rev++; return nil }) // approved again, say
		if _, err := r.aliases.ResolveOwner(ctx, ref); err != ErrAliasNotFound {
			t.Errorf("%v", err)
		}
	})
	t.Run("stopped only", func(t *testing.T) {
		r, ref := setup(t)
		r.g.stopped.Store(true) // the flag alone: the hooks do not run, so the rows are still there
		if _, err := r.aliases.ResolveOwner(ctx, ref); err != ErrAliasNotFound {
			t.Errorf("%v", err)
		}
		r.g.stopped.Store(false)
		if _, err := r.aliases.ResolveOwner(ctx, ref); err != nil {
			t.Errorf("after the flag is lifted: %v", err)
		}
	})
}

// The source scope is part of the binding: a source taken out of the grant ends the alias for it.
func TestAliasBoundToSourceScope(t *testing.T) {
	r := newRig(t)
	h, id := r.evidenceHost("host-1", false)
	ref := rowsOf(t, r.slowest(h, id, nil))[0]["evidenceRef"].(string)
	if _, err := r.db.Update(ctx, "host-1", func(p *mcpaudit.Profile) error { p.Sources = []string{"demo-east"}; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := r.aliases.ResolveOwner(ctx, ref); err != ErrAliasNotFound {
		t.Errorf("source out of scope: %v", err)
	}
	// and the minter of a bridge refuses a source outside its scope
	p, _ := r.db.Get(ctx, "host-1")
	mint := r.aliases.Minter(p)
	if _, err := mint(ctx, []mcp.EvidenceKey{{SourceID: "demo-west", RecordID: "abc"}}); err == nil {
		t.Error("a source outside the scope got an alias")
	}
}

// Expiry: 7 days after the last use; a resolve counts as a use; 30 days is the end whatever the use.
func TestAliasExpiry(t *testing.T) {
	r := newRig(t)
	h, id := r.evidenceHost("host-1", false)
	ref := rowsOf(t, r.slowest(h, id, nil))[0]["evidenceRef"].(string)
	made := r.clk.Now()
	r.clk.Add(6 * 24 * time.Hour)
	if _, err := r.aliases.ResolveOwner(ctx, ref); err != nil {
		t.Fatalf("day 6: %v", err)
	}
	// keep the host's own grant alive
	keepAlive := func() {
		_, _ = r.g.Renew(ctx, "host-1")
	}
	for day := 12; day <= 24; day += 6 {
		r.clk.Add(6 * 24 * time.Hour)
		keepAlive()
		if _, err := r.aliases.ResolveOwner(ctx, ref); err != nil {
			t.Fatalf("day %d: %v", day, err)
		}
	}
	if r.clk.Now().Sub(made) != 24*24*time.Hour {
		t.Fatal("clock arithmetic")
	}
	r.clk.Add(6 * 24 * time.Hour) // day 30: the cap
	keepAlive()
	if _, err := r.aliases.ResolveOwner(ctx, ref); err != ErrAliasNotFound {
		t.Errorf("past 30 days: %v", err)
	}
	// and an unused one ends after 7 days
	h2, id2 := r.evidenceHost("host-2", false)
	ref2 := rowsOf(t, r.slowest(h2, id2, nil))[0]["evidenceRef"].(string)
	r.clk.Add(8 * 24 * time.Hour)
	_, _ = r.g.Renew(ctx, "host-2")
	if _, err := r.aliases.ResolveOwner(ctx, ref2); err != ErrAliasNotFound {
		t.Errorf("unused for 8 days: %v", err)
	}
}

// A mint that loses to a revoke fails the call and leaves nothing behind (fail closed).
func TestMintFailsClosedAfterRevoke(t *testing.T) {
	r := newRig(t)
	h, _ := r.evidenceHost("host-1", false)
	pr, _ := r.g.Authenticate(ctx, h.header)
	p, _ := r.g.Scope(ctx, pr)
	mint := r.aliases.Minter(p)
	if _, err := r.g.Revoke(ctx, "host-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := mint(ctx, []mcp.EvidenceKey{{SourceID: "demo-west", RecordID: "0123456789abcdef01234567"}}); err == nil {
		t.Error("a revoked host got an alias")
	}
	if n, _ := r.db.CountAliases(ctx, ""); n != 0 {
		t.Errorf("%d aliases", n)
	}
}

// The resolver route of the app: the real dashboard handler, the session token, no echo, nothing for a host's own credentials.
func TestResolverRouteOnTheDashboard(t *testing.T) {
	r := newRig(t)
	server.SetEvidenceResolver(r.dash.inner, func(c context.Context, alias string) (server.EvidenceTarget, bool) {
		e, err := r.aliases.ResolveOwner(c, alias)
		if err != nil {
			return server.EvidenceTarget{}, false
		}
		return server.EvidenceTarget{SourceID: e.SourceID, RecordID: e.RecordID, Host: e.ProfileID, ExpiresAt: e.ExpiresAt}, true
	})
	h, id := r.evidenceHost("host-1", false)
	truth, tid := r.evidenceHost("truth", true)
	ref := rowsOf(t, r.slowest(h, id, nil))[0]["evidenceRef"].(string)
	want := rowsOf(t, r.slowest(truth, tid, nil))[0]["id"].(string)
	toolPaths := r.dash.hits() // what the tools asked for so far; the GETs below are the test's own
	get := func(path, token string) (int, string) {
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1"+path, nil)
		req.Host = "127.0.0.1"
		if token != "" {
			req.Header.Set("X-Audit-Token", token)
		}
		w := httptest.NewRecorder()
		r.dash.ServeHTTP(w, req)
		b, _ := io.ReadAll(w.Result().Body)
		return w.Code, string(b)
	}
	st, body := get("/api/mcp/evidence/"+ref, dashToken)
	var got server.EvidenceTarget
	if st != 200 || json.Unmarshal([]byte(body), &got) != nil || got.RecordID != want || got.SourceID != "demo-west" || got.Host != "host-1" {
		t.Fatalf("resolve: %d %s (want record %s)", st, body, want)
	}
	if st, _ := get("/api/mcp/evidence/"+ref, ""); st != http.StatusUnauthorized {
		t.Errorf("no token: %d", st)
	}
	if st, _ := get("/api/mcp/evidence/"+ref, "wrong-token-wrong-token-wrong-token-xx"); st != http.StatusUnauthorized {
		t.Errorf("wrong token: %d", st)
	}
	for _, bad := range []string{"ev1_aaaaaaaaaaaaaaaaaaaaaaaaaa", "ev1_", "x", strings.Repeat("ev1_", 100), "ev1_%00", "ev1_" + strings.Repeat("a", 26) + "/x"} {
		st, body := get("/api/mcp/evidence/"+bad, dashToken)
		if st != 404 || strings.Contains(body, "aaaa") || strings.Contains(body, "ev1_") {
			t.Errorf("%q: %d %s", bad, st, body)
		}
	}
	// no host can reach the route through the bridge of its grant: it is not one of the seven paths
	for _, p := range toolPaths {
		if strings.HasPrefix(p, "/api/mcp/") {
			t.Errorf("a tool asked the dashboard for %s", p)
		}
	}
	if _, err := r.g.Revoke(ctx, "host-1"); err != nil {
		t.Fatal(err)
	}
	if st, body := get("/api/mcp/evidence/"+ref, dashToken); st != 404 || strings.Contains(body, ref) {
		t.Errorf("after revoke: %d %s", st, body)
	}
}

// ---- cursors ----

func nextCursor(t *testing.T, a reply) string {
	t.Helper()
	c, _ := a.env["nextCursor"].(string)
	if c == "" {
		t.Fatalf("no nextCursor: %.400s", a.Raw)
	}
	return c
}

func TestSignedCursorPagesAndIsNotTheDashboardCursor(t *testing.T) {
	r := newRig(t)
	h, id := r.evidenceHost("host-1", false)
	p1 := r.slowest(h, id, map[string]any{"limit": 2})
	c1 := nextCursor(t, p1)
	if !strings.Contains(c1, ".") || len(c1) < 40 {
		t.Fatalf("cursor %q", c1)
	}
	p2 := r.slowest(h, id, map[string]any{"limit": 2, "cursor": c1})
	a, b := rowsOf(t, p1), rowsOf(t, p2)
	if a[0]["evidenceRef"] == b[0]["evidenceRef"] || a[1]["evidenceRef"] == b[0]["evidenceRef"] {
		t.Error("the second page repeats a row of the first")
	}
	// the dashboard's own cursor is inside, sealed: the host cannot read it and cannot send it to the dashboard's paging directly
	payload, _, _ := strings.Cut(c1, ".")
	raw, _ := base64.RawURLEncoding.DecodeString(payload)
	var inner struct{ X string }
	_ = json.Unmarshal(raw, &inner)
	if inner.X == "" || strings.Contains(string(raw), `"pos"`) {
		t.Fatalf("the cursor carries no sealed position: %s", raw)
	}
	if a := r.slowest(h, id, map[string]any{"cursor": inner.X}); !isGone(a) {
		t.Errorf("a raw position was accepted: %.300s", a.Raw)
	}
	// the sealed cursor is no echo of the host's input and the refusal repeats nothing
	bad := r.slowest(h, id, map[string]any{"cursor": "attacker-text-123"})
	if !isGone(bad) || bytes.Contains(bad.Raw, []byte("attacker-text-123")) {
		t.Errorf("bad cursor: %.300s", bad.Raw)
	}
}

func TestCursorTamperReplayAndBinding(t *testing.T) {
	r := newRig(t)
	h1, id1 := r.evidenceHost("host-1", false)
	h2, id2 := r.evidenceHost("host-2", false)
	c1 := nextCursor(t, r.slowest(h1, id1, map[string]any{"limit": 2}))
	if a := r.slowest(h1, id1, map[string]any{"cursor": c1}); !a.OK || a.IsError {
		t.Fatalf("a good cursor: %.300s", a.Raw)
	}
	payload, sig, _ := strings.Cut(c1, ".")
	flip := func(s string, i int) string {
		b := []byte(s)
		if b[i] == 'A' {
			b[i] = 'B'
		} else {
			b[i] = 'A'
		}
		return string(b)
	}
	cases := map[string]string{
		"payload changed":   flip(payload, 10) + "." + sig,
		"payload end":       flip(payload, len(payload)-1) + "." + sig,
		"signature changed": payload + "." + flip(sig, 0),
		"signature cut":     payload + "." + sig[:len(sig)-2],
		"no signature":      payload + ".",
		"only payload":      payload,
		"empty parts":       ".",
		"signature of none": payload + "." + base64.RawURLEncoding.EncodeToString(make([]byte, 16)),
	}
	for name, c := range cases {
		if a := r.slowest(h1, id1, map[string]any{"cursor": c}); !isGone(a) {
			t.Errorf("%s: %.200s", name, a.Raw)
		}
	}
	// a payload with a valid shape but changed fields and a signature of another payload
	var p map[string]any
	raw, _ := base64.RawURLEncoding.DecodeString(payload)
	_ = json.Unmarshal(raw, &p)
	p["pr"] = "host-2"
	forged, _ := json.Marshal(p)
	if a := r.slowest(h2, id2, map[string]any{"cursor": base64.RawURLEncoding.EncodeToString(forged) + "." + sig}); !isGone(a) {
		t.Errorf("a forged payload: %.200s", a.Raw)
	}
	// another host (same sources, same query) cannot use it
	if a := r.slowest(h2, id2, map[string]any{"cursor": c1}); !isGone(a) {
		t.Errorf("another host's cursor: %.200s", a.Raw)
	}
	// another tool cannot
	groups := r.call(h1, "audit_get_groups", map[string]any{"resultId": id1, "limit": 1})
	key := groups.env["groups"].([]any)[0].(map[string]any)["key"].(string)
	if a := r.call(h1, "audit_get_requests", map[string]any{"resultId": id1, "apiKey": key, "cursor": c1}); !isGone(a) {
		t.Errorf("a slowest cursor on audit_get_requests: %.200s", a.Raw)
	}
	// the same query with a floor that changes the ranking cannot
	if a := r.slowest(h1, id1, map[string]any{"cursor": c1, "minMs": 100}); !isGone(a) {
		t.Errorf("changed minMs: %.200s", a.Raw)
	}
}

func TestCursorReplayOnAChangedQuery(t *testing.T) {
	r := newRig(t)
	h, id := r.evidenceHost("host-1", false)
	c := nextCursor(t, r.slowest(h, id, map[string]any{"limit": 2}))
	// a new comparison over another window: a new handle, a different query
	args := r.compareFor("demo-west")
	args["query"] = withWindow(args["query"], r.now.Add(-30*time.Minute))
	cmp := r.call(h, "audit_compare", args)
	if !cmp.OK || cmp.IsError {
		t.Fatalf("%.300s", cmp.Raw)
	}
	if a := r.slowest(h, cmp.env["resultId"].(string), map[string]any{"cursor": c}); !isGone(a) {
		t.Errorf("a cursor of another query: %.300s", a.Raw)
	}
}

func TestCursorEndsWithTheGrantAndTheScope(t *testing.T) {
	r := newRig(t)
	h, _ := r.evidenceHost("host-1", true) // identifiers on
	args := r.activityFor("demo-west")
	args["limit"] = 2
	// activity has no handle behind its cursor, so what ends it here is the cursor's own binding
	c := nextCursor(t, r.call(h, "audit_get_activity", args))
	args["cursor"] = c
	if a := r.call(h, "audit_get_activity", args); !a.OK || a.IsError {
		t.Fatalf("%.200s", a.Raw)
	}
	// the identifiers grant ends (an hour): a cursor made under it is not paged on
	r.clk.Add(2 * time.Hour)
	_, _ = r.g.Renew(ctx, "host-1")
	if a := r.call(h, "audit_get_activity", args); !isGone(a) {
		t.Errorf("a cursor made with identifiers works without them: %.300s", a.Raw)
	}
	// reissue and approve again (a new revision, same id, same scope): the old cursor is dead for the new host
	fresh := nextCursor(t, r.call(h, "audit_get_activity", map[string]any{"sourceIds": []string{"demo-west"}, "start": r.now.Add(-time.Hour), "end": r.now, "limit": 2}))
	_, _ = r.g.Reissue(ctx, "host-1")
	h2 := r.enroll("host-1", r.approval(TierEvidence))
	args["cursor"] = fresh
	args["sourceIds"] = []string{"demo-west", "demo-east"}
	if a := r.call(h2, "audit_get_activity", args); !isGone(a) {
		t.Errorf("a cursor of the old revision (and another scope): %.300s", a.Raw)
	}
	args["sourceIds"] = []string{"demo-west"}
	if a := r.call(h2, "audit_get_activity", args); !isGone(a) {
		t.Errorf("a cursor of the old revision: %.300s", a.Raw)
	}
}

func TestCursorExpires(t *testing.T) {
	now := time.Now()
	clk := &clock{t: now}
	r := newRigClock(t, DefaultBudgetsOpen(), clk.Now)
	h, id := r.evidenceHost("host-1", false)
	c := nextCursor(t, r.slowest(h, id, map[string]any{"limit": 2}))
	clk.Add(CursorTTL - time.Second)
	if a := r.slowest(h, id, map[string]any{"cursor": c}); !a.OK || a.IsError {
		t.Fatalf("before expiry: %.200s", a.Raw)
	}
	clk.Add(2 * time.Second)
	if a := r.slowest(h, id, map[string]any{"cursor": c}); !a.OK || !a.IsError {
		t.Errorf("after expiry: %.200s", a.Raw) // the handle is over too (ten minutes): either way an expired answer
	}
}

// The activity cursor has no handle behind it: the window and sources are its query.
func TestActivityCursorSigned(t *testing.T) {
	r := newRig(t)
	h, _ := r.evidenceHost("host-1", false)
	args := r.activityFor("demo-west")
	args["limit"] = 2
	p1 := r.call(h, "audit_get_activity", args)
	c := nextCursor(t, p1)
	args["cursor"] = c
	if a := r.call(h, "audit_get_activity", args); !a.OK || a.IsError {
		t.Fatalf("%.300s", a.Raw)
	}
	args["end"] = r.now.Add(-time.Minute) // another window
	if a := r.call(h, "audit_get_activity", args); !isGone(a) {
		t.Errorf("changed window: %.300s", a.Raw)
	}
	args["end"], args["cursor"] = r.now, c[:len(c)-3]+"AAA"
	if a := r.call(h, "audit_get_activity", args); !isGone(a) {
		t.Errorf("changed signature: %.300s", a.Raw)
	}
}

// ---- handle bounds ----

func TestResultHandleIsSweptAfterItsTime(t *testing.T) {
	clk := &clock{t: time.Now()}
	r := newRigClock(t, DefaultBudgetsOpen(), clk.Now)
	h, id := r.evidenceHost("host-1", false)
	if r.backend.HeldBytes() == 0 {
		t.Fatal("no result is held after a comparison")
	}
	clk.Add(11 * time.Minute)
	r.backend.Sweep()
	if n := r.backend.HeldBytes(); n != 0 {
		t.Errorf("%d bytes are held after the ten minutes", n)
	}
	if a := r.call(h, "audit_get_groups", map[string]any{"resultId": id}); !isGone(a) {
		t.Errorf("an expired handle: %.200s", a.Raw)
	}
	if !r.backend.bridges["host-1"].b.ResultUsedAt().IsZero() {
		t.Error("the bridge still holds the expired result in memory")
	}
}

func TestResultHandlesOfAllHostsAreBounded(t *testing.T) {
	r := newRig(t)
	h1, id1 := r.evidenceHost("host-1", false)
	one := r.backend.HeldBytes()
	if one == 0 {
		t.Fatal("no bytes")
	}
	r.backend.maxAll = one + one/2 // room for one and a half results
	h2, id2 := r.evidenceHost("host-2", false)
	if n := r.backend.HeldBytes(); n > r.backend.maxAll {
		t.Errorf("%d bytes held, the bound is %d", n, r.backend.maxAll)
	}
	if a := r.call(h1, "audit_get_groups", map[string]any{"resultId": id1}); !isGone(a) {
		t.Errorf("the least recently used handle is still valid: %.200s", a.Raw)
	}
	if a := r.call(h2, "audit_get_groups", map[string]any{"resultId": id2}); !a.OK || a.IsError {
		t.Errorf("the newest handle was dropped: %.200s", a.Raw)
	}
}

func TestResultLargerThanTheCapIsNotKept(t *testing.T) {
	r := newRig(t)
	r.backend.maxOne = 100 // bytes: smaller than any real comparison
	a := r.enroll("host-1", r.approval(TierEvidence))
	cmp := r.call(a, "audit_compare", r.compareArgs())
	if !cmp.OK || !cmp.IsError || errCode(cmp.env) != "query_budget_exceeded" {
		t.Errorf("%.300s", cmp.Raw)
	}
	if n := r.backend.HeldBytes(); n != 0 {
		t.Errorf("%d bytes held", n)
	}
}

// A handle of one host is not valid for another, and ends when the grant does (the bridge dies with it).
func TestHandleOtherProfileAndAfterRevoke(t *testing.T) {
	r := newRig(t)
	h1, id1 := r.evidenceHost("host-1", false)
	h2, _ := r.evidenceHost("host-2", false)
	if a := r.call(h2, "audit_get_groups", map[string]any{"resultId": id1}); !isGone(a) {
		t.Errorf("another host's handle: %.200s", a.Raw)
	}
	_, _ = r.g.Revoke(ctx, "host-1")
	if r.backend.HeldBytes() == 0 { // host-2 still holds its own
		t.Error("the other host lost its result")
	}
	if a := r.call(h1, "audit_get_groups", map[string]any{"resultId": id1}); a.OK {
		t.Errorf("after revoke: %.200s", a.Raw)
	}
}

func withWindow(q any, start time.Time) any {
	qq := q.(audit.Query)
	qq.Start = start
	return qq
}

// DefaultBudgetsOpen is the budgets of newRig: none a test can reach.
func DefaultBudgetsOpen() Budgets {
	open := DefaultBudgets
	open.CompareRate, open.LiveRate, open.CumulativeBytes = 1_000_000, 1_000_000, 1<<40
	return open
}

// A page whose references cannot be made is not delivered with some rows cited and some not: the call fails, with fixed text.
func TestFailingMinterFailsTheCall(t *testing.T) {
	r := newRig(t)
	fail := func(context.Context, []mcp.EvidenceKey) ([]string, error) {
		return nil, errors.New("disk detail /secret/path")
	}
	short := func(_ context.Context, k []mcp.EvidenceKey) ([]string, error) { return make([]string, len(k)-1), nil }
	for name, m := range map[string]mcp.EvidenceMinter{"error": fail, "short answer": short} {
		b := mcp.NewLocal(r.dash, dashToken, mcp.WithSources([]string{"demo-west"}), mcp.WithHiddenRecordIDs(true), mcp.WithEvidenceRefs(m))
		body, isErr := b.CallEnvelope(ctx, "audit_compare", mustJSON(r.compareFor("demo-west")))
		if isErr {
			t.Fatalf("%s: %s", name, body)
		}
		var env map[string]any
		_ = json.Unmarshal(body, &env)
		body, isErr = b.CallEnvelope(ctx, "audit_get_slowest", mustJSON(map[string]any{"resultId": env["resultId"], "limit": 3}))
		if !isErr || bytes.Contains(body, []byte("secret")) || bytes.Contains(body, []byte(`"rows"`)) {
			t.Errorf("%s: isError=%v %.300s", name, isErr, body)
		}
	}
}

// Without the options (the stdio server, the command line) nothing changes: the raw id and the dashboard's cursor, as before.
func TestBridgeWithoutOptionsKeepsRawIDsAndCursors(t *testing.T) {
	r := newRig(t)
	b := mcp.NewLocal(r.dash, dashToken)
	body, _ := b.CallEnvelope(ctx, "audit_compare", mustJSON(r.compareFor("demo-west")))
	var env map[string]any
	_ = json.Unmarshal(body, &env)
	body, isErr := b.CallEnvelope(ctx, "audit_get_slowest", mustJSON(map[string]any{"resultId": env["resultId"], "limit": 1}))
	if isErr {
		t.Fatalf("%s", body)
	}
	var page map[string]any
	_ = json.Unmarshal(body, &page)
	row := page["rows"].([]any)[0].(map[string]any)
	if row["id"] == nil || row["evidenceRef"] != nil || strings.Contains(page["nextCursor"].(string), ".") {
		t.Errorf("row %v cursor %v", row, page["nextCursor"])
	}
}

// layers returns the cursor as the host can see it at every level of base64 and JSON: the text, the payload, and each string field of
// the payload decoded as base64 when it is.
func layers(t *testing.T, cursor string) []string {
	t.Helper()
	out := []string{cursor}
	payload, sig, _ := strings.Cut(cursor, ".")
	for _, part := range []string{payload, sig} {
		raw, err := base64.RawURLEncoding.DecodeString(part)
		if err != nil {
			t.Fatalf("cursor part is not base64url: %v", err)
		}
		out = append(out, string(raw))
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(out[1]), &fields); err != nil {
		t.Fatalf("payload is not JSON: %v", err)
	}
	for _, v := range fields {
		if str, ok := v.(string); ok {
			if raw, err := base64.RawURLEncoding.DecodeString(str); err == nil {
				out = append(out, string(raw))
			}
		}
	}
	return out
}

// M1 of the review of PR 269: the dashboard's cursor holds the raw id and the source of the last row, so a signed cursor that wraps it in
// the clear gives a host without the identifiers grant the id of a row it holds an evidenceRef for. No level of the cursor may show the
// id or the source id, on each of the three paged tools. This test fails when the position is put in the payload as text.
func TestCursorShowsNoRecordIDNorSourceID(t *testing.T) {
	r := newRig(t)
	plain, pid := r.evidenceHost("plain", false)
	truth, tid := r.evidenceHost("truth", true)
	// the ids a host with the identifiers grant sees for the same rows: all of them, and any slice of 6 or more characters
	var ids []string
	for _, row := range rowsOf(t, r.slowest(truth, tid, map[string]any{"limit": 20})) {
		ids = append(ids, row["id"].(string))
	}
	groups := r.call(plain, "audit_get_groups", map[string]any{"resultId": pid, "limit": 1})
	key := groups.env["groups"].([]any)[0].(map[string]any)["key"].(string)
	act := r.activityFor("demo-west")
	act["limit"] = 1
	cursors := map[string]string{
		"audit_get_slowest":  nextCursor(t, r.slowest(plain, pid, map[string]any{"limit": 1})),
		"audit_get_requests": nextCursor(t, r.call(plain, "audit_get_requests", map[string]any{"resultId": pid, "apiKey": key, "limit": 1})),
		"audit_get_activity": nextCursor(t, r.call(plain, "audit_get_activity", act)),
	}
	for tool, c := range cursors {
		for i, layer := range layers(t, c) {
			if strings.Contains(layer, "demo-west") || strings.Contains(layer, "demo-east") {
				t.Errorf("%s layer %d shows a source id", tool, i)
			}
			for _, id := range ids {
				for n := 0; n+8 <= len(id); n++ {
					if strings.Contains(layer, id[n:n+8]) {
						t.Fatalf("%s layer %d shows part of the record id %q", tool, i, id)
					}
				}
			}
		}
	}
}

// The sealed position opens only inside its own cursor: moved into another cursor of the same host (another tool, query or expiry),
// it does not open, even with a valid signature on the new payload.
func TestSealedPositionIsBoundToItsPayload(t *testing.T) {
	now := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	key := func(domain string, data []byte) []byte {
		m := hmac.New(sha256.New, []byte("k"))
		m.Write([]byte(domain))
		m.Write([]byte{0})
		m.Write(data)
		return m.Sum(nil)
	}
	c := &cursorCodec{sign: key, now: func() time.Time { return now }, profile: "h", rev: 1, binding: newBinding([]string{"a"}, TierEvidence, false, newGen())}
	cur, err := c.Seal("audit_get_slowest", "fp", "secret-position")
	if err != nil {
		t.Fatal(err)
	}
	payload, _, _ := strings.Cut(cur, ".")
	raw, _ := base64.RawURLEncoding.DecodeString(payload)
	if bytes.Contains(raw, []byte("secret-position")) {
		t.Fatal("the position is in the payload as text")
	}
	var p cursorPayload
	_ = json.Unmarshal(raw, &p)
	p.T, p.Q = "audit_get_requests", "fp2" // a forger with the signing key (the app itself) still cannot re-use a position elsewhere
	raw2, _ := json.Marshal(p)
	pl2 := base64.RawURLEncoding.EncodeToString(raw2)
	forged := pl2 + "." + base64.RawURLEncoding.EncodeToString(c.mac(pl2))
	if _, ok := c.Open("audit_get_requests", "fp2", forged); ok {
		t.Error("a sealed position opened in another payload")
	}
}

// S3 of the review of PR 269: Stop sets its flag, then deletes the aliases. A mint that ran its check before the flag and committed after
// the delete must not leave aliases that work again after Resume. The seam stops the grants between the stop check and the mint.
func TestMintRacingTheGlobalStopLeavesNothing(t *testing.T) {
	r := newRig(t)
	h, _ := r.evidenceHost("host-1", false)
	pr, _ := r.g.Authenticate(ctx, h.header)
	p, _ := r.g.Scope(ctx, pr)
	r.aliases.beforeMint = func() {
		if err := r.g.Stop(ctx); err != nil {
			t.Error(err)
		}
	}
	_, err := r.aliases.Minter(p)(ctx, []mcp.EvidenceKey{{SourceID: "demo-west", RecordID: "0123456789abcdef01234567"}})
	if err == nil {
		t.Fatal("a mint that lost to the stop was answered")
	}
	if n, _ := r.db.CountAliases(ctx, ""); n != 0 {
		t.Fatalf("%d aliases are left after the stop", n)
	}
	r.aliases.beforeMint = nil
	if err := r.g.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	if n, _ := r.db.CountAliases(ctx, ""); n != 0 {
		t.Errorf("%d aliases work again after Resume", n)
	}
	// and a mint while stopped is refused outright
	r.g.stopped.Store(true)
	if _, err := r.aliases.Minter(p)(ctx, []mcp.EvidenceKey{{SourceID: "demo-west", RecordID: "0123456789abcdef01234567"}}); err == nil {
		t.Error("a mint while stopped")
	}
	r.g.stopped.Store(false)
}

// S2 of the review of PR 269: the owner's shutdown and the master-key lock end the aliases of every host, not only of hosts with a call
// running. (Every host's grant stays: a host that calls again gets new aliases.)
func TestCancelAllEndsTheAliasesOfEveryHost(t *testing.T) {
	r := newRig(t)
	h1, id1 := r.evidenceHost("host-1", false)
	h2, id2 := r.evidenceHost("host-2", false)
	ref1 := rowsOf(t, r.slowest(h1, id1, nil))[0]["evidenceRef"].(string)
	ref2 := rowsOf(t, r.slowest(h2, id2, nil))[0]["evidenceRef"].(string)
	if n, _ := r.db.CountAliases(ctx, ""); n == 0 {
		t.Fatal("no aliases to end")
	}
	r.g.CancelAll() // nothing is running
	if n, _ := r.db.CountAliases(ctx, ""); n != 0 {
		t.Errorf("%d aliases survive CancelAll", n)
	}
	for _, ref := range []string{ref1, ref2} {
		if _, err := r.aliases.ResolveOwner(ctx, ref); err != ErrAliasNotFound {
			t.Errorf("%s: %v", ref, err)
		}
	}
	// the grants are untouched, and a new call mints new aliases
	if p, err := r.db.Get(ctx, "host-2"); err != nil || p.State != mcpaudit.StateActive {
		t.Fatalf("host-2 after CancelAll: %+v %v", p, err)
	}
	again := rowsOf(t, r.slowest(h2, r.compareHandle(h2), nil))[0]["evidenceRef"].(string)
	if _, err := r.aliases.ResolveOwner(ctx, again); err != nil {
		t.Errorf("a new alias after CancelAll: %v", err)
	}
}
