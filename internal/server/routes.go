package server

import (
	"net/http"
	"sort"
	"strings"

	"api-audit-explorer/internal/sessionproof"
)

// routeFunc serves one matched route. arg is the path parameter the route's
// matcher extracted (a source or saved-connection identifier), or "".
type routeFunc func(h *handler, w http.ResponseWriter, r *http.Request, arg string)

// route maps one path shape to the handlers of the methods it allows.
type route struct {
	// pattern names the route for documentation and tests; matching uses match.
	pattern string
	// match reports whether the (decoded) request path belongs to the route and
	// returns the path parameter handed to the handlers.
	match func(path string) (arg string, ok bool)
	// public routes skip the session token check.
	public  bool
	methods map[string]routeFunc
}

func exactPath(p string) func(string) (string, bool) {
	return func(path string) (string, bool) { return "", path == p }
}

func prefixPath(prefix string) func(string) (string, bool) {
	return func(path string) (string, bool) {
		if strings.HasPrefix(path, prefix) {
			return strings.TrimPrefix(path, prefix), true
		}
		return "", false
	}
}

// sourceSubpath matches /api/sources/{id}<suffix> and returns {id}.
func sourceSubpath(suffix string) func(string) (string, bool) {
	return func(path string) (string, bool) {
		if strings.HasPrefix(path, "/api/sources/") && strings.HasSuffix(path, suffix) {
			return strings.TrimSuffix(strings.TrimPrefix(path, "/api/sources/"), suffix), true
		}
		return "", false
	}
}

// ledgerPath matches the three /api/ledger shapes. Anything else under the
// prefix (an unknown action, extra segments) is not a route and answers 404.
func ledgerPath(shape string) func(string) (string, bool) {
	return func(path string) (string, bool) {
		if path != "/api/ledger" && !strings.HasPrefix(path, "/api/ledger/") {
			return "", false
		}
		rest := strings.TrimPrefix(strings.TrimPrefix(path, "/api/ledger"), "/")
		if rest == "" {
			return "", shape == "collection"
		}
		_, action, hasAction := strings.Cut(rest, "/")
		switch {
		case !hasAction:
			return "", shape == "item"
		case action == "reuse":
			return "", shape == "reuse"
		}
		return "", false
	}
}

// serveLedgerRoute hands a matched ledger request to serveLedger, which owns
// the identifier validation and handler dispatch for the three shapes.
func serveLedgerRoute(h *handler, w http.ResponseWriter, r *http.Request, _ string) {
	h.serveLedger(w, r)
}

func post(fn func(*handler, http.ResponseWriter, *http.Request)) map[string]routeFunc {
	return map[string]routeFunc{http.MethodPost: func(h *handler, w http.ResponseWriter, r *http.Request, _ string) { fn(h, w, r) }}
}

func get(fn func(*handler, http.ResponseWriter, *http.Request)) map[string]routeFunc {
	return map[string]routeFunc{http.MethodGet: func(h *handler, w http.ResponseWriter, r *http.Request, _ string) { fn(h, w, r) }}
}

// routes is the whole HTTP surface, checked in order; the first route whose
// matcher accepts the path owns it. A path that matches a route but not the
// request method answers 405 with an Allow header; a path that matches nothing
// answers 404 (API paths) or goes to the asset server (everything else).
var routes = []route{
	{pattern: "/", match: exactPath("/"), public: true, methods: map[string]routeFunc{
		http.MethodGet:  func(h *handler, w http.ResponseWriter, r *http.Request, _ string) { h.serveIndex(w, r) },
		http.MethodHead: func(h *handler, w http.ResponseWriter, r *http.Request, _ string) { h.serveIndex(w, r) },
	}},
	{pattern: launchPath, match: exactPath(launchPath), public: true, methods: map[string]routeFunc{
		http.MethodGet: func(h *handler, w http.ResponseWriter, r *http.Request, _ string) { h.serveLaunch(w, r) },
	}},
	{pattern: sessionproof.Path, match: exactPath(sessionproof.Path), public: true, methods: map[string]routeFunc{
		http.MethodGet: func(h *handler, w http.ResponseWriter, r *http.Request, _ string) { h.sessionProof(w, r) },
	}},
	{pattern: "/api/connections/recent", match: exactPath("/api/connections/recent"), methods: map[string]routeFunc{
		http.MethodGet: func(h *handler, w http.ResponseWriter, _ *http.Request, _ string) { h.listRecent(w) },
	}},
	{pattern: "/api/connections/recent/{id}", match: prefixPath("/api/connections/recent/"), methods: map[string]routeFunc{
		http.MethodDelete: func(h *handler, w http.ResponseWriter, r *http.Request, id string) { h.deleteRecent(w, r, id) },
	}},
	{pattern: "/api/connections/remember", match: exactPath("/api/connections/remember"), methods: post((*handler).rememberConnection)},
	{pattern: "/api/connections/test", match: exactPath("/api/connections/test"), methods: post((*handler).testConnection)},
	{pattern: "/api/info", match: exactPath("/api/info"), methods: map[string]routeFunc{
		http.MethodGet: func(h *handler, w http.ResponseWriter, _ *http.Request, _ string) { h.info(w) },
	}},
	{pattern: "/api/sources", match: exactPath("/api/sources"), methods: map[string]routeFunc{
		http.MethodGet:  func(h *handler, w http.ResponseWriter, r *http.Request, _ string) { h.sources(w, r) },
		http.MethodPost: func(h *handler, w http.ResponseWriter, r *http.Request, _ string) { h.addSource(w, r) },
	}},
	{pattern: "/api/sources/{id}/schema", match: sourceSubpath("/schema"), methods: map[string]routeFunc{
		http.MethodGet: func(h *handler, w http.ResponseWriter, r *http.Request, id string) { h.schema(w, r, id) },
	}},
	{pattern: "/api/sources/{id}/coverage", match: sourceSubpath("/coverage"), methods: map[string]routeFunc{
		http.MethodGet: func(h *handler, w http.ResponseWriter, r *http.Request, id string) { h.coverage(w, r, id) },
	}},
	{pattern: "/api/sources/{id}", match: prefixPath("/api/sources/"), methods: map[string]routeFunc{
		http.MethodDelete: func(h *handler, w http.ResponseWriter, r *http.Request, id string) { h.deleteSource(w, r, id) },
	}},
	{pattern: "/api/analyze", match: exactPath("/api/analyze"), methods: post((*handler).analyze)},
	{pattern: "/api/requests", match: exactPath("/api/requests"), methods: post((*handler).requests)},
	{pattern: "/api/activity", match: exactPath("/api/activity"), methods: post((*handler).activity)},
	{pattern: "/api/slowest", match: exactPath("/api/slowest"), methods: post((*handler).slowest)},
	{pattern: "/api/breakdown", match: exactPath("/api/breakdown"), methods: post((*handler).breakdown)},
	{pattern: "/api/daily", match: exactPath("/api/daily"), methods: get((*handler).daily)},
	{pattern: "/api/hourgrid", match: exactPath("/api/hourgrid"), methods: get((*handler).hourGrid)},
	{pattern: "/api/patterns", match: exactPath("/api/patterns"), methods: post((*handler).patterns)},
	{pattern: "/api/endpoint-daily", match: exactPath("/api/endpoint-daily"), methods: map[string]routeFunc{
		http.MethodGet: func(h *handler, w http.ResponseWriter, r *http.Request, _ string) { h.endpointDaily(w, r) },
	}},
	{pattern: "/api/export/preview", match: exactPath("/api/export/preview"), methods: post((*handler).exportPreview)},
	{pattern: "/api/export/confirm", match: exactPath("/api/export/confirm"), methods: post((*handler).exportConfirm)},
	{pattern: "/api/ledger", match: ledgerPath("collection"), methods: map[string]routeFunc{
		http.MethodGet: serveLedgerRoute, http.MethodPost: serveLedgerRoute,
	}},
	{pattern: "/api/ledger/{id}", match: ledgerPath("item"), methods: map[string]routeFunc{
		http.MethodPut: serveLedgerRoute, http.MethodDelete: serveLedgerRoute,
	}},
	{pattern: "/api/ledger/{id}/reuse", match: ledgerPath("reuse"), methods: map[string]routeFunc{
		http.MethodPost: serveLedgerRoute,
	}},
	{pattern: "/api/payload", match: exactPath("/api/payload"), methods: post((*handler).payload)},
	{pattern: "/api/record", match: exactPath("/api/record"), methods: post((*handler).record)},
	{pattern: "/api/export", match: exactPath("/api/export"), methods: post((*handler).export)},
	// Cases and hosts (importer wave). The case id is a single path segment;
	// each handler validates it before it is used for anything.
	{pattern: "/api/cases", match: exactPath("/api/cases"), methods: map[string]routeFunc{
		http.MethodGet:  func(h *handler, w http.ResponseWriter, r *http.Request, _ string) { h.listCases(w, r) },
		http.MethodPost: func(h *handler, w http.ResponseWriter, r *http.Request, _ string) { h.createCase(w, r) },
	}},
	{pattern: "/api/cases/{id}/close", match: caseSubpath("/close"), methods: map[string]routeFunc{
		http.MethodPost: func(h *handler, w http.ResponseWriter, r *http.Request, id string) { h.closeCase(w, r, id) },
	}},
	{pattern: "/api/cases/{id}/imports", match: caseSubpath("/imports"), methods: map[string]routeFunc{
		http.MethodGet:  func(h *handler, w http.ResponseWriter, r *http.Request, id string) { h.listImports(w, r, id) },
		http.MethodPost: func(h *handler, w http.ResponseWriter, r *http.Request, id string) { h.postImport(w, r, id) },
	}},
	{pattern: "/api/cases/{id}/imports/jobs/{job}", match: caseJobPath, methods: map[string]routeFunc{
		http.MethodGet:    func(h *handler, w http.ResponseWriter, r *http.Request, arg string) { h.getImportJob(w, r, arg) },
		http.MethodDelete: func(h *handler, w http.ResponseWriter, r *http.Request, arg string) { h.deleteImportJob(w, r, arg) },
	}},
	{pattern: "/api/cases/{id}/events", match: caseSubpath("/events"), methods: map[string]routeFunc{
		http.MethodGet: func(h *handler, w http.ResponseWriter, r *http.Request, id string) { h.caseEvents(w, r, id) },
	}},
	{pattern: "/api/cases/{id}/series", match: caseSubpath("/series"), methods: map[string]routeFunc{
		http.MethodGet: func(h *handler, w http.ResponseWriter, r *http.Request, id string) { h.caseSeries(w, r, id) },
	}},
	{pattern: "/api/cases/{id}/resource", match: caseSubpath("/resource"), methods: map[string]routeFunc{
		http.MethodGet: func(h *handler, w http.ResponseWriter, r *http.Request, id string) { h.caseResource(w, r, id) },
	}},
	{pattern: "/api/cases/{id}/collect", match: caseSubpath("/collect"), methods: map[string]routeFunc{
		http.MethodGet: func(h *handler, w http.ResponseWriter, r *http.Request, id string) { h.caseCollect(w, r, id) },
	}},
	{pattern: "/api/cases/{id}/timeline", match: caseSubpath("/timeline"), methods: map[string]routeFunc{
		http.MethodPost: func(h *handler, w http.ResponseWriter, r *http.Request, id string) { h.caseTimeline(w, r, id) },
	}},
	{pattern: "/api/cases/{id}", match: caseSubpath(""), methods: map[string]routeFunc{
		http.MethodDelete: func(h *handler, w http.ResponseWriter, r *http.Request, id string) { h.eraseCase(w, r, id) },
	}},
	{pattern: "/api/hosts", match: exactPath("/api/hosts"), methods: map[string]routeFunc{
		http.MethodGet: func(h *handler, w http.ResponseWriter, r *http.Request, _ string) { h.getHosts(w, r) },
		http.MethodPut: func(h *handler, w http.ResponseWriter, r *http.Request, _ string) { h.putHosts(w, r) },
	}},
	{pattern: "/api/importers", match: exactPath("/api/importers"), methods: get((*handler).listImporters)},
}

// lookup returns the first route that owns path.
func lookup(path string) (*route, string, bool) {
	for i := range routes {
		if arg, ok := routes[i].match(path); ok {
			return &routes[i], arg, true
		}
	}
	return nil, "", false
}

// allowHeader lists the route's methods in a stable order for the Allow header.
func (rt *route) allowHeader() string {
	names := make([]string, 0, len(rt.methods))
	for method := range rt.methods {
		names = append(names, method)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// dispatch serves the request through the table. The caller has already applied
// the host/origin check and security headers. Order: public routes (no token) ->
// token check for everything under /api/ -> method check -> handler; paths that
// match no route are 404 under /api/ and are otherwise served as assets.
func (h *handler) dispatch(w http.ResponseWriter, r *http.Request) {
	rt, arg, found := lookup(r.URL.Path)
	if found && rt.public {
		h.serveRoute(w, r, rt, arg)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/api/") {
		h.serveAsset(w, r)
		return
	}
	if !h.authorized(r) {
		writeError(w, http.StatusUnauthorized, "Invalid or missing session token.")
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "API route was not found.")
		return
	}
	h.serveRoute(w, r, rt, arg)
}

func (h *handler) serveRoute(w http.ResponseWriter, r *http.Request, rt *route, arg string) {
	fn, ok := rt.methods[r.Method]
	if !ok {
		w.Header().Set("Allow", rt.allowHeader())
		methodNotAllowed(w)
		return
	}
	fn(h, w, r, arg)
}
