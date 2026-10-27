package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/importer"
)

type viewDTO struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Tool     string         `json:"tool"`
	Settings map[string]any `json:"settings"`
}

type itemDTO struct {
	ID      string `json:"id"`
	List    string `json:"list"`
	Text    string `json:"text"`
	Suspect string `json:"suspect"`
	Order   int    `json:"order"`
}

func viewsOf(t *testing.T, e *caseEnv, id string) []viewDTO {
	t.Helper()
	res := e.get("/api/cases/" + id + "/views")
	if res.Code != http.StatusOK {
		t.Fatalf("get views = %d %s", res.Code, res.Body)
	}
	var out struct {
		Views []viewDTO `json:"views"`
	}
	decodeBody(t, res, &out)
	if out.Views == nil {
		t.Fatalf("views is null, want []: %s", res.Body)
	}
	return out.Views
}

func itemsOf(t *testing.T, e *caseEnv, id string) []itemDTO {
	t.Helper()
	res := e.get("/api/cases/" + id + "/checklist")
	if res.Code != http.StatusOK {
		t.Fatalf("get checklist = %d %s", res.Code, res.Body)
	}
	var out struct {
		Items []itemDTO `json:"items"`
	}
	decodeBody(t, res, &out)
	if out.Items == nil {
		t.Fatalf("items is null, want []: %s", res.Body)
	}
	return out.Items
}

func TestSavedViewsRoundTrip(t *testing.T) {
	forEachTagStore(t, func(t *testing.T, e *caseEnv, id string, _ string) {
		base := "/api/cases/" + id + "/views"
		if got := viewsOf(t, e, id); len(got) != 0 {
			t.Fatalf("fresh case has views: %v", got)
		}
		res := e.json(http.MethodPost, base, `{"name":"Busy routes","tool":"routes","settings":{"route":"Payroll.Run","minMs":500}}`)
		if res.Code != http.StatusOK {
			t.Fatalf("save = %d %s", res.Code, res.Body)
		}
		var v viewDTO
		decodeBody(t, res, &v)
		if !casestore.ValidPlanID(v.ID) || v.Settings["route"] != "Payroll.Run" {
			t.Fatalf("saved = %+v", v)
		}
		// An id replaces the view in place.
		res = e.json(http.MethodPost, base, fmt.Sprintf(`{"id":%q,"name":"Busy routes v2","tool":"routes","settings":{}}`, v.ID))
		if res.Code != http.StatusOK {
			t.Fatalf("replace = %d %s", res.Code, res.Body)
		}
		got := viewsOf(t, e, id)
		if len(got) != 1 || got[0].Name != "Busy routes v2" {
			t.Fatalf("after replace = %+v", got)
		}
		// A valid id that is not there is "view-not-found".
		res = e.json(http.MethodPost, base, `{"id":"`+strings.Repeat("a", 32)+`","name":"x","tool":"routes","settings":{}}`)
		if res.Code != http.StatusNotFound || failureOf(t, res).Reason != "view-not-found" {
			t.Errorf("unknown id = %d %s", res.Code, res.Body)
		}
		if res := e.json(http.MethodDelete, base+"/"+v.ID, ""); res.Code != http.StatusOK {
			t.Fatalf("delete = %d %s", res.Code, res.Body)
		}
		if res := e.json(http.MethodDelete, base+"/"+v.ID, ""); res.Code != http.StatusNotFound {
			t.Errorf("second delete = %d", res.Code)
		}
		if res := e.json(http.MethodDelete, base+"/not-an-id", ""); res.Code != http.StatusNotFound {
			t.Errorf("malformed id delete = %d", res.Code)
		}
		if got := viewsOf(t, e, id); len(got) != 0 {
			t.Errorf("views left: %v", got)
		}
	})
}

func TestSavedViewsRefuseWhatBreaksARule(t *testing.T) {
	forEachTagStore(t, func(t *testing.T, e *caseEnv, id string, _ string) {
		base := "/api/cases/" + id + "/views"
		bad := map[string]string{
			"no name":              `{"name":"","tool":"routes","settings":{}}`,
			"name too long":        `{"name":"` + strings.Repeat("n", 61) + `","tool":"routes","settings":{}}`,
			"control character":    `{"name":"a\u0001b","tool":"routes","settings":{}}`,
			"tool not a slug":      `{"name":"x","tool":"a b","settings":{}}`,
			"no tool":              `{"name":"x","settings":{}}`,
			"settings an array":    `{"name":"x","tool":"routes","settings":[1]}`,
			"settings a string":    `{"name":"x","tool":"routes","settings":"x"}`,
			"password key":         `{"name":"x","tool":"routes","settings":{"password":"p"}}`,
			"nested token key":     `{"name":"x","tool":"routes","settings":{"a":{"Api-Key":"p"}}}`,
			"pasted address":       `{"name":"x","tool":"routes","settings":{"note":"mongodb://h/db"}}`,
			"bad id shape":         `{"id":"nope","name":"x","tool":"routes","settings":{}}`,
			"unknown body field":   `{"name":"x","tool":"routes","settings":{},"extra":1}`,
			"not json":             `{`,
			"settings very nested": `{"name":"x","tool":"routes","settings":` + strings.Repeat(`{"a":`, 12) + `1` + strings.Repeat(`}`, 12) + `}`,
		}
		for name, body := range bad {
			res := e.json(http.MethodPost, base, body)
			if res.Code != http.StatusBadRequest {
				t.Errorf("%s = %d %s", name, res.Code, res.Body)
			}
			if strings.Contains(res.Body.String(), "mongodb://") {
				t.Errorf("%s: the answer repeats the input: %s", name, res.Body)
			}
		}
		big := `{"name":"x","tool":"routes","settings":{"a":"` + strings.Repeat("z", casestore.MaxViewSettings) + `"}}`
		res := e.json(http.MethodPost, base, big)
		if res.Code != http.StatusRequestEntityTooLarge || failureOf(t, res).Reason != "plan-too-large" {
			t.Errorf("big settings = %d %s", res.Code, res.Body)
		}
		if got := viewsOf(t, e, id); len(got) != 0 {
			t.Errorf("a refused view was kept: %v", got)
		}
	})
}

// The clumsy user saves 100 views, then keeps going to the cap.
func TestSavedViewsHundredThenTheCap(t *testing.T) {
	forEachTagStore(t, func(t *testing.T, e *caseEnv, id string, _ string) {
		base := "/api/cases/" + id + "/views"
		for i := 0; i < 100; i++ {
			body := fmt.Sprintf(`{"name":"View %d","tool":"routes","settings":{"n":%d}}`, i, i)
			if res := e.json(http.MethodPost, base, body); res.Code != http.StatusOK {
				t.Fatalf("view %d = %d %s", i, res.Code, res.Body)
			}
		}
		if got := viewsOf(t, e, id); len(got) != 100 {
			t.Fatalf("views = %d, want 100", len(got))
		}
		for i := 100; i < casestore.MaxSavedViews; i++ {
			if res := e.json(http.MethodPost, base, fmt.Sprintf(`{"name":"View %d","tool":"routes","settings":{}}`, i)); res.Code != http.StatusOK {
				t.Fatalf("view %d = %d %s", i, res.Code, res.Body)
			}
		}
		res := e.json(http.MethodPost, base, `{"name":"One too many","tool":"routes","settings":{}}`)
		if res.Code != http.StatusRequestEntityTooLarge || failureOf(t, res).Reason != "plan-full" {
			t.Fatalf("201st view = %d %s", res.Code, res.Body)
		}
		if got := viewsOf(t, e, id); len(got) != casestore.MaxSavedViews {
			t.Errorf("views = %d after the refusal, want %d", len(got), casestore.MaxSavedViews)
		}
		// Replacing at the cap still works.
		first := viewsOf(t, e, id)[0]
		res = e.json(http.MethodPost, base, fmt.Sprintf(`{"id":%q,"name":"Renamed","tool":"routes","settings":{}}`, first.ID))
		if res.Code != http.StatusOK {
			t.Errorf("replace at the cap = %d %s", res.Code, res.Body)
		}
	})
}

func TestChecklistLists(t *testing.T) {
	forEachTagStore(t, func(t *testing.T, e *caseEnv, id string, _ string) {
		base := "/api/cases/" + id + "/checklist"
		if got := itemsOf(t, e, id); len(got) != 0 {
			t.Fatalf("fresh case has items: %v", got)
		}
		var ids []string
		for _, text := range []string{"Look at the job schedule", "Ask who changed the window"} {
			res := e.json(http.MethodPost, base, `{"list":"todo","text":`+fmt.Sprintf("%q", text)+`,"suspect":"H2"}`)
			if res.Code != http.StatusOK {
				t.Fatalf("add = %d %s", res.Code, res.Body)
			}
			var it itemDTO
			decodeBody(t, res, &it)
			ids = append(ids, it.ID)
		}
		got := itemsOf(t, e, id)
		if len(got) != 2 || got[0].Text != "Look at the job schedule" || got[1].Order <= got[0].Order {
			t.Fatalf("order = %+v", got)
		}
		// Moving to another list keeps the text and goes last there.
		res := e.json(http.MethodPut, base+"/"+ids[0], `{"list":"doing","text":"Look at the job schedule","suspect":"H2"}`)
		if res.Code != http.StatusOK {
			t.Fatalf("move = %d %s", res.Code, res.Body)
		}
		got = itemsOf(t, e, id)
		if got[0].List != "todo" || got[1].List != "doing" {
			t.Fatalf("after move = %+v", got)
		}
		// An edit that stays in its list keeps its place.
		res = e.json(http.MethodPut, base+"/"+ids[1], `{"list":"todo","text":"Ask who changed the maintenance window"}`)
		if res.Code != http.StatusOK {
			t.Fatalf("edit = %d %s", res.Code, res.Body)
		}
		if got = itemsOf(t, e, id); got[0].Text != "Ask who changed the maintenance window" || got[0].Suspect != "" {
			t.Fatalf("after edit = %+v", got)
		}
		for name, body := range map[string]string{
			"no list":       `{"text":"x"}`,
			"unknown list":  `{"list":"done","text":"x"}`,
			"no text":       `{"list":"todo","text":"  "}`,
			"text too long": `{"list":"todo","text":"` + strings.Repeat("t", 301) + `"}`,
			"bad suspect":   `{"list":"todo","text":"x","suspect":"a b"}`,
			"negative":      `{"list":"todo","text":"x","order":-1}`,
			"unknown field": `{"list":"todo","text":"x","id":"y"}`,
		} {
			if res := e.json(http.MethodPost, base, body); res.Code != http.StatusBadRequest {
				t.Errorf("%s = %d %s", name, res.Code, res.Body)
			}
		}
		if res := e.json(http.MethodPut, base+"/"+strings.Repeat("b", 32), `{"list":"todo","text":"x"}`); res.Code != http.StatusNotFound || failureOf(t, res).Reason != "item-not-found" {
			t.Errorf("unknown item = %d %s", res.Code, res.Body)
		}
		if res := e.json(http.MethodDelete, base+"/"+ids[0], ""); res.Code != http.StatusOK {
			t.Errorf("delete = %d %s", res.Code, res.Body)
		}
		if res := e.json(http.MethodDelete, base+"/"+ids[0], ""); res.Code != http.StatusNotFound {
			t.Errorf("second delete = %d", res.Code)
		}
		if len(itemsOf(t, e, id)) != 1 {
			t.Errorf("items left = %v", itemsOf(t, e, id))
		}
	})
}

func TestPlanRoutesOnAClosedCaseReadButDoNotWrite(t *testing.T) {
	forEachTagStore(t, func(t *testing.T, e *caseEnv, id string, _ string) {
		if res := e.json(http.MethodPost, "/api/cases/"+id+"/views", `{"name":"Kept","tool":"routes","settings":{}}`); res.Code != http.StatusOK {
			t.Fatal(res.Body)
		}
		if res := e.json(http.MethodPost, "/api/cases/"+id+"/checklist", `{"list":"todo","text":"Kept"}`); res.Code != http.StatusOK {
			t.Fatal(res.Body)
		}
		if res := e.json(http.MethodPost, "/api/cases/"+id+"/close", ""); res.Code != http.StatusOK {
			t.Fatalf("close = %d %s", res.Code, res.Body)
		}
		if len(viewsOf(t, e, id)) != 1 || len(itemsOf(t, e, id)) != 1 {
			t.Error("a closed case lost its views or items on read")
		}
		for name, res := range map[string]*httptest.ResponseRecorder{
			"save view": e.json(http.MethodPost, "/api/cases/"+id+"/views", `{"name":"New","tool":"routes","settings":{}}`),
			"add item":  e.json(http.MethodPost, "/api/cases/"+id+"/checklist", `{"list":"todo","text":"New"}`),
			"delete":    e.json(http.MethodDelete, "/api/cases/"+id+"/views/"+viewsOf(t, e, id)[0].ID, ""),
		} {
			if res.Code != http.StatusConflict || failureOf(t, res).Reason != "case-closed" {
				t.Errorf("%s on a closed case = %d %s", name, res.Code, res.Body)
			}
		}
	})
}

func TestPlanRoutesStatusMapping(t *testing.T) {
	e := tagEnv(t, casestore.NewMemory(), &fakeRepository{})
	id := e.newCase("plan")
	for _, p := range []string{"/views", "/checklist"} {
		if res := e.json(http.MethodPut, "/api/cases/"+id+p, `{}`); res.Code != http.StatusMethodNotAllowed {
			t.Errorf("PUT %s = %d", p, res.Code)
		}
		if res := request(e.h, http.MethodGet, "/api/cases/"+id+p, "", false); res.Code != http.StatusUnauthorized {
			t.Errorf("no token %s = %d", p, res.Code)
		}
		if res := e.get("/api/cases/not-a-case" + p); res.Code != http.StatusNotFound {
			t.Errorf("unknown case %s = %d", p, res.Code)
		}
		if res := e.get("/api/cases/" + strings.Repeat("a", 26) + p); res.Code != http.StatusNotFound {
			t.Errorf("absent case %s = %d", p, res.Code)
		}
	}
	if res := e.json(http.MethodGet, "/api/cases/"+id+"/views/"+strings.Repeat("a", 32), ""); res.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET one view = %d", res.Code)
	}
	req, _ := http.NewRequest(http.MethodPost, "/api/cases/"+id+"/views", strings.NewReader("name=x"))
	req.Header.Set("X-Audit-Token", testToken)
	req.Header.Set("Content-Type", "text/plain")
	if res := e.serve(req); res.Code != http.StatusUnsupportedMediaType {
		t.Errorf("text body = %d", res.Code)
	}
}

func TestPlanRoutesAreOffInThePlainDemo(t *testing.T) {
	h := New(audit.NewDemo(time.Now()), testAssets(), testToken, true,
		WithCases(casestore.NewMemory(), mustHosts(t, hostsDoc), importer.NewRegistry()))
	e := &caseEnv{t: t, h: h}
	for _, p := range []string{"/views", "/checklist"} {
		if res := e.get("/api/cases/" + strings.Repeat("a", 26) + p); res.Code != http.StatusServiceUnavailable {
			t.Errorf("demo %s = %d", p, res.Code)
		}
	}
	if res := e.json(http.MethodPost, "/api/cases/"+strings.Repeat("a", 26)+"/report", `{"format":"html"}`); res.Code != http.StatusServiceUnavailable {
		t.Errorf("demo report = %d", res.Code)
	}
}
