package server

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/history"
)

const (
	maxRequestBytes  = 1 << 20
	maxResponseBytes = 16 << 20
	maxExportRows    = 10000
	requestTimeout   = audit.OperationTimeout
)

type handler struct {
	repo    audit.Repository
	assets  fs.FS
	index   []byte
	token   string
	demo    bool
	history savedHistory
}

type savedHistory interface {
	List() ([]history.Recent, error)
	Remember(string, audit.ConnectionInput, string, bool) (history.Recent, error)
	Resolve(audit.ConnectionInput) (audit.ConnectionInput, error)
	Delete(string) error
}

type Option func(*handler)

func WithHistory(value savedHistory) Option { return func(h *handler) { h.history = value } }

func New(repo audit.Repository, assets fs.FS, token string, demo bool, options ...Option) http.Handler {
	h := &handler{repo: repo, assets: assets, token: token, demo: demo}
	for _, option := range options {
		if option != nil {
			option(h)
		}
	}
	if assets != nil {
		if data, err := fs.ReadFile(assets, "index.html"); err == nil {
			h.index = []byte(strings.Replace(string(data), `name="audit-session" content=""`, `name="audit-session" content="`+html.EscapeString(token)+`"`, 1))
		}
	}
	return h
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setSecurityHeaders(w)
	if !allowedHost(r.Host) || !allowedOrigin(r.Header.Get("Origin"), r.Host) {
		writeError(w, http.StatusForbidden, "Local request origin is not allowed.")
		return
	}
	if r.URL.Path == "/" {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
			return
		}
		h.serveIndex(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		if !h.authorized(r) {
			writeError(w, http.StatusUnauthorized, "Invalid or missing session token.")
			return
		}
		h.serveAPI(w, r)
		return
	}
	h.serveAsset(w, r)
}

func setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
}

func (h *handler) authorized(r *http.Request) bool {
	got := r.Header.Get("X-Audit-Token")
	if h.token == "" || len(got) != len(h.token) || len(got) > 4096 {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(h.token)) == 1
}

func (h *handler) serveIndex(w http.ResponseWriter, r *http.Request) {
	if len(h.index) == 0 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(h.index)
	}
}

func (h *handler) serveAsset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
		return
	}
	if h.assets == nil {
		http.NotFound(w, r)
		return
	}
	clean := path.Clean("/" + r.URL.Path)
	if clean == "/" || !fs.ValidPath(strings.TrimPrefix(clean, "/")) {
		http.NotFound(w, r)
		return
	}
	http.FileServer(http.FS(h.assets)).ServeHTTP(w, r)
}

func (h *handler) serveAPI(w http.ResponseWriter, r *http.Request) {
	route := r.URL.Path
	if route == "/api/connections/recent" {
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		h.listRecent(w)
		return
	}
	if strings.HasPrefix(route, "/api/connections/recent/") {
		if r.Method != http.MethodDelete {
			methodNotAllowed(w)
			return
		}
		h.deleteRecent(w, r, strings.TrimPrefix(route, "/api/connections/recent/"))
		return
	}
	if route == "/api/connections/remember" {
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		h.rememberConnection(w, r)
		return
	}
	if route == "/api/connections/test" {
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		h.testConnection(w, r)
		return
	}
	if route == "/api/info" {
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		h.info(w)
		return
	}
	if route == "/api/sources" {
		switch r.Method {
		case http.MethodGet:
			h.sources(w, r)
		case http.MethodPost:
			h.addSource(w, r)
		default:
			methodNotAllowed(w)
		}
		return
	}
	if strings.HasPrefix(route, "/api/sources/") && strings.HasSuffix(route, "/schema") {
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		h.schema(w, r, strings.TrimSuffix(strings.TrimPrefix(route, "/api/sources/"), "/schema"))
		return
	}
	if strings.HasPrefix(route, "/api/sources/") && r.Method == http.MethodDelete {
		h.deleteSource(w, r, strings.TrimPrefix(route, "/api/sources/"))
		return
	}
	if route == "/api/analyze" && r.Method == http.MethodPost {
		h.analyze(w, r)
		return
	}
	if route == "/api/requests" && r.Method == http.MethodPost {
		h.requests(w, r)
		return
	}
	if route == "/api/activity" && r.Method == http.MethodPost {
		h.activity(w, r)
		return
	}
	if route == "/api/slowest" && r.Method == http.MethodPost {
		h.slowest(w, r)
		return
	}
	if route == "/api/breakdown" && r.Method == http.MethodPost {
		h.breakdown(w, r)
		return
	}
	if route == "/api/payload" && r.Method == http.MethodPost {
		h.payload(w, r)
		return
	}
	if route == "/api/record" && r.Method == http.MethodPost {
		h.record(w, r)
		return
	}
	if route == "/api/export" && r.Method == http.MethodPost {
		h.export(w, r)
		return
	}
	writeError(w, http.StatusNotFound, "API route was not found.")
}

func (h *handler) testConnection(w http.ResponseWriter, r *http.Request) {
	var input audit.ConnectionInput
	if !decodeJSON(w, r, &input) {
		return
	}
	resolved, ok := h.resolveConnection(w, input)
	if !ok {
		return
	}
	input = resolved
	tester, ok := h.repo.(audit.ConnectionTester)
	if !ok {
		writeError(w, http.StatusBadRequest, "Connection testing is unavailable for this repository.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	result, err := tester.TestConnection(ctx, input)
	if err != nil {
		if writeConnectionError(w, err) {
			return
		}
		if errors.Is(err, audit.ErrInvalidInput) {
			writeError(w, http.StatusBadRequest, "Could not connect or list databases. Check the URI, credentials, authentication database, and permissions.")
			return
		}
		writeRepoError(w, err)
		return
	}
	if result.Databases == nil {
		result.Databases = []string{}
	}
	writeJSON(w, http.StatusOK, result, maxResponseBytes)
}

func (h *handler) listRecent(w http.ResponseWriter) {
	if h.history == nil {
		writeError(w, http.StatusServiceUnavailable, "Saved connections are unavailable on this platform.")
		return
	}
	items, err := h.history.List()
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "Saved connections could not be read. The saved file was left unchanged.")
		return
	}
	if items == nil {
		items = []history.Recent{}
	}
	writeJSON(w, http.StatusOK, items, maxResponseBytes)
}

func (h *handler) rememberConnection(w http.ResponseWriter, r *http.Request) {
	if h.history == nil {
		writeError(w, http.StatusServiceUnavailable, "Saved connections are unavailable on this platform.")
		return
	}
	var input struct {
		Label            string                `json:"label"`
		Connection       audit.ConnectionInput `json:"connection"`
		Collection       string                `json:"collection"`
		RememberPassword bool                  `json:"rememberPassword"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	savedConnectionID := input.Connection.SavedConnectionID
	connection, ok := h.resolveConnection(w, input.Connection)
	if !ok {
		return
	}
	validator, available := h.repo.(audit.ConnectionValidator)
	if !available {
		writeError(w, http.StatusServiceUnavailable, "Offline connection settings validation is unavailable.")
		return
	}
	if err := validator.ValidateConnectionSettings(connection); err != nil {
		if writeConnectionError(w, err) {
			return
		}
		if errors.Is(err, audit.ErrInvalidInput) {
			writeError(w, http.StatusBadRequest, "Connection settings are invalid. Check the URI, TLS settings, credentials, and authentication database.")
			return
		}
		writeRepoError(w, err)
		return
	}
	connection.SavedConnectionID = savedConnectionID
	item, err := h.history.Remember(input.Label, connection, input.Collection, input.RememberPassword)
	if err != nil {
		if errors.Is(err, audit.ErrInvalidInput) || errors.Is(err, history.ErrNotFound) {
			writeError(w, http.StatusBadRequest, "Saved connection details are invalid.")
			return
		}
		writeError(w, http.StatusServiceUnavailable, "Saved connection could not be written. The existing history was left unchanged.")
		return
	}
	writeJSON(w, http.StatusOK, item, maxResponseBytes)
}

func writeConnectionError(w http.ResponseWriter, err error) bool {
	var connectionError *audit.ConnectionError
	if !errors.As(err, &connectionError) || connectionError == nil {
		return false
	}
	writeJSON(w, http.StatusBadRequest, struct {
		Error string `json:"error"`
		Code  string `json:"code"`
		Stage string `json:"stage"`
	}{Error: connectionError.Message, Code: connectionError.Code, Stage: connectionError.Stage}, 4096)
	return true
}

func (h *handler) deleteRecent(w http.ResponseWriter, r *http.Request, id string) {
	decoded, err := url.PathUnescape(id)
	if err != nil || decoded == "" || strings.Contains(decoded, "/") {
		writeError(w, http.StatusBadRequest, "Invalid saved connection identifier.")
		return
	}
	if h.history == nil {
		writeError(w, http.StatusServiceUnavailable, "Saved connections are unavailable on this platform.")
		return
	}
	if err := h.history.Delete(decoded); err != nil {
		if errors.Is(err, history.ErrNotFound) {
			writeError(w, http.StatusNotFound, "Saved connection was not found.")
			return
		}
		writeError(w, http.StatusServiceUnavailable, "Saved connection could not be removed.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) resolveConnection(w http.ResponseWriter, input audit.ConnectionInput) (audit.ConnectionInput, bool) {
	if input.SavedConnectionID == "" {
		return input, true
	}
	if h.history == nil {
		writeError(w, http.StatusServiceUnavailable, "Saved connections are unavailable on this platform.")
		return audit.ConnectionInput{}, false
	}
	resolved, err := h.history.Resolve(input)
	if err != nil {
		if errors.Is(err, history.ErrCorrupt) || errors.Is(err, history.ErrUnavailable) {
			writeError(w, http.StatusServiceUnavailable, "Saved connections could not be read. The saved file was left unchanged.")
			return audit.ConnectionInput{}, false
		}
		writeError(w, http.StatusBadRequest, "Saved connection no longer matches these settings. Select it again or enter the password.")
		return audit.ConnectionInput{}, false
	}
	return resolved, true
}

func methodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
}

func (h *handler) info(w http.ResponseWriter) {
	now := time.Now().UTC()
	if h.demo {
		if clock, ok := h.repo.(interface{ DemoTime() time.Time }); ok {
			now = clock.DemoTime().UTC()
		}
	}
	writeJSON(w, http.StatusOK, struct {
		Demo   bool `json:"demo"`
		Limits struct {
			MaxSources     int `json:"maxSources"`
			MaxPageSize    int `json:"maxPageSize"`
			MaxSummaryRows int `json:"maxSummaryRows"`
			MaxTimeBuckets int `json:"maxTimeBuckets"`
		} `json:"limits"`
		Now time.Time `json:"now"`
	}{Demo: h.demo, Limits: struct {
		MaxSources     int `json:"maxSources"`
		MaxPageSize    int `json:"maxPageSize"`
		MaxSummaryRows int `json:"maxSummaryRows"`
		MaxTimeBuckets int `json:"maxTimeBuckets"`
	}{audit.MaxSources, audit.MaxPageSize, audit.MaxSummaryRows, audit.MaxTimeBuckets}, Now: now}, maxResponseBytes)
}

func (h *handler) sources(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	items, err := h.repo.Sources(ctx)
	if err != nil {
		writeRepoError(w, err)
		return
	}
	if items == nil {
		items = []audit.Source{}
	}
	writeJSON(w, http.StatusOK, items, maxResponseBytes)
}

func (h *handler) addSource(w http.ResponseWriter, r *http.Request) {
	var input audit.SourceInput
	if !decodeJSON(w, r, &input) {
		return
	}
	resolved, ok := h.resolveConnection(w, audit.ConnectionInput{URI: input.URI, Username: input.Username, Password: input.Password, AuthSource: input.AuthSource, TLSMode: input.TLSMode, CACertificate: input.CACertificate, SavedConnectionID: input.SavedConnectionID})
	if !ok {
		return
	}
	input.URI, input.Username, input.Password, input.AuthSource = resolved.URI, resolved.Username, resolved.Password, resolved.AuthSource
	input.TLSMode, input.CACertificate, input.SavedConnectionID = resolved.TLSMode, resolved.CACertificate, ""
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	item, err := h.repo.AddSource(ctx, input)
	if err != nil {
		writeRepoError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, item, maxResponseBytes)
}

func (h *handler) deleteSource(w http.ResponseWriter, r *http.Request, id string) {
	decoded, err := url.PathUnescape(id)
	if err != nil || decoded == "" || strings.Contains(decoded, "/") {
		writeError(w, http.StatusBadRequest, "Invalid source identifier.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	if err := h.repo.DeleteSource(ctx, decoded); err != nil {
		writeRepoError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) analyze(w http.ResponseWriter, r *http.Request) {
	var q audit.Query
	if !decodeJSON(w, r, &q) {
		return
	}
	applyQueryDefaults(&q)
	if err := audit.ValidateQuery(q); err != nil {
		writeRepoError(w, fmt.Errorf("%w: invalid query", audit.ErrInvalidInput))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	analysis, err := h.repo.Analyze(ctx, q)
	if err != nil {
		writeRepoError(w, err)
		return
	}
	normalizeAnalysis(&analysis)
	writeJSON(w, http.StatusOK, analysis, maxResponseBytes)
}

func applyQueryDefaults(q *audit.Query) {
	if q.Kind == "" {
		q.Kind = "synchronous"
	}
	if q.Outcome == "" {
		q.Outcome = "all"
	}
	if q.Metric == "" {
		q.Metric = "total"
	}
}

func normalizeAnalysis(value *audit.Analysis) {
	value.Query.Start, value.Query.End = value.Query.Start.UTC(), value.Query.End.UTC()
	value.Query.BaselineStart, value.Query.BaselineEnd = value.Query.BaselineStart.UTC(), value.Query.BaselineEnd.UTC()
	if value.APIs == nil {
		value.APIs = []audit.Comparison{}
	}
	if value.Timeline == nil {
		value.Timeline = []audit.Series{}
	}
	if value.Sources == nil {
		value.Sources = []audit.SourceResult{}
	}
	if value.Warnings == nil {
		value.Warnings = []string{}
	}
	for i := range value.Sources {
		normalizeSourceResult(&value.Sources[i])
	}
	for i := range value.APIs {
		if value.APIs[i].Current.Histogram == nil {
			value.APIs[i].Current.Histogram = []audit.BinCount{}
		}
		if value.APIs[i].Reference.Histogram == nil {
			value.APIs[i].Reference.Histogram = []audit.BinCount{}
		}
	}
	for i := range value.Timeline {
		if value.Timeline[i].Points == nil {
			value.Timeline[i].Points = []audit.Point{}
		}
	}
}

func (h *handler) requests(w http.ResponseWriter, r *http.Request) {
	var input audit.PageInput
	if !decodeJSON(w, r, &input) {
		return
	}
	applyQueryDefaults(&input.Query)
	if err := audit.ValidateQuery(input.Query); err != nil {
		writeRepoError(w, fmt.Errorf("%w: invalid query", audit.ErrInvalidInput))
		return
	}
	if input.Limit < 1 || input.Limit > audit.MaxPageSize {
		writeRepoError(w, audit.ErrLimit)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	page, err := h.repo.Requests(ctx, input)
	if err != nil {
		writeRepoError(w, err)
		return
	}
	normalizePage(&page)
	writeJSON(w, http.StatusOK, page, maxResponseBytes)
}

func normalizeSourceResult(source *audit.SourceResult) {
	if source.OK {
		source.Error, source.ErrorCode = "", ""
		return
	}
	switch source.ErrorCode {
	case "capacity_timeout", "query_cancelled", "source_unavailable", "query_timeout", "permission_denied", "authentication_failed", "query_memory_limit", "connection_failed", "query_failed":
	default:
		source.ErrorCode = "query_failed"
	}
	source.Error = audit.SourceQueryErrorMessage(source.ErrorCode)
}

func normalizePage(value *audit.Page) {
	if value.Rows == nil {
		value.Rows = []audit.RequestRecord{}
	}
	if value.Sources == nil {
		value.Sources = []audit.SourceResult{}
	}
	for i := range value.Sources {
		normalizeSourceResult(&value.Sources[i])
	}
	for i := range value.Rows {
		normalizeRowTimes(&value.Rows[i])
	}
}

// normalizeRowTimes renders every timestamp on a row in UTC.
func normalizeRowTimes(row *audit.RequestRecord) {
	for _, field := range []**time.Time{&row.InitiatedAt, &row.CompletedAt, &row.CreatedOn, &row.UpdatedOn} {
		if *field != nil {
			utc := (*field).UTC()
			*field = &utc
		}
	}
}

func (h *handler) record(w http.ResponseWriter, r *http.Request) {
	var input audit.RecordInput
	if !decodeJSON(w, r, &input) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	value, err := h.repo.Record(ctx, input)
	if err != nil {
		writeRepoError(w, err)
		return
	}
	normalizeRowTimes(&value.RequestRecord)
	writeJSON(w, http.StatusOK, value, audit.MaxPayloadBytes+128*1024)
}

func (h *handler) payload(w http.ResponseWriter, r *http.Request) {
	var input audit.PayloadInput
	if !decodeJSON(w, r, &input) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	value, err := h.repo.Payload(ctx, input)
	if err != nil {
		writeRepoError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, value, audit.MaxPayloadBytes+1024)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json.")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "Request body exceeds 1 MiB.")
		} else {
			writeError(w, http.StatusBadRequest, "Request body is invalid JSON.")
		}
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		writeError(w, http.StatusBadRequest, "Request body must contain one JSON value.")
		return false
	}
	return true
}

func writeRepoError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, audit.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, "Request validation failed.")
	case errors.Is(err, audit.ErrNotFound):
		writeError(w, http.StatusNotFound, "Source or request was not found.")
	case errors.Is(err, audit.ErrLegacyScanLimit):
		writeError(w, http.StatusRequestEntityTooLarge, "MongoDB 3.4 metadata scan exceeded its safety limit (100,000 records, 16 MiB, or a field size limit). Narrow the date range or API filters; oversized metadata fields may require a different range.")
	case errors.Is(err, audit.ErrAPIGroupLimit):
		writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("This comparison contains more than %d API groups. A group is one API action in one source, separated by request type and outcome. Your settings are kept. Choose fewer sources or add a controller or action filter.", audit.MaxCohorts))
	case errors.Is(err, audit.ErrSummaryRowLimit):
		writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("This comparison needs more than %d summary rows across all selected sources and both periods. These are grouped timing measurements, not individual requests. Your settings are kept. Increase the time interval in chart settings, or add a controller or action filter.", audit.MaxSummaryRows))
	case errors.Is(err, audit.ErrSummaryBytesLimit):
		writeError(w, http.StatusRequestEntityTooLarge, "The grouped timing measurements exceed the 16 MiB memory budget across all selected sources and both periods. Your settings are kept. Increase the time interval in chart settings, or add a controller or action filter.")
	case errors.Is(err, audit.ErrAnalysisBytesLimit):
		writeError(w, http.StatusRequestEntityTooLarge, "The comparison results exceed the 16 MiB display budget. Your settings are kept. Increase the time interval in chart settings, or add a controller or action filter.")
	case errors.Is(err, audit.ErrBreakdownGroupLimit):
		writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("This breakdown has more than %d distinct values in one source. Your settings are kept. Narrow the time range or add a filter.", audit.MaxBreakdownGroups))
	case errors.Is(err, audit.ErrLimit):
		writeError(w, http.StatusRequestEntityTooLarge, "Request exceeds a configured limit.")
	case errors.Is(err, audit.ErrBusy):
		writeError(w, http.StatusTooManyRequests, "Service is busy; retry shortly.")
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusGatewayTimeout, "Request timed out.")
	default:
		writeError(w, http.StatusInternalServerError, "Internal server error.")
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, struct {
		Error string `json:"error"`
	}{message}, 4096)
}

func writeJSON(w http.ResponseWriter, status int, value any, maxBytes int) {
	var output boundedBuffer
	output.limit = maxBytes
	encoder := json.NewEncoder(&output)
	if err := encoder.Encode(value); err != nil {
		if errors.Is(err, errResponseTooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "Response exceeds a configured limit.")
			return
		}
		status = http.StatusInternalServerError
		output.Reset()
		_, _ = output.Write([]byte("{\"error\":\"Internal server error.\"}\n"))
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(output.Bytes())
}

var errResponseTooLarge = errors.New("response exceeds configured byte limit")

type boundedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *boundedBuffer) Write(value []byte) (int, error) {
	if len(value) > b.limit-b.Len() {
		return 0, errResponseTooLarge
	}
	return b.Buffer.Write(value)
}

func (h *handler) export(w http.ResponseWriter, r *http.Request) {
	var input audit.PageInput
	if !decodeJSON(w, r, &input) {
		return
	}
	applyQueryDefaults(&input.Query)
	if err := audit.ValidateQuery(input.Query); err != nil {
		writeRepoError(w, fmt.Errorf("%w: invalid query", audit.ErrInvalidInput))
		return
	}
	if input.Cursor != "" {
		writeError(w, http.StatusBadRequest, "Export starts from the first page; cursor must be empty.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), requestTimeout)
	defer cancel()
	var output boundedBuffer
	output.limit = maxResponseBytes
	usedBytes := 0
	if !writeCSVRow(&output, []string{"id", "sourceId", "controller", "action", "kind", "outcome", "status", "url", "initiatedAt", "completedAt", "durationMs", "apiTimeMs", "auditCreateTimeMs", "payloadRecordTimeMs", "requestKB", "responseKB", "userId", "userIp", "userAgent", "uiVersion", "createdOn", "updatedOn", "pollingTypeRaw"}, &usedBytes) {
		return
	}
	rows := 0
	cursor := ""
	truncated := false
	for rows < maxExportRows {
		input.Cursor = cursor
		input.Limit = audit.MaxPageSize
		page, err := h.repo.Requests(ctx, input)
		if err != nil {
			writeRepoError(w, err)
			return
		}
		for _, source := range page.Sources {
			if !source.OK {
				writeError(w, http.StatusServiceUnavailable, "One or more selected sources could not be queried.")
				return
			}
		}
		csvTruncated := false
		for _, row := range page.Rows {
			if rows == maxExportRows {
				truncated = true
				csvTruncated = true
				break
			}
			cells := requestCSVRow(row)
			if !writeCSVRow(&output, cells, &usedBytes) {
				truncated = true
				csvTruncated = true
				break
			}
			rows++
		}
		if page.Truncated {
			truncated = true
		}
		if csvTruncated {
			break
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
		if rows >= maxExportRows {
			truncated = true
			break
		}
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=unravis-requests.csv")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Export-Limit", strconv.Itoa(maxExportRows))
	if truncated {
		w.Header().Set("X-Export-Truncated", "true")
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(output.Bytes())
}

func writeCSVRow(w io.Writer, cells []string, used *int) bool {
	// Bound the per-row encoder buffer before CSV escaping can expand fields.
	estimate := 0
	for _, cell := range cells {
		if len(cell) > maxResponseBytes/2 || estimate > maxResponseBytes-2*len(cell)-2 {
			return false
		}
		estimate += 2*len(cell) + 2
	}
	if estimate > maxResponseBytes-*used {
		return false
	}
	var row bytes.Buffer
	writer := csv.NewWriter(&row)
	if err := writer.Write(cells); err != nil {
		return false
	}
	writer.Flush()
	if writer.Error() != nil || row.Len() > maxResponseBytes-*used {
		return false
	}
	n, err := w.Write(row.Bytes())
	if err != nil || n != row.Len() {
		return false
	}
	*used += n
	return true
}

func requestCSVRow(row audit.RequestRecord) []string {
	str := func(value string) string { return spreadsheetSafe(value) }
	timeValue := func(value *time.Time) string {
		if value == nil {
			return ""
		}
		return value.UTC().Format(time.RFC3339Nano)
	}
	number := func(value *float64) string {
		if value == nil {
			return ""
		}
		return strconv.FormatFloat(*value, 'f', -1, 64)
	}
	userID := ""
	if row.UserID != nil {
		userID = strconv.Itoa(*row.UserID)
	}
	return []string{str(row.ID), str(row.SourceID), str(row.Controller), str(row.Action), str(row.Kind), str(row.Outcome), str(row.Status), str(row.URL), str(timeValue(row.InitiatedAt)), str(timeValue(row.CompletedAt)), number(row.DurationMs), number(row.ApiTimeMs), number(row.AuditCreateTimeMs), number(row.PayloadRecordTimeMs), strconv.FormatFloat(row.RequestKB, 'f', -1, 64), strconv.FormatFloat(row.ResponseKB, 'f', -1, 64), userID, str(row.UserIP), str(row.UserAgent), str(row.UIVersion), str(timeValue(row.CreatedOn)), str(timeValue(row.UpdatedOn)), str(row.PollingTypeRaw)}
}

func spreadsheetSafe(value string) string {
	if value != "" && strings.ContainsRune("=+-@\t\r", rune(value[0])) {
		return "'" + value
	}
	return value
}
