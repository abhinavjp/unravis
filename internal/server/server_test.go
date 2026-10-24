package server

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	explorer "api-audit-explorer"
	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/history"
	"api-audit-explorer/internal/sessionproof"
	"api-audit-explorer/internal/store"
)

const testToken = "0123456789abcdef0123456789abcdef"

func testQuery() audit.Query {
	start := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	return audit.Query{SourceIDs: []string{"demo-west"}, Start: start, End: start.Add(time.Minute), BaselineStart: start.Add(-time.Minute), BaselineEnd: start, Kind: "all", Outcome: "all", Metric: "total", BucketSeconds: 10}
}

func request(handler http.Handler, method, path, body string, withToken bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = "127.0.0.1:8765"
	if withToken {
		req.Header.Set("X-Audit-Token", testToken)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	return res
}

func TestAPIRoutesRequireExactSessionToken(t *testing.T) {
	h := New(audit.NewDemo(time.Now()), nil, testToken, true)
	for _, token := range []string{"", "wrong-token"} {
		req := httptest.NewRequest(http.MethodGet, "/api/info", nil)
		req.Host = "127.0.0.1:8765"
		req.Header.Set("X-Audit-Token", token)
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		if res.Code != http.StatusUnauthorized {
			t.Errorf("token %q status=%d", token, res.Code)
		}
	}
}

func TestHostAndOriginGuards(t *testing.T) {
	h := New(audit.NewDemo(time.Now()), nil, testToken, true)
	req := httptest.NewRequest(http.MethodGet, "/api/info", nil)
	req.Host = "attacker.example"
	req.Header.Set("X-Audit-Token", testToken)
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("remote host status=%d", res.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/info", nil)
	req.Host = "127.0.0.1:8765"
	req.Header.Set("X-Audit-Token", testToken)
	req.Header.Set("Origin", "http://127.0.0.1:9999")
	res = httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("mismatched origin port status=%d", res.Code)
	}
}

func TestIPv6LoopbackAndTokenedCLIWithoutOriginAreAllowed(t *testing.T) {
	h := New(audit.NewDemo(time.Now()), nil, testToken, true)
	for _, host := range []string{"localhost:8765", "[::1]:8765"} {
		req := httptest.NewRequest(http.MethodGet, "/api/info", nil)
		req.Host = host
		req.Header.Set("X-Audit-Token", testToken)
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Errorf("host %s status=%d body=%s", host, res.Code, res.Body.String())
		}
	}
}

// The unauthenticated page must never carry the session token: any local user
// or process can read GET / on loopback.
func TestRootNeverContainsSessionTokenAndSetsSecurityHeaders(t *testing.T) {
	for name, assets := range map[string]fs.FS{"fixture": testAssets(), "embedded": explorer.Assets()} {
		t.Run(name, func(t *testing.T) {
			h := New(audit.NewDemo(time.Now()), assets, testToken, true)
			for _, path := range []string{"/", "/index.html"} {
				res := request(h, http.MethodGet, path, "", false)
				if path == "/" && res.Code != http.StatusOK {
					t.Fatalf("root status=%d", res.Code)
				}
				if strings.Contains(res.Body.String(), testToken) {
					t.Fatalf("%s served the session token without authentication", path)
				}
			}
			res := request(h, http.MethodGet, "/", "", false)
			if res.Header().Get("Content-Security-Policy") == "" || res.Header().Get("X-Content-Type-Options") != "nosniff" || res.Header().Get("Referrer-Policy") != "no-referrer" {
				t.Fatalf("security headers missing: %#v", res.Header())
			}
		})
	}
}

func TestAPIStillRequiresTheHeaderAfterThePageLoads(t *testing.T) {
	h := New(audit.NewDemo(time.Now()), testAssets(), testToken, true)
	if res := request(h, http.MethodGet, "/", "", false); res.Code != http.StatusOK {
		t.Fatalf("root status=%d", res.Code)
	}
	if res := request(h, http.MethodGet, "/api/info", "", false); res.Code != http.StatusUnauthorized {
		t.Fatalf("api without header status=%d", res.Code)
	}
	if res := request(h, http.MethodGet, "/api/info", "", true); res.Code != http.StatusOK {
		t.Fatalf("api with header status=%d body=%s", res.Code, res.Body.String())
	}
}

func TestAssetsServeExactFilesOnlyWithoutDirectoryListings(t *testing.T) {
	h := New(audit.NewDemo(time.Now()), testAssets(), testToken, true)
	if res := request(h, http.MethodGet, "/assets/app.js", "", false); res.Code != http.StatusOK || res.Body.String() != "ok" {
		t.Fatalf("asset status=%d body=%q", res.Code, res.Body.String())
	}
	for _, path := range []string{"/assets", "/assets/", "/assets/missing.js", "/assets/../index.html", "/assets//app.js", "/assets/./app.js"} {
		res := request(h, http.MethodGet, path, "", false)
		if res.Code != http.StatusNotFound {
			t.Errorf("%s status=%d body=%q", path, res.Code, res.Body.String())
		}
		if strings.Contains(res.Body.String(), "app.js") {
			t.Errorf("%s listed directory contents: %q", path, res.Body.String())
		}
	}
	embedded := New(audit.NewDemo(time.Now()), explorer.Assets(), testToken, true)
	if res := request(embedded, http.MethodGet, "/assets/", "", false); res.Code != http.StatusNotFound || strings.Contains(res.Body.String(), ".js") {
		t.Fatalf("embedded /assets/ status=%d body=%q", res.Code, res.Body.String())
	}
}

func TestConnectionTestsAreRateLimited(t *testing.T) {
	repo := &connectionTestRepository{fakeRepository: &fakeRepository{}, result: audit.ConnectionResult{Databases: []string{"a"}}}
	h := New(repo, nil, testToken, false).(*handler)
	clock := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	h.tests.now = func() time.Time { return clock }
	body := `{"uri":"mongodb://127.0.0.1:27017"}`
	for i := 0; i < connectionTestsPerWindow; i++ {
		if res := request(h, http.MethodPost, "/api/connections/test", body, true); res.Code == http.StatusTooManyRequests {
			t.Fatalf("test %d refused early", i+1)
		}
	}
	res := request(h, http.MethodPost, "/api/connections/test", body, true)
	if res.Code != http.StatusTooManyRequests || !strings.Contains(res.Body.String(), "Wait a minute") {
		t.Fatalf("over-limit status=%d body=%s", res.Code, res.Body.String())
	}
	clock = clock.Add(connectionTestWindow)
	if res := request(h, http.MethodPost, "/api/connections/test", body, true); res.Code == http.StatusTooManyRequests {
		t.Fatalf("limit did not reset after the window: %s", res.Body.String())
	}
}

func TestJSONPostsRejectContentTypeOversizeUnknownFieldsAndInvalidQueries(t *testing.T) {
	h := New(audit.NewDemo(time.Now()), nil, testToken, true)
	for _, tc := range []struct {
		name, body, content string
		want                int
	}{
		{"media type", `{}`, "text/plain", http.StatusUnsupportedMediaType},
		{"unknown field", `{"unexpected":true}`, "application/json", http.StatusBadRequest},
		{"invalid query", `{"sourceIds":[]}`, "application/json", http.StatusBadRequest},
		{"oversize", strings.Repeat(" ", 1<<20+1), "application/json", http.StatusRequestEntityTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/analyze", strings.NewReader(tc.body))
			req.Host = "127.0.0.1:8765"
			req.Header.Set("X-Audit-Token", testToken)
			req.Header.Set("Content-Type", tc.content)
			res := httptest.NewRecorder()
			h.ServeHTTP(res, req)
			if res.Code != tc.want {
				t.Fatalf("status=%d want=%d body=%s", res.Code, tc.want, res.Body.String())
			}
		})
	}
}

func TestConnectionTestRouteUsesSharedSecurityAndReturnsOnlyDatabaseNames(t *testing.T) {
	repo := &connectionTestRepository{
		fakeRepository: &fakeRepository{},
		result:         audit.ConnectionResult{Databases: []string{"allowed-a", "allowed-b"}, Warning: "list capped"},
	}
	h := New(repo, nil, testToken, false)
	input := audit.ConnectionInput{URI: "mongodb://host/?tls=true", Username: "reader", Password: "secret", AuthSource: "admin"}
	body, _ := json.Marshal(input)
	unauthorized := request(h, http.MethodPost, "/api/connections/test", string(body), false)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status=%d", unauthorized.Code)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/connections/test", strings.NewReader(string(body)))
	req.Host = "127.0.0.1:8765"
	req.Header.Set("X-Audit-Token", testToken)
	req.Header.Set("Origin", "http://127.0.0.1:9999")
	req.Header.Set("Content-Type", "application/json")
	denied := httptest.NewRecorder()
	h.ServeHTTP(denied, req)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("mismatched origin status=%d", denied.Code)
	}
	res := request(h, http.MethodPost, "/api/connections/test", string(body), true)
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	if repo.input != input {
		t.Fatalf("repository input=%#v, want %#v", repo.input, input)
	}
	if strings.Contains(res.Body.String(), "secret") || strings.Contains(res.Body.String(), "mongodb://") {
		t.Fatalf("connection details leaked in response: %s", res.Body.String())
	}
	var result audit.ConnectionResult
	if err := json.Unmarshal(res.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Databases) != 2 || result.Databases[0] != "allowed-a" || result.Warning != "list capped" {
		t.Fatalf("connection result=%#v", result)
	}
}

func TestConnectionTestSanitizesCredentialsAndRejectsDemoRepository(t *testing.T) {
	input := audit.ConnectionInput{URI: "mongodb://reader:top-secret@host", Username: "other", Password: "password"}
	body, _ := json.Marshal(input)
	repo := &connectionTestRepository{fakeRepository: &fakeRepository{}, err: fmt.Errorf("%w: mongodb://reader:top-secret@host password=password", audit.ErrInvalidInput)}
	h := New(repo, nil, testToken, false)
	res := request(h, http.MethodPost, "/api/connections/test", string(body), true)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("failure status=%d body=%s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "Check the URI, credentials, authentication database, and permissions.") {
		t.Fatalf("connection failure was not actionable: %s", res.Body.String())
	}
	for _, secret := range []string{"top-secret", "password=password", "mongodb://", "reader"} {
		if strings.Contains(res.Body.String(), secret) {
			t.Fatalf("connection error leaked %q: %s", secret, res.Body.String())
		}
	}
	demo := New(audit.NewDemo(time.Now()), nil, testToken, true)
	unsupported := request(demo, http.MethodPost, "/api/connections/test", string(body), true)
	if unsupported.Code != http.StatusBadRequest || strings.Contains(unsupported.Body.String(), "top-secret") {
		t.Fatalf("demo connection test status/body=%d %s", unsupported.Code, unsupported.Body.String())
	}
}

func TestErrorMappingAndSanitizedInternalFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		want   int
		secret bool
	}{
		{"invalid", audit.ErrInvalidInput, http.StatusBadRequest, false},
		{"missing", audit.ErrNotFound, http.StatusNotFound, false},
		{"limit", audit.ErrLimit, http.StatusRequestEntityTooLarge, false},
		{"busy", audit.ErrBusy, http.StatusTooManyRequests, false},
		{"deadline", context.DeadlineExceeded, http.StatusGatewayTimeout, false},
		{"internal", errors.New("mongodb://user:password@db.example/private"), http.StatusInternalServerError, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeRepository{sourceErr: tc.err}
			h := New(repo, nil, testToken, false)
			res := request(h, http.MethodGet, "/api/sources", "", true)
			if res.Code != tc.want {
				t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
			}
			if tc.secret && strings.Contains(res.Body.String(), "password") {
				t.Fatalf("driver error leaked: %s", res.Body.String())
			}
		})
	}
}

func TestAPIGroupLimitExplainsGroupsWithoutLeakingWrappedDetails(t *testing.T) {
	w := httptest.NewRecorder()
	writeRepoError(w, errors.Join(audit.ErrLimit, audit.ErrAPIGroupLimit, errors.New("mongodb://user:secret@private")))
	if w.Code != http.StatusRequestEntityTooLarge || !strings.Contains(w.Body.String(), "10000 API groups") || !strings.Contains(w.Body.String(), "one API action") {
		t.Fatalf("unexpected response: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "secret") || strings.Contains(w.Body.String(), "mongodb://") {
		t.Fatal("wrapped details leaked")
	}
}

func TestAnalysisSourceFailuresAreRedactedAndEmptyArraysStayArrays(t *testing.T) {
	repo := &fakeRepository{analysis: audit.Analysis{APIs: nil, Timeline: nil, Sources: []audit.SourceResult{{SourceID: "s", OK: false, Error: "mongodb://user:secret@host/db"}}, Warnings: nil}}
	h := New(repo, nil, testToken, false)
	body, _ := json.Marshal(testQuery())
	res := request(h, http.MethodPost, "/api/analyze", string(body), true)
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(res.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"apis", "timeline", "sources", "warnings"} {
		if string(decoded[name]) == "null" {
			t.Errorf("%s encoded as null", name)
		}
	}
	if strings.Contains(res.Body.String(), "secret") || strings.Contains(res.Body.String(), "mongodb://") {
		t.Fatalf("source URI leaked: %s", res.Body.String())
	}
}

func TestPayloadIsPlaintextInsideValidJSON(t *testing.T) {
	repo := &fakeRepository{payload: audit.Payload{Request: "line one\n雪", Response: "ok"}}
	h := New(repo, nil, testToken, false)
	res := request(h, http.MethodPost, "/api/payload", `{"sourceId":"s","id":"r"}`, true)
	var payload audit.Payload
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if payload.Request != "line one\n雪" {
		t.Fatalf("payload changed: %q", payload.Request)
	}
}

func TestServerRecordRouteDisallowsUnknownFields(t *testing.T) {
	uid := 7
	created := time.Date(2026, 10, 1, 9, 0, 0, 0, time.FixedZone("x", 3600))
	repo := &fakeRepository{record: audit.RecordDetail{
		RequestRecord: audit.RequestRecord{ID: "r", SourceID: "s", UserID: &uid, UserIP: "203.0.113.9", UIVersion: "9.4.1", CreatedOn: &created, PollingTypeRaw: "Synchronous "},
		State:         "valid",
		Payload:       audit.Payload{Request: "q", Response: "p"},
	}}
	h := New(repo, nil, testToken, false)
	for _, body := range []string{`{"sourceId":"s","id":"r","extra":1}`, `{"sourceId":"s","id":"r","payload":true}`, `{"sourceId":"s","id":"r"} {}`} {
		if res := request(h, http.MethodPost, "/api/record", body, true); res.Code != http.StatusBadRequest {
			t.Fatalf("body %s status=%d, want 400: %s", body, res.Code, res.Body.String())
		}
	}
	if repo.recordInput != (audit.RecordInput{}) {
		t.Fatalf("rejected body reached the repository: %#v", repo.recordInput)
	}
	if res := request(h, http.MethodPost, "/api/record", `{"sourceId":"s","id":"r"}`, false); res.Code == http.StatusOK {
		t.Fatal("record route must require the session token")
	}
	res := request(h, http.MethodPost, "/api/record", `{"sourceId":"s","id":"r"}`, true)
	if res.Code != http.StatusOK || repo.recordInput != (audit.RecordInput{SourceID: "s", ID: "r"}) {
		t.Fatalf("status=%d input=%#v body=%s", res.Code, repo.recordInput, res.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["userId"] != float64(7) || body["uiVersion"] != "9.4.1" || body["pollingTypeRaw"] != "Synchronous " || body["state"] != "valid" || body["createdOn"] != "2026-10-01T08:00:00Z" {
		t.Fatalf("record JSON wrong: %s", res.Body.String())
	}
	if _, present := body["updatedOn"]; present {
		t.Fatalf("absent timestamps must be omitted: %s", res.Body.String())
	}
	repo.recordErr = fmt.Errorf("%w: request was not found", audit.ErrNotFound)
	if res := request(h, http.MethodPost, "/api/record", `{"sourceId":"s","id":"r"}`, true); res.Code != http.StatusNotFound {
		t.Fatalf("not found status=%d", res.Code)
	}
}

func TestExportIncludesAuditEvidenceColumns(t *testing.T) {
	uid := 7
	created := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	row := audit.RequestRecord{ID: "r", SourceID: "s", UserID: &uid, UserIP: "203.0.113.9", UserAgent: "=cmd", UIVersion: "9.4.1", CreatedOn: &created, PollingTypeRaw: "Synchronous "}
	h := New(&fakeRepository{requestPages: []audit.Page{{Rows: []audit.RequestRecord{row}}}}, nil, testToken, false)
	q, _ := json.Marshal(audit.PageInput{Query: testQuery(), Limit: 100})
	res := request(h, http.MethodPost, "/api/export", string(q), true)
	records, err := csv.NewReader(strings.NewReader(res.Body.String())).ReadAll()
	if err != nil || res.Code != http.StatusOK || len(records) != 2 {
		t.Fatalf("status=%d err=%v records=%d body=%s", res.Code, err, len(records), res.Body.String())
	}
	if disposition := res.Header().Get("Content-Disposition"); disposition != "attachment; filename=unravis-requests.csv" {
		t.Errorf("Content-Disposition = %q, want the unravis-requests.csv attachment", disposition)
	}
	got := map[string]string{}
	for i, name := range records[0] {
		got[name] = records[1][i]
	}
	want := map[string]string{"userId": "7", "userIp": "203.0.113.9", "userAgent": "'=cmd", "uiVersion": "9.4.1", "createdOn": "2026-10-01T09:00:00Z", "updatedOn": "", "pollingTypeRaw": "Synchronous ", "auditCreateTimeMs": ""}
	for name, value := range want {
		if v, ok := got[name]; !ok || v != value {
			t.Errorf("column %s = %q (present=%t), want %q", name, v, ok, value)
		}
	}
	if records[0][0] != "id" || records[0][15] != "responseKB" {
		t.Errorf("existing column order changed: %v", records[0])
	}
}

func TestExportEscapesFormulaPrefixesAndCapsRows(t *testing.T) {
	repo := &fakeRepository{exportRows: 10005}
	h := New(repo, nil, testToken, false)
	q, _ := json.Marshal(audit.PageInput{Query: testQuery(), Limit: 100})
	res := request(h, http.MethodPost, "/api/export", string(q), true)
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	if res.Header().Get("X-Export-Limit") != "10000" || !strings.Contains(res.Body.String(), "'=") || !strings.Contains(res.Body.String(), "'\t") {
		t.Fatalf("export limit or formula escaping missing: headers=%v prefix=%q", res.Header(), res.Body.String()[:min(100, len(res.Body.String()))])
	}
	if repo.requestCalls > 101 {
		t.Fatalf("unexpected page calls=%d", repo.requestCalls)
	}
}

type fakeRepository struct {
	sourceErr       error
	validationInput audit.ConnectionInput
	validationErr   error
	validationCalls int
	analysis        audit.Analysis
	payload         audit.Payload
	record          audit.RecordDetail
	recordInput     audit.RecordInput
	recordErr       error
	exportRows      int
	largeURL        string
	requestPages    []audit.Page
	requestCalls    int
}

func (f *fakeRepository) Sources(context.Context) ([]audit.Source, error) {
	if f.sourceErr != nil {
		return nil, f.sourceErr
	}
	return nil, nil
}
func (f *fakeRepository) AddSource(context.Context, audit.SourceInput) (audit.Source, error) {
	return audit.Source{}, nil
}
func (f *fakeRepository) ValidateConnectionSettings(input audit.ConnectionInput) error {
	f.validationCalls++
	f.validationInput = input
	return f.validationErr
}
func (f *fakeRepository) DeleteSource(context.Context, string) error { return nil }
func (f *fakeRepository) Analyze(context.Context, audit.Query) (audit.Analysis, error) {
	if f.sourceErr != nil {
		return audit.Analysis{}, f.sourceErr
	}
	return f.analysis, nil
}
func (f *fakeRepository) Requests(_ context.Context, in audit.PageInput) (audit.Page, error) {
	f.requestCalls++
	if len(f.requestPages) > 0 {
		index := min(f.requestCalls-1, len(f.requestPages)-1)
		return f.requestPages[index], nil
	}
	if f.exportRows == 0 {
		return audit.Page{}, nil
	}
	offset := 0
	if in.Cursor != "" {
		fmt.Sscan(in.Cursor, &offset)
	}
	remaining := f.exportRows - offset
	if remaining <= 0 {
		return audit.Page{}, nil
	}
	n := min(100, remaining)
	rows := make([]audit.RequestRecord, n)
	for i := range rows {
		id := fmt.Sprintf("id-%05d", offset+i)
		url := "/ok"
		if offset+i == 0 {
			url = "=1+1"
		}
		if offset+i == 1 {
			url = "\t=1+1"
		}
		if f.largeURL != "" {
			url = f.largeURL
		}
		rows[i] = audit.RequestRecord{ID: id, SourceID: "s", URL: url, Outcome: "success", Status: "200 OK", RequestKB: 1, ResponseKB: 2}
	}
	next := ""
	if offset+n < f.exportRows {
		next = fmt.Sprint(offset + n)
	}
	return audit.Page{Rows: rows, NextCursor: next}, nil
}
func (f *fakeRepository) Payload(context.Context, audit.PayloadInput) (audit.Payload, error) {
	return f.payload, nil
}
func (f *fakeRepository) Record(_ context.Context, in audit.RecordInput) (audit.RecordDetail, error) {
	f.recordInput = in
	return f.record, f.recordErr
}
func (f *fakeRepository) Close(context.Context) error { return nil }

type connectionTestRepository struct {
	*fakeRepository
	input  audit.ConnectionInput
	calls  int
	result audit.ConnectionResult
	err    error
}

type sourceCaptureRepository struct {
	*fakeRepository
	input audit.SourceInput
}

type blockingValidationRepository struct {
	*fakeRepository
	started chan struct{}
	release chan struct{}
}

func (r *blockingValidationRepository) ValidateConnectionSettings(audit.ConnectionInput) error {
	close(r.started)
	<-r.release
	return nil
}

type serverTestProtector struct{}

func (serverTestProtector) Protect(value []byte) ([]byte, error) {
	return append([]byte("protected:"), value...), nil
}
func (serverTestProtector) Unprotect(value []byte) ([]byte, error) {
	if !bytes.HasPrefix(value, []byte("protected:")) {
		return nil, errors.New("bad test data")
	}
	return bytes.TrimPrefix(value, []byte("protected:")), nil
}

func (r *sourceCaptureRepository) AddSource(_ context.Context, input audit.SourceInput) (audit.Source, error) {
	r.input = input
	return audit.Source{ID: "source-1", Label: input.Label, Database: input.Database, Collection: input.Collection}, nil
}

func (r *connectionTestRepository) TestConnection(_ context.Context, input audit.ConnectionInput) (audit.ConnectionResult, error) {
	r.calls++
	r.input = input
	return r.result, r.err
}

type fakeHistory struct {
	items              []history.Recent
	resolved           audit.ConnectionInput
	resolveErr         error
	remembered         audit.ConnectionInput
	rememberedPassword bool
	deleted            string
	rememberCalls      int
}

func (f *fakeHistory) List() ([]history.Recent, error) { return f.items, nil }
func (f *fakeHistory) Remember(label string, in audit.ConnectionInput, collection string, rememberPassword bool) (history.Recent, error) {
	f.rememberCalls++
	f.remembered, f.rememberedPassword = in, rememberPassword
	return history.Recent{ID: "saved-1", Label: label, URI: in.URI, Username: in.Username, AuthSource: in.AuthSource, TLSMode: in.TLSMode, CACertificate: in.CACertificate, Collection: collection, HasCredentials: rememberPassword, LastUsedAt: time.Now().UTC()}, nil
}
func (f *fakeHistory) Resolve(in audit.ConnectionInput) (audit.ConnectionInput, error) {
	if f.resolveErr != nil {
		return audit.ConnectionInput{}, f.resolveErr
	}
	if in.SavedConnectionID != "saved-1" || in.URI != f.resolved.URI || in.Username != f.resolved.Username || in.AuthSource != f.resolved.AuthSource || in.TLSMode != f.resolved.TLSMode || in.CACertificate != f.resolved.CACertificate {
		return audit.ConnectionInput{}, audit.ErrInvalidInput
	}
	if in.Password == "" {
		in.Password = f.resolved.Password
	}
	in.SavedConnectionID = ""
	return in, nil
}
func (f *fakeHistory) Delete(id string) error {
	f.deleted = id
	if id != "saved-1" {
		return history.ErrNotFound
	}
	return nil
}

func TestSavedConnectionRoutesHidePasswordAndUseSharedGuards(t *testing.T) {
	input := audit.ConnectionInput{URI: "mongodb://db.example", Username: "reader", Password: "private-password", AuthSource: "admin", TLSMode: "system"}
	repo := &connectionTestRepository{fakeRepository: &fakeRepository{}, err: errors.New("synthetic unreachable database")}
	saved := &fakeHistory{resolved: input, items: []history.Recent{{ID: "saved-1", URI: input.URI, Username: input.Username, HasCredentials: true}}}
	h := New(repo, nil, testToken, false, WithHistory(saved))

	unauthorized := request(h, http.MethodGet, "/api/connections/recent", "", false)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("recent missing token status=%d", unauthorized.Code)
	}
	listed := request(h, http.MethodGet, "/api/connections/recent", "", true)
	if listed.Code != http.StatusOK || strings.Contains(listed.Body.String(), input.Password) {
		t.Fatalf("recent status/body=%d %s", listed.Code, listed.Body.String())
	}

	input.SavedConnectionID = "saved-1"
	body, _ := json.Marshal(struct {
		Label            string                `json:"label"`
		Connection       audit.ConnectionInput `json:"connection"`
		Collection       string                `json:"collection"`
		RememberPassword bool                  `json:"rememberPassword"`
	}{"production", input, "audit", false})
	remembered := request(h, http.MethodPost, "/api/connections/remember", string(body), true)
	if remembered.Code != http.StatusOK || strings.Contains(remembered.Body.String(), input.Password) {
		t.Fatalf("remember status/body=%d %s", remembered.Code, remembered.Body.String())
	}
	if repo.validationInput.Password != input.Password || repo.validationInput.SavedConnectionID != "" || repo.calls != 0 {
		t.Fatalf("offline validator input=%#v, network test calls=%d", repo.validationInput, repo.calls)
	}
	if repo.validationCalls != 1 || saved.rememberCalls != 1 {
		t.Fatalf("offline validation calls=%d, save calls=%d", repo.validationCalls, saved.rememberCalls)
	}
	if saved.remembered.Password != input.Password || saved.rememberedPassword {
		t.Fatalf("unchecked password was not available for session-only persistence: %#v", saved.remembered)
	}

	deleted := request(h, http.MethodDelete, "/api/connections/recent/saved-1", "", true)
	if deleted.Code != http.StatusNoContent || saved.deleted != "saved-1" {
		t.Fatalf("delete status=%d id=%q", deleted.Code, saved.deleted)
	}
}

func TestRememberRejectsInvalidURIAndTLSSettingsWithoutWriting(t *testing.T) {
	for _, connection := range []audit.ConnectionInput{
		{URI: "http://db.example", TLSMode: "system"},
		{URI: "mongodb://db.example", TLSMode: "custom", CACertificate: "not a PEM certificate"},
	} {
		repo := store.New()
		saved := &fakeHistory{}
		h := New(repo, nil, testToken, false, WithHistory(saved))
		body, _ := json.Marshal(struct {
			Label            string                `json:"label"`
			Connection       audit.ConnectionInput `json:"connection"`
			Collection       string                `json:"collection"`
			RememberPassword bool                  `json:"rememberPassword"`
		}{"invalid", connection, "audit", false})
		res := request(h, http.MethodPost, "/api/connections/remember", string(body), true)
		_ = repo.Close(context.Background())
		if res.Code != http.StatusBadRequest || saved.rememberCalls != 0 {
			t.Fatalf("invalid settings saved: input=%#v status=%d calls=%d body=%s", connection, res.Code, saved.rememberCalls, res.Body.String())
		}
	}
}

func TestConnectionTestReturnsTypedSafeFailure(t *testing.T) {
	repo := &connectionTestRepository{fakeRepository: &fakeRepository{}, err: &audit.ConnectionError{Stage: "connection", Code: "network_timeout", Message: "Connection attempt timed out."}}
	h := New(repo, nil, testToken, false)
	input := audit.ConnectionInput{URI: "mongodb://db.example"}
	body, _ := json.Marshal(input)
	res := request(h, http.MethodPost, "/api/connections/test", string(body), true)
	var payload map[string]string
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if res.Code != http.StatusBadRequest || payload["error"] != "Connection attempt timed out." || payload["code"] != "network_timeout" || payload["stage"] != "connection" {
		t.Fatalf("typed error response status=%d payload=%#v", res.Code, payload)
	}
}

func TestRememberReturnsValidatedCAWithinResponseLimit(t *testing.T) {
	ca := strings.Repeat("A", 20<<10)
	input := audit.ConnectionInput{URI: "mongodb://db.example", TLSMode: "custom", CACertificate: ca}
	repo := &connectionTestRepository{fakeRepository: &fakeRepository{}}
	saved := &fakeHistory{}
	h := New(repo, nil, testToken, false, WithHistory(saved))
	body, _ := json.Marshal(struct {
		Label            string                `json:"label"`
		Connection       audit.ConnectionInput `json:"connection"`
		Collection       string                `json:"collection"`
		RememberPassword bool                  `json:"rememberPassword"`
	}{"large CA", input, "audit", false})
	res := request(h, http.MethodPost, "/api/connections/remember", string(body), true)
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body prefix=%q", res.Code, res.Body.String()[:min(100, res.Body.Len())])
	}
	var recent history.Recent
	if err := json.Unmarshal(res.Body.Bytes(), &recent); err != nil {
		t.Fatal(err)
	}
	if recent.CACertificate != ca {
		t.Fatalf("certificate was not returned intact: got %d bytes", len(recent.CACertificate))
	}
}

func TestForgetDuringRememberValidationDoesNotRecreateRecent(t *testing.T) {
	service, err := history.New(filepath.Join(t.TempDir(), "connections.dat"), serverTestProtector{})
	if err != nil {
		t.Fatal(err)
	}
	seed := audit.ConnectionInput{URI: "mongodb://db.example", Username: "reader", Password: "session-secret", AuthSource: "admin", TLSMode: "system"}
	saved, err := service.Remember("production", seed, "audit", true)
	if err != nil {
		t.Fatal(err)
	}
	repo := &blockingValidationRepository{fakeRepository: &fakeRepository{}, started: make(chan struct{}), release: make(chan struct{})}
	h := New(repo, nil, testToken, false, WithHistory(service))
	input := seed
	input.Password = ""
	input.SavedConnectionID = saved.ID
	body, _ := json.Marshal(struct {
		Label            string                `json:"label"`
		Connection       audit.ConnectionInput `json:"connection"`
		Collection       string                `json:"collection"`
		RememberPassword bool                  `json:"rememberPassword"`
	}{"production", input, "audit", false})
	req := httptest.NewRequest(http.MethodPost, "/api/connections/remember", strings.NewReader(string(body)))
	req.Host = "127.0.0.1:8765"
	req.Header.Set("X-Audit-Token", testToken)
	req.Header.Set("Content-Type", "application/json")
	res := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { h.ServeHTTP(res, req); close(done) }()
	<-repo.started
	if err := service.Delete(saved.ID); err != nil {
		t.Fatal(err)
	}
	close(repo.release)
	<-done
	if res.Code != http.StatusBadRequest {
		t.Fatalf("remember completed after forget: status=%d body=%s", res.Code, res.Body.String())
	}
	items, err := service.List()
	if err != nil || len(items) != 0 {
		t.Fatalf("forget race recreated saved connection: %#v %v", items, err)
	}
}

func TestSavedConnectionIdentifierRejectsMetadataChangesBeforeRepository(t *testing.T) {
	stored := audit.ConnectionInput{URI: "mongodb://db.example", Username: "reader", Password: "private-password", AuthSource: "admin", TLSMode: "system"}
	saved := &fakeHistory{resolved: stored}
	repo := &connectionTestRepository{fakeRepository: &fakeRepository{}}
	h := New(repo, nil, testToken, false, WithHistory(saved))
	changed := stored
	changed.URI = "mongodb://attacker.example"
	changed.SavedConnectionID = "saved-1"
	body, _ := json.Marshal(changed)
	res := request(h, http.MethodPost, "/api/connections/test", string(body), true)
	if res.Code != http.StatusBadRequest || repo.calls != 0 {
		t.Fatalf("changed saved address reached repo: status=%d calls=%d body=%s", res.Code, repo.calls, res.Body.String())
	}
	if strings.Contains(res.Body.String(), stored.Password) {
		t.Fatalf("password leaked: %s", res.Body.String())
	}
}

func TestAddSourceResolvesSavedPasswordAndClearsVaultIdentifier(t *testing.T) {
	stored := audit.ConnectionInput{URI: "mongodb://db.example", Username: "reader", Password: "private-password", AuthSource: "admin", TLSMode: "system"}
	saved := &fakeHistory{resolved: stored}
	repo := &sourceCaptureRepository{fakeRepository: &fakeRepository{}}
	h := New(repo, nil, testToken, false, WithHistory(saved))
	input := audit.SourceInput{Label: "production", URI: stored.URI, Username: stored.Username, AuthSource: stored.AuthSource, TLSMode: stored.TLSMode, SavedConnectionID: "saved-1", Database: "events", Collection: "audit"}
	body, _ := json.Marshal(input)
	res := request(h, http.MethodPost, "/api/sources", string(body), true)
	if res.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	if repo.input.Password != stored.Password || repo.input.SavedConnectionID != "" {
		t.Fatalf("source repo input=%#v", repo.input)
	}
	if strings.Contains(res.Body.String(), stored.Password) {
		t.Fatalf("source response leaked saved password: %s", res.Body.String())
	}
}

func testAssets() fs.FS {
	return fstest.MapFS{"index.html": {Data: []byte(`<!doctype html><html><head></head><body>ok</body></html>`)}, "assets/app.js": {Data: []byte("ok")}}
}

func TestRequestPageLimitIsEnforcedAtHTTPBoundary(t *testing.T) {
	repo := &fakeRepository{}
	h := New(repo, nil, testToken, false)
	input, _ := json.Marshal(audit.PageInput{Query: testQuery(), APIKey: "key", Limit: audit.MaxPageSize + 1})
	res := request(h, http.MethodPost, "/api/requests", string(input), true)
	if res.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	if repo.requestCalls != 0 {
		t.Fatalf("repo called before page limit validation: %d", repo.requestCalls)
	}
}

func TestExportStopsAtResponseByteLimitWithoutPartialRow(t *testing.T) {
	repo := &fakeRepository{exportRows: 1, largeURL: strings.Repeat("x", maxResponseBytes+1)}
	h := New(repo, nil, testToken, false)
	input, _ := json.Marshal(audit.PageInput{Query: testQuery(), Limit: audit.MaxPageSize})
	res := request(h, http.MethodPost, "/api/export", string(input), true)
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d", res.Code)
	}
	if len(res.Body.Bytes()) > maxResponseBytes {
		t.Fatalf("CSV exceeded byte cap: %d", len(res.Body.Bytes()))
	}
	if res.Header().Get("X-Export-Truncated") != "true" {
		t.Fatalf("byte truncation wasn't indicated in response headers: %#v", res.Header())
	}
}

func TestExportFailsBeforeCSVWhenSourceQueryFails(t *testing.T) {
	repo := &fakeRepository{requestPages: []audit.Page{{Sources: []audit.SourceResult{{SourceID: "offline", Error: "mongodb://user:secret@db/private"}}}}}
	h := New(repo, nil, testToken, false)
	input, _ := json.Marshal(audit.PageInput{Query: testQuery(), Limit: audit.MaxPageSize})
	res := request(h, http.MethodPost, "/api/export", string(input), true)
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want=%d body=%s", res.Code, http.StatusServiceUnavailable, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "text/csv") || strings.Contains(res.Body.String(), "secret") || strings.Contains(res.Body.String(), "mongodb://") {
		t.Fatalf("failed source exported CSV or leaked source details: %s", res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "could not be queried") {
		t.Fatalf("missing safe source failure message: %s", res.Body.String())
	}
}

func TestExportRejectsMixedPartialSourcesBeforeReturningRows(t *testing.T) {
	repo := &fakeRepository{requestPages: []audit.Page{{
		Rows: []audit.RequestRecord{{ID: "partial-row", SourceID: "healthy", URL: "/ok"}},
		Sources: []audit.SourceResult{
			{SourceID: "healthy", OK: true},
			{SourceID: "offline", OK: false, Error: "private driver details"},
		},
	}}}
	h := New(repo, nil, testToken, false)
	input, _ := json.Marshal(audit.PageInput{Query: testQuery(), Limit: audit.MaxPageSize})
	res := request(h, http.MethodPost, "/api/export", string(input), true)
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want=%d body=%s", res.Code, http.StatusServiceUnavailable, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "partial-row") || strings.Contains(res.Body.String(), "private driver details") {
		t.Fatalf("partial CSV rows or source details escaped: %s", res.Body.String())
	}
}

func TestInfoAndEmptySourceRoutesMatchContracts(t *testing.T) {
	h := New(&fakeRepository{}, nil, testToken, true)
	info := request(h, http.MethodGet, "/api/info", "", true)
	if info.Code != http.StatusOK {
		t.Fatalf("info status=%d body=%s", info.Code, info.Body.String())
	}
	var metadata struct {
		Demo   bool           `json:"demo"`
		Limits map[string]int `json:"limits"`
		Now    time.Time      `json:"now"`
	}
	if err := json.Unmarshal(info.Body.Bytes(), &metadata); err != nil {
		t.Fatal(err)
	}
	if !metadata.Demo || metadata.Limits["maxSources"] != 8 || metadata.Limits["maxPageSize"] != 100 || metadata.Limits["maxSummaryRows"] != 40000 || metadata.Limits["maxTimeBuckets"] != 120 || metadata.Now.IsZero() {
		t.Fatalf("info response = %#v", metadata)
	}
	sources := request(h, http.MethodGet, "/api/sources", "", true)
	if sources.Code != http.StatusOK || strings.TrimSpace(sources.Body.String()) != "[]" {
		t.Fatalf("empty source list = status %d body %s", sources.Code, sources.Body.String())
	}
}

func TestDemoInfoTimeAnchorsDefaultAnalysisToDemoDataset(t *testing.T) {
	anchor := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	h := New(audit.NewDemo(anchor), nil, testToken, true)
	infoResponse := request(h, http.MethodGet, "/api/info", "", true)
	if infoResponse.Code != http.StatusOK {
		t.Fatalf("info status=%d body=%s", infoResponse.Code, infoResponse.Body.String())
	}
	var info struct {
		Now time.Time `json:"now"`
	}
	if err := json.Unmarshal(infoResponse.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if !info.Now.Equal(anchor) {
		t.Fatalf("demo info time=%s, want fixture anchor %s", info.Now, anchor)
	}
	query := testQuery()
	query.SourceIDs = []string{"demo-west"}
	query.End = info.Now
	query.Start = info.Now.Add(-15 * time.Minute)
	query.BaselineEnd = query.Start
	query.BaselineStart = query.BaselineEnd.Add(-15 * time.Minute)
	body, _ := json.Marshal(query)
	analysisResponse := request(h, http.MethodPost, "/api/analyze", string(body), true)
	if analysisResponse.Code != http.StatusOK {
		t.Fatalf("analysis status=%d body=%s", analysisResponse.Code, analysisResponse.Body.String())
	}
	var analysis audit.Analysis
	if err := json.Unmarshal(analysisResponse.Body.Bytes(), &analysis); err != nil {
		t.Fatal(err)
	}
	for _, comparison := range analysis.APIs {
		if comparison.Controller == "Payroll" && comparison.P95Ratio != nil {
			if *comparison.P95Ratio < 4.5 || *comparison.P95Ratio > 5.5 {
				t.Fatalf("default demo payroll p95 ratio=%f, want approximately 5x", *comparison.P95Ratio)
			}
			return
		}
	}
	t.Fatalf("default demo window did not produce a compared Payroll cohort: %#v", analysis.APIs)
}

func TestSummarySerializationStopsAtConfiguredByteLimit(t *testing.T) {
	repo := &fakeRepository{analysis: audit.Analysis{Warnings: []string{strings.Repeat("x", maxResponseBytes+1)}}}
	h := New(repo, nil, testToken, false)
	body, _ := json.Marshal(testQuery())
	res := request(h, http.MethodPost, "/api/analyze", string(body), true)
	if res.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}
	if len(res.Body.Bytes()) > 4096 {
		t.Fatalf("oversized response was buffered or sent: %d bytes", len(res.Body.Bytes()))
	}
}

func TestEmbeddedDemoHTTPWorkflow(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	h := New(audit.NewDemo(now), explorer.Assets(), testToken, true)
	root := request(h, http.MethodGet, "/", "", false)
	if root.Code != http.StatusOK || strings.Contains(root.Body.String(), testToken) {
		t.Fatalf("embedded dashboard did not load, or served the session token: status=%d", root.Code)
	}
	assetMatch := regexp.MustCompile(`src="([^"]+\.js)"`).FindStringSubmatch(root.Body.String())
	if len(assetMatch) != 2 {
		t.Fatalf("embedded dashboard did not reference a JavaScript asset: %s", root.Body.String())
	}
	assets := request(h, http.MethodGet, assetMatch[1], "", false)
	if assets.Code != http.StatusOK || !strings.Contains(assets.Header().Get("Content-Type"), "javascript") {
		t.Fatalf("embedded app asset did not load: status=%d type=%q", assets.Code, assets.Header().Get("Content-Type"))
	}
	query := testQuery()
	query.SourceIDs = []string{"demo-west", "demo-east"}
	query.Start, query.End = now.Add(-15*time.Minute), now
	query.BaselineStart, query.BaselineEnd = now.Add(-30*time.Minute), now.Add(-15*time.Minute)
	analysisBody, _ := json.Marshal(query)
	analysisResponse := request(h, http.MethodPost, "/api/analyze", string(analysisBody), true)
	if analysisResponse.Code != http.StatusOK {
		t.Fatalf("analyze status=%d body=%s", analysisResponse.Code, analysisResponse.Body.String())
	}
	var analysis audit.Analysis
	if err := json.Unmarshal(analysisResponse.Body.Bytes(), &analysis); err != nil {
		t.Fatal(err)
	}
	if len(analysis.APIs) != 6 || len(analysis.Sources) != 2 {
		t.Fatalf("analysis response cohorts/sources=%d/%d", len(analysis.APIs), len(analysis.Sources))
	}
	var cohort *audit.Comparison
	for i := range analysis.APIs {
		if analysis.APIs[i].Controller == "Payroll" && analysis.APIs[i].SourceID == "demo-west" {
			cohort = &analysis.APIs[i]
			break
		}
	}
	if cohort == nil {
		t.Fatal("payroll cohort missing")
	}
	pageBody, _ := json.Marshal(audit.PageInput{Query: query, APIKey: cohort.Key, Limit: 1})
	pageResponse := request(h, http.MethodPost, "/api/requests", string(pageBody), true)
	if pageResponse.Code != http.StatusOK {
		t.Fatalf("requests status=%d body=%s", pageResponse.Code, pageResponse.Body.String())
	}
	var page audit.Page
	if err := json.Unmarshal(pageResponse.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Rows) != 1 || page.NextCursor == "" {
		t.Fatalf("first page rows=%d next=%q", len(page.Rows), page.NextCursor)
	}
	payloadBody, _ := json.Marshal(audit.PayloadInput{SourceID: page.Rows[0].SourceID, ID: page.Rows[0].ID})
	payloadResponse := request(h, http.MethodPost, "/api/payload", string(payloadBody), true)
	if payloadResponse.Code != http.StatusOK || !strings.Contains(payloadResponse.Body.String(), "Illustrative demo") {
		t.Fatalf("payload status=%d body=%s", payloadResponse.Code, payloadResponse.Body.String())
	}
	exportBody, _ := json.Marshal(audit.PageInput{Query: query, APIKey: cohort.Key, Limit: 100})
	exportResponse := request(h, http.MethodPost, "/api/export", string(exportBody), true)
	if exportResponse.Code != http.StatusOK || exportResponse.Header().Get("X-Export-Limit") != "10000" || !strings.Contains(exportResponse.Body.String(), "initiatedAt") {
		t.Fatalf("export status=%d bodyPrefix=%q", exportResponse.Code, exportResponse.Body.String()[:min(120, len(exportResponse.Body.String()))])
	}
}
func TestSourceDiagnosticFieldsAreCuratedAcrossRoutes(t *testing.T) {
	for _, route := range []string{"/api/analyze", "/api/requests"} {
		t.Run(route, func(t *testing.T) {
			sources := []audit.SourceResult{
				{SourceID: "unknown", Error: "mongodb://user:secret@host", ErrorCode: "mongodb://user:secret@host"},
				{SourceID: "success", OK: true, Error: "secret", ErrorCode: "secret"},
				{SourceID: "timeout", Error: "secret", ErrorCode: "query_timeout"},
				{SourceID: "permission", Error: "secret", ErrorCode: "permission_denied"},
				{SourceID: "cancelled", Error: "secret", ErrorCode: "query_cancelled"},
				{SourceID: "removed", Error: "secret", ErrorCode: "source_unavailable"},
				{SourceID: "capacity", Error: "secret", ErrorCode: "capacity_timeout"},
			}
			repo := &fakeRepository{analysis: audit.Analysis{Sources: sources}, requestPages: []audit.Page{{Sources: sources}}}
			var body []byte
			if route == "/api/analyze" {
				body, _ = json.Marshal(testQuery())
			} else {
				body, _ = json.Marshal(audit.PageInput{Query: testQuery(), Limit: 10})
			}
			res := request(New(repo, nil, testToken, false), http.MethodPost, route, string(body), true)
			if res.Code != 200 {
				t.Fatalf("status %d", res.Code)
			}
			if strings.Contains(res.Body.String(), "secret") || strings.Contains(res.Body.String(), "mongodb://") {
				t.Fatal("private diagnostic reflected")
			}
			var decoded struct {
				Sources []audit.SourceResult `json:"sources"`
			}
			if err := json.Unmarshal(res.Body.Bytes(), &decoded); err != nil {
				t.Fatal(err)
			}
			got := decoded.Sources
			if got[6].ErrorCode != "capacity_timeout" {
				t.Fatal("admission diagnostic lost")
			}
			if got[4].ErrorCode != "query_cancelled" || got[5].ErrorCode != "source_unavailable" {
				t.Fatal("specific diagnostics lost")
			}
			if got[0].ErrorCode != "query_failed" || got[1].ErrorCode != "" || got[1].Error != "" || got[2].ErrorCode != "query_timeout" || got[3].ErrorCode != "permission_denied" {
				t.Fatalf("bad normalized sources: %#v", got)
			}
			if got[2].Error != audit.SourceQueryErrorMessage("query_timeout") || got[3].Error != audit.SourceQueryErrorMessage("permission_denied") {
				t.Fatal("known diagnostic changed")
			}
		})
	}
}

type operationDeadlineRepository struct {
	*fakeRepository
	remaining time.Duration
}

func (f *operationDeadlineRepository) Analyze(ctx context.Context, q audit.Query) (audit.Analysis, error) {
	d, ok := ctx.Deadline()
	if ok {
		f.remaining = time.Until(d)
	}
	return audit.Analysis{Query: q}, nil
}
func TestAnalysisHTTPBudgetIsFiveMinutes(t *testing.T) {
	repo := &operationDeadlineRepository{fakeRepository: &fakeRepository{}}
	body, _ := json.Marshal(testQuery())
	res := request(New(repo, nil, testToken, false), http.MethodPost, "/api/analyze", string(body), true)
	if res.Code != http.StatusOK || repo.remaining < 299*time.Second || repo.remaining > 300*time.Second {
		t.Fatalf("status %d HTTP budget %v", res.Code, repo.remaining)
	}
}

func TestProcessingLimitsHaveSpecificSafeExplanations(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{audit.ErrSummaryRowLimit, "40000 summary rows"},
		{audit.ErrSummaryBytesLimit, "16 MiB memory budget"},
		{audit.ErrAnalysisBytesLimit, "16 MiB display budget"},
	} {
		w := httptest.NewRecorder()
		writeRepoError(w, errors.Join(audit.ErrLimit, tc.err, errors.New("mongodb://user:secret@private")))
		if w.Code != http.StatusRequestEntityTooLarge || !strings.Contains(w.Body.String(), tc.want) || strings.Contains(w.Body.String(), "secret") {
			t.Fatalf("unsafe or incorrect limit response: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestRequestsStateFilterOverHTTP(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	h := New(audit.NewDemo(now), nil, testToken, true)
	query := testQuery()
	query.SourceIDs = []string{"demo-west"}
	query.Start, query.End = now.Add(-15*time.Minute), now
	query.BaselineStart, query.BaselineEnd = now.Add(-30*time.Minute), now.Add(-15*time.Minute)
	page := func(state string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(audit.PageInput{Query: query, State: state, Limit: 5})
		return request(h, http.MethodPost, "/api/requests", string(body), true)
	}
	// A query-wide page (no API key) with a state returns rows that carry the id and state.
	res := page("complete")
	if res.Code != http.StatusOK {
		t.Fatalf("complete status=%d body=%s", res.Code, res.Body.String())
	}
	var got audit.Page
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 5 {
		t.Fatalf("rows=%d, want 5", len(got.Rows))
	}
	for _, row := range got.Rows {
		if row.ID == "" || row.State != "valid" {
			t.Fatalf("row id=%q state=%q, want an id and state valid", row.ID, row.State)
		}
	}
	// Nothing in the demo is incomplete, so that state is a valid, empty page.
	res = page("incomplete")
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"rows":[]`) {
		t.Fatalf("incomplete status=%d body=%s", res.Code, res.Body.String())
	}
	// An unknown state is a client error on the page and the export route.
	if res = page("bogus"); res.Code != http.StatusBadRequest {
		t.Fatalf("unknown state status=%d body=%s", res.Code, res.Body.String())
	}
	body, _ := json.Marshal(audit.PageInput{Query: query, State: "bogus", Limit: 5})
	if res = request(h, http.MethodPost, "/api/export", string(body), true); res.Code != http.StatusBadRequest {
		t.Fatalf("export unknown state status=%d", res.Code)
	}
}

func proofRequest(h http.Handler, nonce string, local net.Addr) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, sessionproof.Path+"?n="+nonce, nil)
	req.Host = "127.0.0.1:8765"
	if local != nil {
		req = req.WithContext(context.WithValue(req.Context(), http.LocalAddrContextKey, local))
	}
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	return res
}

// TestSessionProofIsBoundToTokenAndListeningAddressWithoutRevealingIt is the
// M-A regression: the proof needs no token, never returns it, and only verifies
// for this server's own listening address.
func TestSessionProofIsBoundToTokenAndListeningAddressWithoutRevealingIt(t *testing.T) {
	srv := httptest.NewServer(New(audit.NewDemo(time.Now()), nil, testToken, true))
	defer srv.Close()
	nonce, _ := sessionproof.NewNonce()
	res, err := http.Get(srv.URL + sessionproof.Path + "?n=" + nonce)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(res.Body)
	res.Body.Close()
	var body struct{ Proof string }
	if res.StatusCode != http.StatusOK || json.Unmarshal(data, &body) != nil || res.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("status=%d body=%s", res.StatusCode, data)
	}
	if strings.Contains(string(data), testToken) {
		t.Fatalf("proof response leaked the token: %s", data)
	}
	addr := srv.Listener.Addr().String()
	if !sessionproof.Verify(testToken, addr, nonce, body.Proof) {
		t.Fatal("proof does not verify for the listening address")
	}
	host, port, _ := net.SplitHostPort(addr)
	other := net.JoinHostPort(host, port+"1")
	if sessionproof.Verify(testToken, other, nonce, body.Proof) || sessionproof.Verify(testToken+"x", addr, nonce, body.Proof) {
		t.Fatal("proof verified for another address or token")
	}
}

func TestSessionProofRejectsBadNoncesMethodsAndIsRateLimited(t *testing.T) {
	h := New(audit.NewDemo(time.Now()), nil, testToken, true).(*handler)
	local := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8765}
	for _, nonce := range []string{"", "abc", strings.Repeat("A", 64), strings.Repeat("g", 64), strings.Repeat("a", 65)} {
		if res := proofRequest(h, nonce, local); res.Code != http.StatusBadRequest {
			t.Errorf("nonce %q status=%d", nonce, res.Code)
		}
	}
	nonce, _ := sessionproof.NewNonce()
	if res := proofRequest(h, nonce, nil); res.Code != http.StatusBadRequest {
		t.Errorf("missing local address status=%d", res.Code)
	}
	if res := request(h, http.MethodPost, sessionproof.Path+"?n="+nonce, "", false); res.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST status=%d", res.Code)
	}
	clock := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	h.proofs.now = func() time.Time { return clock }
	for i := 0; i < sessionProofsPerWindow; i++ {
		if res := proofRequest(h, nonce, local); res.Code != http.StatusOK {
			t.Fatalf("proof %d status=%d", i+1, res.Code)
		}
	}
	if res := proofRequest(h, nonce, local); res.Code != http.StatusTooManyRequests {
		t.Fatalf("over-limit status=%d", res.Code)
	}
	clock = clock.Add(connectionTestWindow)
	if res := proofRequest(h, nonce, local); res.Code != http.StatusOK {
		t.Fatalf("limit did not reset: %d", res.Code)
	}
}

// TestSourceAddsAreRateLimited is the L-C regression: adding a source dials the
// server, so it is capped like connection tests.
func TestSourceAddsAreRateLimited(t *testing.T) {
	repo := &sourceCaptureRepository{fakeRepository: &fakeRepository{}}
	h := New(repo, nil, testToken, false).(*handler)
	clock := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	h.adds.now = func() time.Time { return clock }
	body, _ := json.Marshal(audit.SourceInput{Label: "a", URI: "mongodb://127.0.0.1:27017", Database: "events", Collection: "audit"})
	for i := 0; i < sourceAddsPerWindow; i++ {
		if res := request(h, http.MethodPost, "/api/sources", string(body), true); res.Code == http.StatusTooManyRequests {
			t.Fatalf("add %d refused early", i+1)
		}
	}
	res := request(h, http.MethodPost, "/api/sources", string(body), true)
	if res.Code != http.StatusTooManyRequests || !strings.Contains(res.Body.String(), "Wait a minute") {
		t.Fatalf("over-limit status=%d body=%s", res.Code, res.Body.String())
	}
	clock = clock.Add(connectionTestWindow)
	if res := request(h, http.MethodPost, "/api/sources", string(body), true); res.Code == http.StatusTooManyRequests {
		t.Fatalf("limit did not reset: %s", res.Body.String())
	}
}
