package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"api-audit-explorer/internal/casestore"
)

// preparingCase is a case that says whether it is building its indexes.
type preparingCase struct {
	casestore.Case
	preparing bool
}

func (p preparingCase) Info() casestore.CaseInfo {
	return casestore.CaseInfo{ID: "c-test", Title: "Old case"}
}
func (p preparingCase) Preparing() bool { return p.preparing }

// W-34: the case view carries "preparing" so the page can say "Preparing this case
// so its charts open quickly" while an old case builds its indexes.
func TestCaseViewSaysPreparing(t *testing.T) {
	h := &handler{}
	for _, want := range []bool{true, false} {
		v := h.viewOf(context.Background(), preparingCase{preparing: want})
		if v.Preparing != want {
			t.Fatalf("view.Preparing = %v, want %v", v.Preparing, want)
		}
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		needle := `"preparing":false`
		if want {
			needle = `"preparing":true`
		}
		if !strings.Contains(string(raw), needle) {
			t.Fatalf("json %s lacks %s", raw, needle)
		}
	}
}

// A case that cannot build anything (the memory store) is not preparing, and the
// field is always there, so the page never has to guess.
func TestCaseViewOfAPlainCaseIsNotPreparing(t *testing.T) {
	e := tagEnv(t, casestore.NewMemory(), &fakeRepository{})
	id := e.newCase("plain case")
	res := e.get("/api/cases/" + id)
	if res.Code != http.StatusOK {
		t.Fatalf("get case = %d %s", res.Code, res.Body)
	}
	if !strings.Contains(res.Body.String(), `"preparing":false`) {
		t.Fatalf("case view lacks preparing:false: %s", res.Body)
	}
}
