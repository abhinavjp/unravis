package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/casestore/sqlite"
	"api-audit-explorer/internal/casestore/storetest"
	"api-audit-explorer/internal/importer"
)

// tagStores gives each test a memory and a SQLite store; root is the SQLite
// folder (for the byte scan).
func tagStores(t *testing.T) map[string]func() (casestore.Store, string) {
	return map[string]func() (casestore.Store, string){
		"memory": func() (casestore.Store, string) { return casestore.NewMemory(), "" },
		"sqlite": func() (casestore.Store, string) {
			root := filepath.Join(t.TempDir(), "cases")
			s, err := sqlite.New(sqlite.Options{Root: root, Protector: storetest.RandomProtector(), MinFreeBytes: -1})
			if err != nil {
				t.Fatal(err)
			}
			return s, root
		},
	}
}

// tagEnv builds a server over the store with a fake Mongo connection tester.
func tagEnv(t *testing.T, store casestore.Store, repo audit.Repository, opts ...Option) *caseEnv {
	t.Helper()
	options := append([]Option{
		WithCases(store, mustHosts(t, hostsDoc), importer.NewRegistry(toyImporter{name: "toy", score: 90})),
	}, opts...)
	return &caseEnv{t: t, store: store, h: New(repo, testAssets(), testToken, false, options...)}
}

func forEachTagStore(t *testing.T, fn func(t *testing.T, e *caseEnv, id string, root string)) {
	for name, mk := range tagStores(t) {
		t.Run(name, func(t *testing.T) {
			store, root := mk()
			e := tagEnv(t, store, &fakeRepository{})
			fn(t, e, e.newCase("tags"), root)
		})
	}
}

func envsOf(t *testing.T, e *caseEnv, id string) []string {
	t.Helper()
	res := e.get("/api/cases/" + id + "/environments")
	if res.Code != http.StatusOK {
		t.Fatalf("get environments = %d %s", res.Code, res.Body)
	}
	var out environmentsDTO
	decodeBody(t, res, &out)
	if out.Environments == nil {
		t.Fatalf("environments is null, want []: %s", res.Body)
	}
	return out.Environments
}

func sourcesOf(t *testing.T, e *caseEnv, path string) []sourceTagDTO {
	t.Helper()
	res := e.get(path)
	if res.Code != http.StatusOK {
		t.Fatalf("get %s = %d %s", path, res.Code, res.Body)
	}
	var out sourceTagsDTO
	decodeBody(t, res, &out)
	if out.Sources == nil {
		t.Fatalf("sources is null, want []: %s", res.Body)
	}
	return out.Sources
}

func TestTagsHappyPath(t *testing.T) {
	forEachTagStore(t, func(t *testing.T, e *caseEnv, id string, _ string) {
		if got := envsOf(t, e, id); len(got) != 0 {
			t.Fatalf("new case environments = %v", got)
		}
		res := e.json(http.MethodPut, "/api/cases/"+id+"/environments", `{"environments":[" Production ","staging","PRODUCTION",""]}`)
		if res.Code != http.StatusOK {
			t.Fatalf("put environments = %d %s", res.Code, res.Body)
		}
		var put environmentsDTO
		decodeBody(t, res, &put)
		if strings.Join(put.Environments, "|") != "Production|staging" {
			t.Fatalf("put answered %v", put.Environments)
		}
		if got := envsOf(t, e, id); strings.Join(got, "|") != "Production|staging" {
			t.Fatalf("environments = %v", got)
		}

		res = e.json(http.MethodPost, "/api/cases/"+id+"/sources", `{"kind":"iis","label":"web-a logs","env":"Production","portal":"Admin","api":"Orders"}`)
		if res.Code != http.StatusCreated {
			t.Fatalf("post source = %d %s", res.Code, res.Body)
		}
		var added sourceTagDTO
		decodeBody(t, res, &added)
		if added.ID < 1 || added.Kind != "iis" || added.Label != "web-a logs" || added.Env != "Production" ||
			added.Portal != "Admin" || added.API != "Orders" || added.Created.IsZero() {
			t.Fatalf("added = %+v", added)
		}
		list := sourcesOf(t, e, "/api/cases/"+id+"/sources")
		if len(list) != 1 || list[0] != added {
			t.Fatalf("sources = %+v, want [%+v]", list, added)
		}

		view := e.get("/api/cases/" + id)
		if view.Code != http.StatusOK {
			t.Fatalf("get case = %d %s", view.Code, view.Body)
		}
		var cv caseViewDTO
		decodeBody(t, view, &cv)
		if cv.ID != id || cv.Title != "tags" || cv.Envs != 2 || cv.Sources != 1 {
			t.Fatalf("case view = %+v", cv)
		}

		del := e.json(http.MethodDelete, "/api/cases/"+id+"/sources/"+strconv.FormatInt(added.ID, 10), "")
		if del.Code != http.StatusOK || !strings.Contains(del.Body.String(), "were not deleted") {
			t.Fatalf("delete source = %d %s", del.Code, del.Body)
		}
		if len(sourcesOf(t, e, "/api/cases/"+id+"/sources")) != 0 {
			t.Fatal("source still listed after delete")
		}
		// Removing it again is not an error.
		if again := e.json(http.MethodDelete, "/api/cases/"+id+"/sources/"+strconv.FormatInt(added.ID, 10), ""); again.Code != http.StatusOK {
			t.Fatalf("second delete = %d", again.Code)
		}
		if bad := e.json(http.MethodDelete, "/api/cases/"+id+"/sources/abc", ""); bad.Code != http.StatusNotFound {
			t.Fatalf("non-numeric source id = %d", bad.Code)
		}
	})
}

func TestDeletingASourceTagKeepsImportedEvents(t *testing.T) {
	forEachTagStore(t, func(t *testing.T, e *caseEnv, id string, _ string) {
		res := e.upload(commitPath(id), ackFields, &upload{"a.csv", toyCSV(toyRow(1, "web-a", "alpha", 5), toyRow(2, "web-a", "beta", 7))})
		if res.Code != http.StatusCreated {
			t.Fatalf("import = %d %s", res.Code, res.Body)
		}
		var imp importAnswer
		decodeBody(t, res, &imp)
		body := `{"kind":"toy","label":"toy logs","importId":` + strconv.FormatInt(imp.Record.ID, 10) + `}`
		add := e.json(http.MethodPost, "/api/cases/"+id+"/sources", body)
		if add.Code != http.StatusCreated {
			t.Fatalf("post source = %d %s", add.Code, add.Body)
		}
		var tag sourceTagDTO
		decodeBody(t, add, &tag)
		if tag.ImportID != imp.Record.ID {
			t.Fatalf("importId = %d, want %d", tag.ImportID, imp.Record.ID)
		}
		before, _ := e.eventsOf(id, "")
		if len(before) != 2 {
			t.Fatalf("events before = %d", len(before))
		}
		if del := e.json(http.MethodDelete, "/api/cases/"+id+"/sources/"+strconv.FormatInt(tag.ID, 10), ""); del.Code != http.StatusOK {
			t.Fatalf("delete = %d %s", del.Code, del.Body)
		} else if !strings.Contains(del.Body.String(), `"eventsDeleted":false`) {
			t.Fatalf("delete body = %s", del.Body)
		}
		after, _ := e.eventsOf(id, "")
		if len(after) != 2 {
			t.Fatalf("events after = %d, want 2", len(after))
		}
		if recs := e.importsOf(id); len(recs) != 1 {
			t.Fatalf("imports after = %d, want 1", len(recs))
		}
	})
}

func TestTagRequestsRefuseUnknownFieldsAndBadBodies(t *testing.T) {
	forEachTagStore(t, func(t *testing.T, e *caseEnv, id string, _ string) {
		for name, c := range map[string]struct{ method, path, body string }{
			"envs unknown":    {http.MethodPut, "/environments", `{"environments":["a"],"extra":1}`},
			"source unknown":  {http.MethodPost, "/sources", `{"kind":"iis","secret":"x"}`},
			"source bad json": {http.MethodPost, "/sources", `{"kind":`},
			"envs two values": {http.MethodPut, "/environments", `{"environments":[]} {}`},
		} {
			res := e.json(c.method, "/api/cases/"+id+c.path, c.body)
			if res.Code != http.StatusBadRequest {
				t.Errorf("%s = %d %s, want 400", name, res.Code, res.Body)
			}
			if strings.Contains(res.Body.String(), "secret") {
				t.Errorf("%s: body echoed input: %s", name, res.Body)
			}
		}
		// Wrong media type is refused by the shared decoder.
		req := httptest.NewRequest(http.MethodPut, "/api/cases/"+id+"/environments", strings.NewReader(`{"environments":[]}`))
		req.Header.Set("X-Audit-Token", testToken)
		req.Header.Set("Content-Type", "text/plain")
		if res := e.serve(req); res.Code != http.StatusUnsupportedMediaType {
			t.Errorf("text/plain = %d", res.Code)
		}
		if len(sourcesOf(t, e, "/api/cases/"+id+"/sources")) != 0 {
			t.Error("a refused request stored a source")
		}
	})
}

func TestTagBoundsAnswer422WithoutEchoingInput(t *testing.T) {
	forEachTagStore(t, func(t *testing.T, e *caseEnv, id string, _ string) {
		long := strings.Repeat("L", casestore.MaxTagTextChars+1)
		for name, body := range map[string]string{
			"long label": `{"kind":"iis","label":"` + long + `"}`,
			"long kind":  `{"kind":"` + long + `"}`,
			"blank kind": `{"kind":"  "}`,
			"control":    `{"kind":"iis","label":"a\u0007b"}`,
			"class":      `{"kind":"iis","classification":"maybe"}`,
		} {
			res := e.json(http.MethodPost, "/api/cases/"+id+"/sources", body)
			if res.Code != http.StatusUnprocessableEntity {
				t.Errorf("%s = %d %s, want 422", name, res.Code, res.Body)
				continue
			}
			if strings.Contains(res.Body.String(), "LLLL") || strings.Contains(res.Body.String(), "maybe") {
				t.Errorf("%s: error echoed input: %s", name, res.Body)
			}
		}
		longEnv := strings.Repeat("E", casestore.MaxEnvNameChars+1)
		res := e.json(http.MethodPut, "/api/cases/"+id+"/environments", `{"environments":["`+longEnv+`"]}`)
		if res.Code != http.StatusUnprocessableEntity {
			t.Errorf("long environment = %d %s", res.Code, res.Body)
		}
		if len(sourcesOf(t, e, "/api/cases/"+id+"/sources")) != 0 {
			t.Error("a refused source was stored")
		}
	})
}

func TestThirtyThirdEnvironmentAndTwoHundredFirstSourceAreRefused(t *testing.T) {
	forEachTagStore(t, func(t *testing.T, e *caseEnv, id string, _ string) {
		names := make([]string, 0, casestore.MaxEnvs+1)
		for i := 0; i < casestore.MaxEnvs; i++ {
			names = append(names, "env-"+strconv.Itoa(i))
		}
		put := func(list []string) *httptest.ResponseRecorder {
			b, _ := json.Marshal(environmentsDTO{Environments: list})
			return e.json(http.MethodPut, "/api/cases/"+id+"/environments", string(b))
		}
		if res := put(names); res.Code != http.StatusOK {
			t.Fatalf("32 environments = %d %s", res.Code, res.Body)
		}
		res := put(append(names, "env-32"))
		if res.Code != http.StatusUnprocessableEntity || !strings.Contains(res.Body.String(), "32 environments") {
			t.Fatalf("33rd environment = %d %s", res.Code, res.Body)
		}
		if got := envsOf(t, e, id); len(got) != casestore.MaxEnvs {
			t.Fatalf("environments after refusal = %d", len(got))
		}
		for i := 0; i < casestore.MaxSources; i++ {
			r := e.json(http.MethodPost, "/api/cases/"+id+"/sources", `{"kind":"iis","label":"s`+strconv.Itoa(i)+`"}`)
			if r.Code != http.StatusCreated {
				t.Fatalf("source %d = %d %s", i, r.Code, r.Body)
			}
		}
		r := e.json(http.MethodPost, "/api/cases/"+id+"/sources", `{"kind":"iis","label":"one too many"}`)
		if r.Code != http.StatusUnprocessableEntity || !strings.Contains(r.Body.String(), "200 sources") {
			t.Fatalf("201st source = %d %s", r.Code, r.Body)
		}
	})
}

func TestClosedCaseRefusesTagWritesButStillReads(t *testing.T) {
	forEachTagStore(t, func(t *testing.T, e *caseEnv, id string, _ string) {
		e.json(http.MethodPut, "/api/cases/"+id+"/environments", `{"environments":["prod"]}`)
		added := e.json(http.MethodPost, "/api/cases/"+id+"/sources", `{"kind":"iis","label":"a"}`)
		var tag sourceTagDTO
		decodeBody(t, added, &tag)
		if res := e.json(http.MethodPost, "/api/cases/"+id+"/close", ""); res.Code != http.StatusOK {
			t.Fatalf("close = %d %s", res.Code, res.Body)
		}
		for name, res := range map[string]*httptest.ResponseRecorder{
			"put":    e.json(http.MethodPut, "/api/cases/"+id+"/environments", `{"environments":["x"]}`),
			"post":   e.json(http.MethodPost, "/api/cases/"+id+"/sources", `{"kind":"iis"}`),
			"delete": e.json(http.MethodDelete, "/api/cases/"+id+"/sources/"+strconv.FormatInt(tag.ID, 10), ""),
		} {
			if res.Code != http.StatusConflict || failureOf(t, res).Reason != "case-closed" {
				t.Errorf("%s on closed case = %d %s, want 409 case-closed", name, res.Code, res.Body)
			}
		}
		if got := envsOf(t, e, id); len(got) != 1 || got[0] != "prod" {
			t.Errorf("closed case environments = %v", got)
		}
		if len(sourcesOf(t, e, "/api/cases/"+id+"/sources")) != 1 {
			t.Error("closed case lost its source")
		}
		if res := e.get("/api/cases/" + id); res.Code != http.StatusOK {
			t.Errorf("get closed case = %d", res.Code)
		}
	})
}

func TestMalformedCaseIDIsTheSame404AsAMissingCase(t *testing.T) {
	forEachTagStore(t, func(t *testing.T, e *caseEnv, id string, _ string) {
		missing, err := casestore.NewID()
		if err != nil {
			t.Fatal(err)
		}
		type call struct{ method, suffix, body string }
		calls := []call{
			{http.MethodGet, "/environments", ""}, {http.MethodPut, "/environments", `{"environments":[]}`},
			{http.MethodGet, "/sources", ""}, {http.MethodPost, "/sources", `{"kind":"iis"}`},
			{http.MethodDelete, "/sources/1", ""}, {http.MethodGet, "/mongo-sources", ""}, {http.MethodGet, "", ""},
		}
		for _, c := range calls {
			ref := e.json(c.method, "/api/cases/"+string(missing)+c.suffix, c.body)
			if ref.Code != http.StatusNotFound {
				t.Errorf("%s %s on a missing case = %d %s", c.method, c.suffix, ref.Code, ref.Body)
			}
			for _, bad := range []string{"not-a-case-id", "..", "%2e%2e", "ABC", strings.Repeat("a", 200)} {
				res := e.json(c.method, "/api/cases/"+bad+c.suffix, c.body)
				if res.Code != http.StatusNotFound || res.Body.String() != ref.Body.String() {
					t.Errorf("%s %q%s = %d %s, want the missing-case 404 %s", c.method, bad, c.suffix, res.Code, res.Body, ref.Body)
				}
			}
		}
	})
}

func TestTagRoutesNeedTokenAndLocalOrigin(t *testing.T) {
	forEachTagStore(t, func(t *testing.T, e *caseEnv, id string, _ string) {
		for _, c := range []struct{ method, path, body string }{
			{http.MethodGet, "/environments", ""}, {http.MethodPut, "/environments", `{"environments":["a"]}`},
			{http.MethodGet, "/sources", ""}, {http.MethodPost, "/sources", `{"kind":"iis"}`},
			{http.MethodDelete, "/sources/1", ""}, {http.MethodGet, "/mongo-sources", ""}, {http.MethodGet, "", ""},
		} {
			req := httptest.NewRequest(c.method, "/api/cases/"+id+c.path, strings.NewReader(c.body))
			req.Header.Set("Content-Type", "application/json")
			if res := e.serve(req); res.Code != http.StatusUnauthorized {
				t.Errorf("%s %s without token = %d", c.method, c.path, res.Code)
			}
			req = httptest.NewRequest(c.method, "/api/cases/"+id+c.path, strings.NewReader(c.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Audit-Token", testToken)
			req.Header.Set("Origin", "http://evil.example")
			if res := e.serve(req); res.Code != http.StatusForbidden {
				t.Errorf("%s %s from a foreign origin = %d", c.method, c.path, res.Code)
			}
		}
		if got := envsOf(t, e, id); len(got) != 0 {
			t.Errorf("a refused request changed the case: %v", got)
		}
	})
}

func TestCaseViewCountsComeFromTheOpenedCase(t *testing.T) {
	forEachTagStore(t, func(t *testing.T, e *caseEnv, id string, _ string) {
		// A case that has never been tagged (as a migrated older case is) shows zero.
		var cv caseViewDTO
		decodeBody(t, e.get("/api/cases/"+id), &cv)
		if cv.Envs != 0 || cv.Sources != 0 || cv.Imports != 0 || cv.ID != id {
			t.Fatalf("fresh case view = %+v", cv)
		}
		e.json(http.MethodPut, "/api/cases/"+id+"/environments", `{"environments":["a","b","c"]}`)
		e.json(http.MethodPost, "/api/cases/"+id+"/sources", `{"kind":"iis"}`)
		e.json(http.MethodPost, "/api/cases/"+id+"/sources", `{"kind":"sql"}`)
		decodeBody(t, e.get("/api/cases/"+id), &cv)
		if cv.Envs != 3 || cv.Sources != 2 {
			t.Fatalf("case view = %+v", cv)
		}
		// The list keeps its field names and carries no counts.
		res := e.get("/api/cases")
		if strings.Contains(res.Body.String(), `"envs"`) || strings.Contains(res.Body.String(), `"sources"`) {
			t.Errorf("list changed shape: %s", res.Body)
		}
	})
}

func TestTagTextThatLooksLikeACredentialIsRefused(t *testing.T) {
	forEachTagStore(t, func(t *testing.T, e *caseEnv, id string, _ string) {
		for _, text := range []string{"mongodb://u:hunter2@db.example/x", "mongodb+srv://c.example", "https://user:hunter2@x.example", "password=hunter2", "pwd: hunter2"} {
			label, _ := json.Marshal(text)
			res := e.json(http.MethodPost, "/api/cases/"+id+"/sources", `{"kind":"iis","label":`+string(label)+`}`)
			if res.Code != http.StatusUnprocessableEntity || strings.Contains(res.Body.String(), "hunter2") {
				t.Errorf("label %q = %d %s", text, res.Code, res.Body)
			}
			res = e.json(http.MethodPut, "/api/cases/"+id+"/environments", `{"environments":[`+string(label)+`]}`)
			if res.Code != http.StatusUnprocessableEntity || strings.Contains(res.Body.String(), "hunter2") {
				t.Errorf("environment %q = %d %s", text, res.Code, res.Body)
			}
		}
		if len(sourcesOf(t, e, "/api/cases/"+id+"/sources")) != 0 || len(envsOf(t, e, id)) != 0 {
			t.Error("credential-like text was stored")
		}
	})
}

/* ------------------------------------------------------------------ mongo */

const (
	mongoPassword = "pw-zz-sentinel-47"
	mongoURI      = "mongodb://reader:" + mongoPassword + "@db.example.test:27017/?tls=true"
)

func mongoEnv(t *testing.T, store casestore.Store, names ...string) (*caseEnv, *connectionTestRepository, *time.Time) {
	t.Helper()
	repo := &connectionTestRepository{fakeRepository: &fakeRepository{}, result: audit.ConnectionResult{Databases: names}}
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	e := tagEnv(t, store, repo, func(h *handler) { h.mongoTests.now = func() time.Time { return now } })
	return e, repo, &now
}

func testMongo(t *testing.T, e *caseEnv) {
	t.Helper()
	body, _ := json.Marshal(audit.ConnectionInput{URI: mongoURI, Username: "reader", Password: mongoPassword, AuthSource: "admin"})
	res := e.json(http.MethodPost, "/api/connections/test", string(body))
	if res.Code != http.StatusOK {
		t.Fatalf("connection test = %d %s", res.Code, res.Body)
	}
}

func classify(e *caseEnv, id, db, class, env string) *httptest.ResponseRecorder {
	b, _ := json.Marshal(sourceTagBody{Kind: mongoDatabaseKind, Label: db, Classification: class, Env: env})
	return e.json(http.MethodPost, "/api/cases/"+id+"/sources", string(b))
}

func TestMongoClassificationRoundTrip(t *testing.T) {
	for name, mk := range tagStores(t) {
		t.Run(name, func(t *testing.T) {
			store, _ := mk()
			e, _, _ := mongoEnv(t, store, "auditdb", "appdata", "scratch")
			id := e.newCase("mongo")
			testMongo(t, e)
			for db, class := range map[string]string{"auditdb": "audit", "appdata": "other", "scratch": "unused"} {
				res := classify(e, id, db, class, "")
				if res.Code != http.StatusCreated {
					t.Fatalf("classify %s = %d %s", db, res.Code, res.Body)
				}
				var tag sourceTagDTO
				decodeBody(t, res, &tag)
				if tag.Kind != mongoDatabaseKind || tag.Label != db || tag.Classification != class {
					t.Fatalf("stored %+v", tag)
				}
			}
			audits := sourcesOf(t, e, "/api/cases/"+id+"/mongo-sources")
			if len(audits) != 1 || audits[0].Label != "auditdb" || audits[0].Classification != "audit" {
				t.Fatalf("mongo-sources = %+v", audits)
			}
			for class, db := range map[string]string{"other": "appdata", "unused": "scratch"} {
				got := sourcesOf(t, e, "/api/cases/"+id+"/mongo-sources?classification="+class)
				if len(got) != 1 || got[0].Label != db {
					t.Errorf("mongo-sources?classification=%s = %+v", class, got)
				}
			}
			if res := e.get("/api/cases/" + id + "/mongo-sources?classification=bogus"); res.Code != http.StatusBadRequest {
				t.Errorf("bad filter = %d", res.Code)
			}
			// The three rows are also ordinary source tags.
			if all := sourcesOf(t, e, "/api/cases/"+id+"/sources"); len(all) != 3 {
				t.Errorf("sources = %d, want 3", len(all))
			}
			// Changing the choice replaces it.
			if res := classify(e, id, "auditdb", "unused", ""); res.Code != http.StatusCreated {
				t.Fatalf("reclassify = %d %s", res.Code, res.Body)
			}
			if got := sourcesOf(t, e, "/api/cases/"+id+"/mongo-sources"); len(got) != 0 {
				t.Errorf("audit rows after reclassify = %+v", got)
			}
			if all := sourcesOf(t, e, "/api/cases/"+id+"/sources"); len(all) != 3 {
				t.Errorf("sources after reclassify = %d, want 3", len(all))
			}
		})
	}
}

func TestMongoClassificationNeedsATestedDatabaseAndAValidChoice(t *testing.T) {
	e, repo, now := mongoEnv(t, casestore.NewMemory(), "auditdb")
	id := e.newCase("mongo")

	if res := classify(e, id, "auditdb", "audit", ""); res.Code != http.StatusUnprocessableEntity || failureOf(t, res).Reason != "database-not-tested" {
		t.Fatalf("before any test = %d %s", res.Code, res.Body)
	}
	testMongo(t, e)
	for name, res := range map[string]*httptest.ResponseRecorder{
		"unlisted name": classify(e, id, "admin", "audit", ""),
		"free text":     classify(e, id, "anything I like", "audit", ""),
		"case differs":  classify(e, id, "AuditDB", "audit", ""),
		"empty label":   classify(e, id, "", "audit", ""),
		"uri as label":  classify(e, id, mongoURI, "audit", ""),
	} {
		if res.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s = %d %s, want 422", name, res.Code, res.Body)
		}
		if strings.Contains(res.Body.String(), "anything I like") || strings.Contains(res.Body.String(), mongoPassword) {
			t.Errorf("%s echoed input: %s", name, res.Body)
		}
	}
	for _, class := range []string{"", "maybe", "Audit"} {
		res := classify(e, id, "auditdb", class, "")
		if res.Code != http.StatusUnprocessableEntity || failureOf(t, res).Reason != "bad-classification" {
			t.Errorf("classification %q = %d %s", class, res.Code, res.Body)
		}
	}
	if got := sourcesOf(t, e, "/api/cases/"+id+"/sources"); len(got) != 0 {
		t.Fatalf("a refused choice was stored: %+v", got)
	}
	if res := classify(e, id, "auditdb", "audit", ""); res.Code != http.StatusCreated {
		t.Fatalf("tested name = %d %s", res.Code, res.Body)
	}
	// The record expires.
	*now = now.Add(mongoTestTTL + time.Second)
	if res := classify(e, id, "auditdb", "other", ""); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("after expiry = %d %s", res.Code, res.Body)
	}
	// A failed test records nothing.
	repo.result, repo.err = audit.ConnectionResult{Databases: []string{"other-db"}}, audit.ErrInvalidInput
	body, _ := json.Marshal(audit.ConnectionInput{URI: mongoURI})
	if res := e.json(http.MethodPost, "/api/connections/test", string(body)); res.Code == http.StatusOK {
		t.Fatalf("failing test = %d", res.Code)
	}
	if res := classify(e, id, "other-db", "audit", ""); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("name from a failed test = %d %s", res.Code, res.Body)
	}
}

func TestMongoTestRecordKeepsOnlyTheMostRecentResults(t *testing.T) {
	var m mongoTests
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	for i := 0; i < mongoTestKeep+2; i++ {
		m.record([]string{"db" + strconv.Itoa(i)})
		now = now.Add(time.Second)
	}
	if m.has("db0") || m.has("db1") {
		t.Error("oldest results were not dropped")
	}
	if !m.has("db2") || !m.has("db"+strconv.Itoa(mongoTestKeep+1)) {
		t.Error("recent results were dropped")
	}
}

func TestMongoConnectionStringNeverReachesRowsResponsesOrBytes(t *testing.T) {
	for name, mk := range tagStores(t) {
		t.Run(name, func(t *testing.T) {
			store, root := mk()
			e, _, _ := mongoEnv(t, store, "auditdb")
			id := e.newCase("mongo")
			testMongo(t, e)
			var responses bytes.Buffer
			note := func(res *httptest.ResponseRecorder) { responses.Write(res.Body.Bytes()) }
			// Even a client that puts the connection string in every field cannot store it.
			for _, body := range []string{
				`{"kind":"mongo-database","label":"` + mongoURI + `","classification":"audit"}`,
				`{"kind":"mongo-database","label":"auditdb","env":"` + mongoURI + `","classification":"audit"}`,
				`{"kind":"mongo-database","label":"auditdb","portal":"` + mongoURI + `","classification":"audit"}`,
				`{"kind":"mongo-database","label":"auditdb","api":"password=` + mongoPassword + `","classification":"audit"}`,
			} {
				res := e.json(http.MethodPost, "/api/cases/"+id+"/sources", body)
				note(res)
				if res.Code != http.StatusUnprocessableEntity {
					t.Errorf("body %.60s = %d", body, res.Code)
				}
			}
			note(classify(e, id, "auditdb", "audit", "Production"))
			note(e.get("/api/cases/" + id + "/sources"))
			note(e.get("/api/cases/" + id + "/mongo-sources"))
			note(e.get("/api/cases/" + id))
			note(e.get("/api/cases"))
			if bytes.Contains(responses.Bytes(), []byte(mongoPassword)) || bytes.Contains(responses.Bytes(), []byte("mongodb://")) {
				t.Fatalf("connection details in a response: %s", responses.String())
			}
			rows, err := mustTagger(t, e, id).Sources(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 1 {
				t.Fatalf("stored rows = %d, want 1", len(rows))
			}
			dump, _ := json.Marshal(rows)
			if bytes.Contains(dump, []byte(mongoPassword)) || bytes.Contains(dump, []byte("mongodb")) || bytes.Contains(dump, []byte("db.example.test")) {
				t.Fatalf("connection details in a stored row: %s", dump)
			}
			if root != "" {
				scanFolder(t, root, []byte(mongoPassword), []byte("mongodb://"), []byte("db.example.test"))
			}
		})
	}
}

func mustTagger(t *testing.T, e *caseEnv, id string) casestore.Tagger {
	t.Helper()
	c, err := e.store.Open(t.Context(), casestore.CaseID(id))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	tg, ok := c.(casestore.Tagger)
	if !ok {
		t.Fatal("case is not a Tagger")
	}
	return tg
}

func scanFolder(t *testing.T, root string, needles ...[]byte) {
	t.Helper()
	files := 0
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		files++
		data, err := os.ReadFile(path)
		if err != nil {
			return nil // a locked file is not one that holds plain text of ours
		}
		for _, n := range needles {
			if bytes.Contains(data, n) {
				t.Errorf("%s holds %q", path, n)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if files == 0 {
		t.Fatal("no files scanned")
	}
}

func TestMongoRoutesNeedTheToken(t *testing.T) {
	e, _, _ := mongoEnv(t, casestore.NewMemory(), "auditdb")
	id := e.newCase("mongo")
	testMongo(t, e)
	req := httptest.NewRequest(http.MethodPost, "/api/cases/"+id+"/sources", strings.NewReader(`{"kind":"mongo-database","label":"auditdb","classification":"audit"}`))
	req.Header.Set("Content-Type", "application/json")
	if res := e.serve(req); res.Code != http.StatusUnauthorized {
		t.Fatalf("post without token = %d", res.Code)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/cases/"+id+"/mongo-sources", nil)
	if res := e.serve(req); res.Code != http.StatusUnauthorized {
		t.Fatalf("mongo-sources without token = %d", res.Code)
	}
	if got := sourcesOf(t, e, "/api/cases/"+id+"/sources"); len(got) != 0 {
		t.Fatalf("a refused request stored %+v", got)
	}
}

func TestMongoClassificationOnAClosedCaseIs409(t *testing.T) {
	e, _, _ := mongoEnv(t, casestore.NewMemory(), "auditdb")
	id := e.newCase("mongo")
	testMongo(t, e)
	e.json(http.MethodPost, "/api/cases/"+id+"/close", "")
	if res := classify(e, id, "auditdb", "audit", ""); res.Code != http.StatusConflict {
		t.Fatalf("closed case = %d %s", res.Code, res.Body)
	}
}

func TestConnectionTestResponseIsUnchangedByTheRecord(t *testing.T) {
	e, repo, _ := mongoEnv(t, casestore.NewMemory(), "a", "b")
	body, _ := json.Marshal(audit.ConnectionInput{URI: mongoURI, Username: "u", Password: "p"})
	res := e.json(http.MethodPost, "/api/connections/test", string(body))
	if res.Code != http.StatusOK || strings.TrimSpace(res.Body.String()) != `{"databases":["a","b"]}` {
		t.Fatalf("test response = %d %s", res.Code, res.Body)
	}
	if repo.calls != 1 {
		t.Fatalf("repository called %d times", repo.calls)
	}
}
