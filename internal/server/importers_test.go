package server

import (
	"net/http"
	"strings"
	"testing"

	"api-audit-explorer/internal/importer"
)

type importersAnswer struct {
	Importers []struct {
		Name     string `json:"name"`
		Supplied []struct {
			Key     string   `json:"key"`
			Kind    string   `json:"kind"`
			Values  []string `json:"values"`
			Min     *float64 `json:"min"`
			Max     *float64 `json:"max"`
			Integer bool     `json:"integer"`
			MaxLen  int      `json:"maxLen"`
		} `json:"supplied"`
	} `json:"importers"`
}

func TestImportersListDeclaredSuppliedKeys(t *testing.T) {
	se := newSuppliedEnv(t)
	res := se.get("/api/importers")
	if res.Code != http.StatusOK {
		t.Fatalf("importers = %d %s", res.Code, res.Body)
	}
	var out importersAnswer
	decodeBody(t, res, &out)
	if len(out.Importers) != 1 || out.Importers[0].Name != "toy" {
		t.Fatalf("importers: %+v", out.Importers)
	}
	got := out.Importers[0].Supplied
	if len(got) != 3 || got[0].Key != "log-time" || got[1].Key != "pattern" || got[2].Key != "slowms" {
		t.Fatalf("supplied keys not sorted or incomplete: %+v", got)
	}
	if got[0].Kind != "enum" || strings.Join(got[0].Values, ",") != "utc,local" {
		t.Errorf("log-time: %+v", got[0])
	}
	if got[1].Kind != "text" || got[1].MaxLen != 60 {
		t.Errorf("pattern: %+v", got[1])
	}
	if got[2].Kind != "number" || got[2].Min == nil || *got[2].Min != 0 || got[2].Max == nil || *got[2].Max != 600000 || !got[2].Integer {
		t.Errorf("slowms: %+v", got[2])
	}
}

func TestImportersWithoutDeclaredKeysGiveAnEmptyList(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	res := e.get("/api/importers")
	if res.Code != http.StatusOK {
		t.Fatalf("importers = %d %s", res.Code, res.Body)
	}
	if !strings.Contains(res.Body.String(), `"supplied":[]`) {
		t.Errorf("an importer with no keys should list an empty array: %s", res.Body)
	}
}

func TestImportersTextSpecCapsAtTheServerLimit(t *testing.T) {
	dto, ok := toSuppliedDTO("pattern", importer.SuppliedSpec{Kind: importer.SuppliedText})
	if !ok || dto.MaxLen != importer.MaxSuppliedValue {
		t.Errorf("default text length: %+v", dto)
	}
	if _, ok := toSuppliedDTO("x", importer.SuppliedSpec{}); ok {
		t.Error("a spec of unknown kind should be left out")
	}
}

func TestImportersNeedTheTokenAndTheStore(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	if res := request(e.h, http.MethodGet, "/api/importers", "", false); res.Code != http.StatusUnauthorized {
		t.Errorf("no token = %d", res.Code)
	}
	bare := New(&fakeRepository{}, testAssets(), testToken, false)
	if res := request(bare, http.MethodGet, "/api/importers", "", true); res.Code != http.StatusServiceUnavailable {
		t.Errorf("no store = %d", res.Code)
	}
}
