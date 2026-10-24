package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/evidence"
)

// L1: the acknowledgement text is bound to the banner the page shows.

func storedAcks(t *testing.T, e *caseEnv, id string) []string {
	t.Helper()
	c, err := e.store.Open(context.Background(), casestore.CaseID(id))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	recs, err := c.Imports(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, r := range recs {
		out = append(out, r.AckText)
	}
	return out
}

func ackFile(sec int) *upload {
	return &upload{"a.csv", toyCSV(toyRow(sec, "web-a", "alpha", 5))}
}

func TestAckTextThatIsNotTheBannerIsRefused(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := e.newCase("Ack bind")
	const forged = "zz-forged-ack-sentinel"
	res := e.upload(commitPath(id), []formField{{"ackGating", "true"}, {"ackText", forged}}, ackFile(1))
	if res.Code != http.StatusBadRequest || failureOf(t, res).Reason != "ack-mismatch" {
		t.Fatalf("forged ack = %d %s", res.Code, res.Body)
	}
	if strings.Contains(res.Body.String(), forged) {
		t.Fatal("the refusal repeats the text sent")
	}
	if n := len(e.importsOf(id)); n != 0 {
		t.Fatalf("imports = %d, want 0", n)
	}
}

func TestBannerTextIsStoredInItsCanonicalForm(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := e.newCase("Ack canonical")
	spaced := "  " + strings.ReplaceAll(evidence.GatingBanner(), " ", "  ") + "\n"
	res := e.upload(commitPath(id), []formField{{"ackGating", "true"}, {"ackText", spaced}}, ackFile(1))
	if res.Code != http.StatusCreated {
		t.Fatalf("banner with other spacing = %d %s", res.Code, res.Body)
	}
	got := storedAcks(t, e, id)
	if len(got) != 1 || got[0] != evidence.GatingBanner() {
		t.Fatalf("stored ack = %q, want the canonical banner", got)
	}
}

func TestCustomAckMustBeMarkedAndPlain(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := e.newCase("Ack custom")
	cases := []struct {
		name   string
		fields []formField
		want   int
	}{
		{"custom marked", []formField{{"ackGating", "true"}, {"ackCustom", "true"}, {"ackText", "Read the note for web-a."}}, http.StatusCreated},
		{"custom not marked", []formField{{"ackGating", "true"}, {"ackText", "Read the note for web-b."}}, http.StatusBadRequest},
		{"custom marked false", []formField{{"ackGating", "true"}, {"ackCustom", "false"}, {"ackText", "Read the note."}}, http.StatusBadRequest},
		{"custom blank", []formField{{"ackGating", "true"}, {"ackCustom", "true"}, {"ackText", "   "}}, http.StatusBadRequest},
		{"custom control char", []formField{{"ackGating", "true"}, {"ackCustom", "true"}, {"ackText", "ok\x00ok"}}, http.StatusBadRequest},
		{"custom bad utf8", []formField{{"ackGating", "true"}, {"ackCustom", "true"}, {"ackText", "ok\xff\xfeok"}}, http.StatusBadRequest},
	}
	for i, c := range cases {
		res := e.upload(commitPath(id), c.fields, ackFile(i+1))
		if res.Code != c.want {
			t.Errorf("%s = %d %s, want %d", c.name, res.Code, res.Body, c.want)
		}
		if c.want == http.StatusBadRequest && failureOf(t, res).Reason != "ack-mismatch" {
			t.Errorf("%s reason = %q", c.name, failureOf(t, res).Reason)
		}
	}
	if got := storedAcks(t, e, id); len(got) != 1 || got[0] != "Read the note for web-a." {
		t.Fatalf("stored acks = %q", got)
	}
}

// The page sends GATING_BANNER as ackText. If the two texts drift apart every
// import from the page would be refused, so the copy file is checked here.
func TestPageBannerMatchesServerBanner(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "web", "src", "lib", "copy", "importCopy.ts"))
	if err != nil {
		t.Skipf("web copy not readable: %v", err)
	}
	if !strings.Contains(string(data), "'"+evidence.GatingBanner()+"'") {
		t.Fatal("web GATING_BANNER differs from evidence.GatingBanner()")
	}
}

func TestPreviewNeedsNoAckBinding(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := e.newCase("Ack preview")
	res := e.upload("/api/cases/"+id+"/imports?importer=toy&host=web-a&preview=1", nil, ackFile(1))
	if res.Code != http.StatusOK {
		t.Fatalf("preview = %d %s", res.Code, res.Body)
	}
}
