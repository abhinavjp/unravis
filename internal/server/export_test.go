package server

import (
	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/ledger"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

const fakeExportKey = "KEYBYTES-must-never-leave-the-server"

type fakeBuild struct {
	inputs []evidence.Input
	err    error
}

// installExportFakes swaps the evidence seams for fakes and restores them.
func installExportFakes(t *testing.T) *fakeBuild {
	t.Helper()
	fb := &fakeBuild{}
	ob, ok, oc := buildExport, newExportKey, canonicalExport
	t.Cleanup(func() { buildExport, newExportKey, canonicalExport = ob, ok, oc })
	newExportKey = func() ([]byte, error) { return []byte(fakeExportKey), nil }
	buildExport = func(in evidence.Input) (evidence.Export, error) {
		in.Key = append([]byte(nil), in.Key...)
		fb.inputs = append(fb.inputs, in)
		if fb.err != nil {
			return evidence.Export{}, fb.err
		}
		return evidence.Export{Manifest: evidence.Manifest{SchemaVersion: 1, ToolVersion: in.ToolVersion, CreatedAt: in.Now, BodySHA256: "fakehash123"}, Body: evidence.Body{Records: []evidence.Record{}}}, nil
	}
	canonicalExport = func(e evidence.Export) ([]byte, error) {
		return []byte("{\n  \"canonical\": \"" + e.Manifest.BodySHA256 + "\"\n}\n"), nil
	}
	return fb
}

func exportBody(ids ...string) string {
	now := time.Now().UTC().Truncate(time.Second)
	raw, _ := json.Marshal(map[string]any{"sourceIds": ids, "start": now.Add(-time.Hour), "end": now, "includePseudonyms": false, "includeLedger": true})
	return string(raw)
}

type previewReply struct {
	PreviewID string          `json:"previewId"`
	Export    evidence.Export `json:"export"`
}

func demoExportHandler(opts ...Option) http.Handler {
	return New(audit.NewDemoWithPatterns(time.Now()), nil, testToken, true, opts...)
}

func doPreview(t *testing.T, h http.Handler) previewReply {
	t.Helper()
	r := request(h, http.MethodPost, "/api/export/preview", exportBody("demo-west"), true)
	if r.Code != http.StatusOK {
		t.Fatalf("preview = %d %s", r.Code, r.Body.String())
	}
	var out previewReply
	if err := json.Unmarshal(r.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestExportPreviewThenConfirmOnce(t *testing.T) {
	fb := installExportFakes(t)
	store := &fakeLedger{items: []ledger.Hypothesis{{ID: "h1", Text: "slow reads", State: ledger.StateOpen}}}
	h := demoExportHandler(WithLedger(store))
	p := doPreview(t, h)
	if raw, err := hex.DecodeString(p.PreviewID); err != nil || len(raw) != 16 {
		t.Fatalf("previewId = %q", p.PreviewID)
	}
	if p.Export.Manifest.BodySHA256 != "fakehash123" {
		t.Fatalf("export = %+v", p.Export)
	}
	in := fb.inputs[0]
	// h1 references no finding of this report, so it is not passed on.
	if len(in.Ledger) != 0 || !in.Options.IncludeLedger || in.Options.IncludePseudonyms || in.ToolVersion == "" || in.Now.IsZero() {
		t.Fatalf("build input = %+v", in)
	}
	if in.SourceLabels["demo-west"] != "Demo West" {
		t.Fatalf("labels = %v", in.SourceLabels)
	}

	body := `{"previewId":"` + p.PreviewID + `"}`
	r := request(h, http.MethodPost, "/api/export/confirm", body, true)
	if r.Code != http.StatusOK {
		t.Fatalf("confirm = %d %s", r.Code, r.Body.String())
	}
	var c struct{ Filename, Content, SHA256 string }
	if err := json.Unmarshal(r.Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	want, _ := canonicalExport(p.Export)
	sum := sha256.Sum256([]byte(c.Content))
	if c.Content != string(want) || c.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("confirm = %+v; sha256 must be over the content string", c)
	}
	if !strings.HasPrefix(c.Filename, "unravis-evidence-") || !strings.HasSuffix(c.Filename, ".json") || len(c.Filename) != len("unravis-evidence-20060102-150405.json") {
		t.Fatalf("filename = %q", c.Filename)
	}
	if r := request(h, http.MethodPost, "/api/export/confirm", body, true); r.Code != http.StatusNotFound {
		t.Fatalf("second confirm = %d", r.Code)
	}
}

func TestExportLedgerOnlyWhenRequested(t *testing.T) {
	fb := installExportFakes(t)
	h := demoExportHandler(WithLedger(&fakeLedger{items: []ledger.Hypothesis{{ID: "h1", Text: "x", State: ledger.StateOpen}}}))
	now := time.Now().UTC()
	raw, _ := json.Marshal(map[string]any{"sourceIds": []string{"demo-west"}, "start": now.Add(-time.Hour), "end": now})
	if r := request(h, http.MethodPost, "/api/export/preview", string(raw), true); r.Code != http.StatusOK {
		t.Fatalf("preview = %d %s", r.Code, r.Body.String())
	}
	if len(fb.inputs[0].Ledger) != 0 || fb.inputs[0].Options.IncludeLedger {
		t.Fatalf("ledger passed without opt-in: %+v", fb.inputs[0])
	}
}

// "Include my ledger" passes on only hypotheses whose evidence ids intersect
// this report's findings.
func TestExportLedgerOnlyRelatedHypotheses(t *testing.T) {
	fb := installExportFakes(t)
	store := &fakeLedger{}
	h := demoExportHandler(WithLedger(store))
	body := exportBody("demo-west") // one fixed window for both previews
	preview := func() evidence.Input {
		t.Helper()
		if r := request(h, http.MethodPost, "/api/export/preview", body, true); r.Code != http.StatusOK {
			t.Fatalf("preview = %d %s", r.Code, r.Body.String())
		}
		return fb.inputs[len(fb.inputs)-1]
	}
	probe := preview()
	if len(probe.Report.Findings) == 0 {
		t.Fatal("demo report has no findings")
	}
	related := probe.Report.Findings[0].EvidenceID
	store.items = []ledger.Hypothesis{
		{ID: "related", Text: "this case", State: ledger.StateOpen, EvidenceIDs: []string{"p2:b1:ffffffffffffffff", related}},
		{ID: "other", Text: "an earlier case", State: ledger.StateOpen, EvidenceIDs: []string{"p2:b1:ffffffffffffffff"}},
		{ID: "none", Text: "no references", State: ledger.StateOpen},
	}
	in := preview()
	if len(in.Ledger) != 1 || in.Ledger[0].ID != "related" {
		t.Fatalf("ledger passed to build = %+v", in.Ledger)
	}
}

func TestExportConfirmUnknownPreview(t *testing.T) {
	installExportFakes(t)
	h := demoExportHandler()
	r := request(h, http.MethodPost, "/api/export/confirm", `{"previewId":"nope"}`, true)
	if r.Code != http.StatusNotFound || !strings.Contains(r.Body.String(), "That preview is no longer available. Run the preview again.") {
		t.Fatalf("unknown = %d %s", r.Code, r.Body.String())
	}
}

func TestExportPreviewBadWindowIs400(t *testing.T) {
	installExportFakes(t)
	h := demoExportHandler()
	now := time.Now().UTC()
	raw, _ := json.Marshal(map[string]any{"sourceIds": []string{"demo-west"}, "start": now, "end": now.Add(-time.Hour)})
	if r := request(h, http.MethodPost, "/api/export/preview", string(raw), true); r.Code != http.StatusBadRequest {
		t.Fatalf("bad window = %d %s", r.Code, r.Body.String())
	}
	if r := request(h, http.MethodPost, "/api/export/preview", exportBody(), true); r.Code != http.StatusBadRequest {
		t.Fatalf("no sources = %d", r.Code)
	}
	if r := request(h, http.MethodPost, "/api/export/preview", `{"sourceIds":["a"],"bogus":1}`, true); r.Code != http.StatusBadRequest {
		t.Fatalf("unknown field = %d", r.Code)
	}
}

func TestExportPreviewWithoutSlowestReaderIs503(t *testing.T) {
	installExportFakes(t)
	h := New(&fakeRepository{}, nil, testToken, false)
	r := request(h, http.MethodPost, "/api/export/preview", exportBody("demo-west"), true)
	if r.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d %s", r.Code, r.Body.String())
	}
}

func TestExportRequiresToken(t *testing.T) {
	installExportFakes(t)
	h := demoExportHandler()
	if r := request(h, http.MethodPost, "/api/export/preview", exportBody("demo-west"), false); r.Code != http.StatusUnauthorized {
		t.Fatalf("preview no token = %d", r.Code)
	}
	if r := request(h, http.MethodPost, "/api/export/confirm", `{"previewId":"x"}`, false); r.Code != http.StatusUnauthorized {
		t.Fatalf("confirm no token = %d", r.Code)
	}
}

func TestExportResponsesNeverContainKey(t *testing.T) {
	fb := installExportFakes(t)
	h := demoExportHandler()
	r := request(h, http.MethodPost, "/api/export/preview", exportBody("demo-west"), true)
	var p previewReply
	_ = json.Unmarshal(r.Body.Bytes(), &p)
	c := request(h, http.MethodPost, "/api/export/confirm", `{"previewId":"`+p.PreviewID+`"}`, true)
	if string(fb.inputs[0].Key) != fakeExportKey {
		t.Fatalf("fake key was not passed to build: %q", fb.inputs[0].Key)
	}
	for _, body := range []string{r.Body.String(), c.Body.String()} {
		for _, form := range []string{fakeExportKey, hex.EncodeToString([]byte(fakeExportKey)), base64.StdEncoding.EncodeToString([]byte(fakeExportKey)), "KEYBYTES"} {
			if strings.Contains(body, form) {
				t.Fatalf("response contains key form %q: %s", form, body)
			}
		}
	}
}

func TestExportBuildFailureMapping(t *testing.T) {
	fb := installExportFakes(t)
	fb.err = evidence.ErrInvalid
	h := demoExportHandler()
	if r := request(h, http.MethodPost, "/api/export/preview", exportBody("demo-west"), true); r.Code != http.StatusBadRequest {
		t.Fatalf("invalid build = %d", r.Code)
	}
	fb.err = http.ErrAbortHandler
	r := request(h, http.MethodPost, "/api/export/preview", exportBody("demo-west"), true)
	if r.Code != http.StatusInternalServerError || strings.Contains(r.Body.String(), "abort") {
		t.Fatalf("build failure = %d %s", r.Code, r.Body.String())
	}
}

func TestExportPreviewCapEvictsOldest(t *testing.T) {
	installExportFakes(t)
	hh := demoExportHandler()
	hd := hh.(*handler)
	clock := time.Now()
	hd.exports().now = func() time.Time { clock = clock.Add(time.Second); return clock }
	var ids []string
	for i := 0; i < exportMaxPreviews+1; i++ {
		ids = append(ids, doPreview(t, hh).PreviewID)
	}
	if n := len(hd.exports().items); n != exportMaxPreviews {
		t.Fatalf("store size = %d", n)
	}
	if r := request(hh, http.MethodPost, "/api/export/confirm", `{"previewId":"`+ids[0]+`"}`, true); r.Code != http.StatusNotFound {
		t.Fatalf("oldest preview survived the cap: %d", r.Code)
	}
	if r := request(hh, http.MethodPost, "/api/export/confirm", `{"previewId":"`+ids[1]+`"}`, true); r.Code != http.StatusOK {
		t.Fatalf("second-oldest preview evicted: %d", r.Code)
	}
}

func TestExportPreviewExpiresAfterTTL(t *testing.T) {
	installExportFakes(t)
	hh := demoExportHandler()
	clock := time.Now()
	hh.(*handler).exports().now = func() time.Time { return clock }
	p := doPreview(t, hh)
	clock = clock.Add(exportPreviewTTL + time.Second)
	if r := request(hh, http.MethodPost, "/api/export/confirm", `{"previewId":"`+p.PreviewID+`"}`, true); r.Code != http.StatusNotFound {
		t.Fatalf("expired preview confirmed: %d", r.Code)
	}
}
