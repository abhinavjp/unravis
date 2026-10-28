package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

const (
	resourceFrom = "2001-01-01T00:00:00Z"
	resourceTo   = "2001-01-01T00:01:00Z"
)

type resourceAnswerBody struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Partial bool   `json:"partial"`
	Hosts   []struct {
		Host     string   `json:"host"`
		Summary  []string `json:"summary"`
		Coverage []struct {
			Metric   string  `json:"metric"`
			Fraction float64 `json:"fraction"`
		} `json:"coverage"`
		Processes []struct {
			Name string            `json:"name"`
			Sel  map[string]string `json:"sel"`
		} `json:"processes"`
	} `json:"hosts"`
	Rules []struct {
		ID         string   `json:"id"`
		Label      string   `json:"label"`
		Wording    string   `json:"wording"`
		Thresholds string   `json:"thresholds"`
		Evidence   []string `json:"evidence"`
		Episode    *struct {
			Start string `json:"start"`
			End   string `json:"end"`
		} `json:"episode"`
	} `json:"rules"`
	Gaps    []string `json:"gaps"`
	Markers []struct {
		Kind string `json:"kind"`
		T    string `json:"t"`
	} `json:"markers"`
	Episodes []struct {
		Start string `json:"start"`
		End   string `json:"end"`
	} `json:"episodes"`
}

func resourceURL(id, from, to string, eps ...string) string {
	q := url.Values{}
	if from != "" {
		q.Set("from", from)
	}
	if to != "" {
		q.Set("to", to)
	}
	for _, ep := range eps {
		q.Add("ep", ep)
	}
	return "/api/cases/" + id + "/resource?" + q.Encode()
}

// assertNoNullArrays checks the body keeps every top-level array a list.
func assertNoNullArrays(t *testing.T, body string) {
	t.Helper()
	for _, k := range []string{"hosts", "rules", "gaps", "markers", "episodes"} {
		if strings.Contains(body, `"`+k+`":null`) || !strings.Contains(body, `"`+k+`":[`) {
			t.Errorf("%s is not a list: %s", k, body)
		}
	}
	for _, k := range []string{"summary", "coverage", "processes", "evidence"} {
		if strings.Contains(body, `"`+k+`":null`) {
			t.Errorf("%s is null: %s", k, body)
		}
	}
}

func TestResourceAnswersWithEpisodes(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := seriesCase(t, e)
	res := e.get(resourceURL(id, resourceFrom, resourceTo, "2001-01-01T00:00:05Z/2001-01-01T00:00:10Z"))
	if res.Code != http.StatusOK {
		t.Fatalf("resource = %d %s", res.Code, res.Body)
	}
	if ct := res.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content type %q", ct)
	}
	assertNoNullArrays(t, res.Body.String())
	var out resourceAnswerBody
	decodeBody(t, res, &out)
	if out.From != resourceFrom || out.To != resourceTo {
		t.Errorf("header: %+v", out)
	}
	if len(out.Episodes) != 1 || out.Episodes[0].Start != "2001-01-01T00:00:05Z" || out.Episodes[0].End != "2001-01-01T00:00:10Z" {
		t.Errorf("episodes: %+v", out.Episodes)
	}
	for _, r := range out.Rules {
		if r.Label != "Symptom" && r.Label != "Correlation" {
			t.Errorf("label %q on %s", r.Label, r.ID)
		}
		if err := evidence.LintCausal(r.Wording); err != nil {
			t.Errorf("wording %q: %v", r.Wording, err)
		}
	}
	for _, g := range out.Gaps {
		if err := evidence.LintCausal(g); err != nil {
			t.Errorf("gap %q: %v", g, err)
		}
	}
}

func TestResourceWithoutEpisodesAnswersAnEmptyEpisodeList(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := seriesCase(t, e)
	res := e.get(resourceURL(id, resourceFrom, resourceTo))
	if res.Code != http.StatusOK {
		t.Fatalf("resource = %d %s", res.Code, res.Body)
	}
	if !strings.Contains(res.Body.String(), `"episodes":[]`) {
		t.Errorf("episodes not an empty list: %s", res.Body)
	}
	assertNoNullArrays(t, res.Body.String())
}

func TestResourceEmptyCaseIsEmptyListsNotNull(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := e.newCase("Empty resource case")
	res := e.get(resourceURL(id, resourceFrom, resourceTo, "2001-01-01T00:00:05Z/2001-01-01T00:00:10Z"))
	if res.Code != http.StatusOK {
		t.Fatalf("resource = %d %s", res.Code, res.Body)
	}
	assertNoNullArrays(t, res.Body.String())
	var out resourceAnswerBody
	decodeBody(t, res, &out)
	if len(out.Hosts) != 0 || len(out.Rules) != 0 || len(out.Markers) != 0 || out.Partial {
		t.Errorf("empty case answer: %+v", out)
	}
}

func TestResourceRefusals(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := seriesCase(t, e)
	base := "/api/cases/" + id + "/resource?"
	ep := "2001-01-01T00:00:05Z/2001-01-01T00:00:10Z"
	many := make([]string, maxResourceEpisodes+1)
	for i := range many {
		many[i] = ep
	}
	for _, c := range []struct {
		name   string
		path   string
		reason string
	}{
		{"no time", base, "bad-time"},
		{"no from", resourceURL(id, "", resourceTo), "bad-time"},
		{"no to", resourceURL(id, resourceFrom, ""), "bad-time"},
		{"words for from", resourceURL(id, "yesterday", resourceTo), "bad-time"},
		{"no zone", resourceURL(id, "2001-01-01T00:00:00", resourceTo), "bad-time"},
		{"to before from", resourceURL(id, resourceTo, resourceFrom), "bad-time"},
		{"year 0001 to year 9999", resourceURL(id, "0001-01-01T00:00:00Z", "9999-01-01T00:00:00Z"), "bad-time"},
		{"ep without slash", resourceURL(id, resourceFrom, resourceTo, "2001-01-01T00:00:05Z"), "bad-episode"},
		{"ep empty", resourceURL(id, resourceFrom, resourceTo, ""), "bad-episode"},
		{"ep words", resourceURL(id, resourceFrom, resourceTo, "start/end"), "bad-episode"},
		{"ep without zone", resourceURL(id, resourceFrom, resourceTo, "2001-01-01T00:00:05/2001-01-01T00:00:10Z"), "bad-episode"},
		{"ep end before start", resourceURL(id, resourceFrom, resourceTo, "2001-01-01T00:00:10Z/2001-01-01T00:00:05Z"), "bad-episode"},
		{"ep three parts", resourceURL(id, resourceFrom, resourceTo, ep+"/2001-01-01T00:00:11Z"), "bad-episode"},
		{"201 episodes", resourceURL(id, resourceFrom, resourceTo, many...), "bad-episode"},
	} {
		res := e.get(c.path)
		if res.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s, want 400", c.name, res.Code, res.Body)
			continue
		}
		if f := failureOf(t, res); f.Reason != c.reason || f.Error == "" {
			t.Errorf("%s: reason %q (%s), want %q", c.name, f.Reason, f.Error, c.reason)
		}
	}
	// The limit itself is accepted.
	if res := e.get(resourceURL(id, resourceFrom, resourceTo, many[:maxResourceEpisodes]...)); res.Code != http.StatusOK {
		t.Errorf("200 episodes = %d %s", res.Code, res.Body)
	}
}

func TestResourceNeverEchoesFileContents(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := e.newCase("Resource secret")
	file := &upload{"a.csv", toyCSV(toyRow(1, "web-a", secretCell, 10))}
	if res := e.upload(commitPath(id), ackFields, file); res.Code != http.StatusCreated {
		t.Fatalf("import = %d %s", res.Code, res.Body)
	}
	for _, path := range []string{
		resourceURL(id, resourceFrom, resourceTo),
		resourceURL(id, resourceFrom, resourceTo, secretCell),
	} {
		if body := e.get(path).Body.String(); strings.Contains(body, secretCell) {
			t.Errorf("response carries a cell: %s", body)
		}
	}
}

func TestResourceNeedsTheTokenAndTheRightMethod(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := seriesCase(t, e)
	path := resourceURL(id, resourceFrom, resourceTo)
	for name, header := range map[string]string{"none": "", "wrong": "not-the-token"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if header != "" {
			req.Header.Set("X-Audit-Token", header)
		}
		if res := e.serve(req); res.Code != http.StatusUnauthorized {
			t.Errorf("%s token = %d %s, want 401", name, res.Code, res.Body)
		}
	}
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		req := httptest.NewRequest(method, path, nil)
		req.Header.Set("X-Audit-Token", testToken)
		if res := e.serve(req); res.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s = %d %s, want 405", method, res.Code, res.Body)
		}
	}
}

func TestResourceUnknownAndBadCaseIDs(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	unknown := strings.Repeat("a", 26)
	res := e.get(resourceURL(unknown, resourceFrom, resourceTo))
	if res.Code != http.StatusNotFound || failureOf(t, res).Reason != "case-not-found" {
		t.Errorf("unknown case = %d %s", res.Code, res.Body)
	}
	for _, id := range []string{"BAD", "short", strings.Repeat("a", 27), strings.Repeat("A", 26), strings.Repeat("a", 25) + "-"} {
		res := e.get(resourceURL(id, resourceFrom, resourceTo))
		if res.Code != http.StatusBadRequest || failureOf(t, res).Reason != "bad-case-id" {
			t.Errorf("id %q = %d %s", id, res.Code, res.Body)
		}
	}
	// A bad case id is refused before the query is looked at.
	res = e.get("/api/cases/BAD/resource")
	if res.Code != http.StatusBadRequest || failureOf(t, res).Reason != "bad-case-id" {
		t.Errorf("bad id, no query = %d %s", res.Code, res.Body)
	}
}

func TestResourceWithoutAStoreIs503(t *testing.T) {
	h := New(&fakeRepository{}, testAssets(), testToken, false)
	res := request(h, http.MethodGet, resourceURL(strings.Repeat("a", 26), resourceFrom, resourceTo), "", true)
	if res.Code != http.StatusServiceUnavailable {
		t.Errorf("resource without a store = %d", res.Code)
	}
}

func TestResourceAfterEraseIs404(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := seriesCase(t, e)
	path := resourceURL(id, resourceFrom, resourceTo)
	if res := e.get(path); res.Code != http.StatusOK {
		t.Fatalf("before erase = %d %s", res.Code, res.Body)
	}
	if res := e.json(http.MethodDelete, "/api/cases/"+id, `{"confirm":"`+id+`"}`); res.Code != http.StatusNoContent {
		t.Fatalf("erase = %d %s", res.Code, res.Body)
	}
	res := e.get(path)
	if res.Code != http.StatusNotFound || failureOf(t, res).Reason != "case-not-found" {
		t.Errorf("after erase = %d %s", res.Code, res.Body)
	}
}

// Resource reads that race an erase end in 200, 404, or 409 "case-erasing"
// (M5: the erase is draining the readers in flight), never in a server error
// or any other answer, and every read after the erase is 404.
func TestResourceRacingAnEraseEndsCleanly(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := seriesCase(t, e)
	path := resourceURL(id, resourceFrom, resourceTo, "2001-01-01T00:00:05Z/2001-01-01T00:00:10Z")
	const readers = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	type answer struct {
		code int
		body string
	}
	codes := make(chan answer, readers*10)
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 10; j++ {
				res := e.get(path)
				codes <- answer{res.Code, res.Body.String()}
			}
		}()
	}
	eraseDone := make(chan int, 1)
	go func() {
		<-start
		eraseDone <- e.json(http.MethodDelete, "/api/cases/"+id, `{"confirm":"`+id+`"}`).Code
	}()
	close(start)
	wg.Wait()
	close(codes)
	if code := <-eraseDone; code != http.StatusNoContent {
		t.Fatalf("erase = %d", code)
	}
	for a := range codes {
		switch {
		case a.code == http.StatusOK || a.code == http.StatusNotFound:
		case a.code == http.StatusConflict && strings.Contains(a.body, `"case-erasing"`):
		default:
			t.Errorf("a resource read racing an erase answered %d %s", a.code, a.body)
		}
	}
	if res := e.get(path); res.Code != http.StatusNotFound {
		t.Errorf("after the race = %d", res.Code)
	}
}

/* ----------------------------------------------------------- processes */

// procsnapImporter reads the toy rows and emits them as win-procsnap private
// bytes samples of w3wp pid 1240: the ms column is the value in bytes.
type procsnapImporter struct{ toyImporter }

func (procsnapImporter) AllowedAttrs() map[string]bool {
	return map[string]bool{"metric": true, "process": true, "pid": true}
}

func (p procsnapImporter) Parse(ctx context.Context, r io.Reader, opts importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
	return p.toyImporter.Parse(ctx, r, opts, func(ev evidence.Event) error {
		v := *ev.DurationMs
		ev.Kind, ev.Value, ev.DurationMs, ev.Unit = "metric", &v, nil, "bytes"
		ev.Attrs = map[string]string{"metric": "private-bytes", "process": "w3wp", "pid": "1240"}
		return emit(ev)
	})
}

func TestResourceProcessSelectorsReadThroughSeries(t *testing.T) {
	e := newCaseEnv(t, envConfig{importers: []importer.Importer{procsnapImporter{toyImporter{name: "win-procsnap", score: 90}}}})
	id := e.newCase("Process case")
	var rows []string
	for s := 0; s < 60; s += 5 {
		rows = append(rows, toyRow(s, "web-a", "p", 1000+s))
	}
	path := "/api/cases/" + id + "/imports?importer=win-procsnap&host=web-a"
	if res := e.upload(path, ackFields, &upload{"a.csv", toyCSV(rows...)}); res.Code != http.StatusCreated {
		t.Fatalf("import = %d %s", res.Code, res.Body)
	}
	res := e.get(resourceURL(id, resourceFrom, resourceTo))
	if res.Code != http.StatusOK {
		t.Fatalf("resource = %d %s", res.Code, res.Body)
	}
	var out resourceAnswerBody
	decodeBody(t, res, &out)
	if len(out.Hosts) != 1 || len(out.Hosts[0].Processes) != 1 {
		t.Fatalf("hosts: %+v", out.Hosts)
	}
	p := out.Hosts[0].Processes[0]
	want := "kind=metric;source=win-procsnap;host=web-a;attr.metric=private-bytes;attr.pid=1240;field=value"
	if p.Name != "w3wp:1240" || len(p.Sel) != 1 || p.Sel["proc.private"] != want {
		t.Fatalf("process: %+v", p)
	}
	sres := e.get(seriesURL(id, resourceFrom, resourceTo, "10000", p.Sel["proc.private"]))
	if sres.Code != http.StatusOK {
		t.Fatalf("series = %d %s", sres.Code, sres.Body)
	}
	var series seriesAnswer
	decodeBody(t, sres, &series)
	if len(series.Series) != 1 || series.Series[0].Count != int64(len(rows)) || series.Series[0].Unit != "bytes" {
		t.Errorf("series through the selector: %+v", series.Series)
	}
}
