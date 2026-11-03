package server

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

// policyProbe wraps toyImporter and records the CookiePolicy of each Parse.
type policyProbe struct {
	toyImporter
	mu   *sync.Mutex
	seen *[]*importer.CookiePolicy
}

func (p policyProbe) Parse(ctx context.Context, r io.Reader, opts importer.Options, emit func(evidence.Event) error) (importer.Report, error) {
	var cp *importer.CookiePolicy
	if opts.CookiePolicy != nil {
		c := *opts.CookiePolicy
		c.Allowed = append([]string(nil), c.Allowed...)
		c.HMACKey = append([]byte(nil), c.HMACKey...)
		cp = &c
	}
	p.mu.Lock()
	*p.seen = append(*p.seen, cp)
	p.mu.Unlock()
	return p.toyImporter.Parse(ctx, r, opts, emit)
}

func newProbeEnv(t *testing.T) (*caseEnv, func() []*importer.CookiePolicy) {
	t.Helper()
	var mu sync.Mutex
	var seen []*importer.CookiePolicy
	probe := policyProbe{toyImporter: toyImporter{name: "toy", score: 90}, mu: &mu, seen: &seen}
	e := newCaseEnv(t, envConfig{importers: []importer.Importer{probe}})
	return e, func() []*importer.CookiePolicy {
		mu.Lock()
		defer mu.Unlock()
		return append([]*importer.CookiePolicy(nil), seen...)
	}
}

func setAllowlist(t *testing.T, e *caseEnv, id string, names ...string) {
	t.Helper()
	c, err := e.store.Open(context.Background(), casestore.CaseID(id))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	cp, ok := c.(casestore.CookiePolicyStore)
	if !ok {
		t.Fatal("the case store has no cookie policy")
	}
	if _, err := cp.SetCookieAllowlist(context.Background(), names); err != nil {
		t.Fatal(err)
	}
}

func policyOf(t *testing.T, e *caseEnv, id string) importer.CookiePolicy {
	t.Helper()
	c, err := e.store.Open(context.Background(), casestore.CaseID(id))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	pol, err := c.(casestore.CookiePolicyStore).CookiePolicy(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return pol
}

func TestImportGetsNoPolicyByDefault(t *testing.T) {
	e, seen := newProbeEnv(t)
	id := e.newCase("no policy")
	res := e.upload(commitPath(id), ackFields, &upload{"a.csv", toyCSV(toyRow(1, "web-a", "alpha", 5))})
	if res.Code != http.StatusCreated && res.Code != http.StatusOK {
		t.Fatalf("import = %d %s", res.Code, res.Body)
	}
	got := seen()
	if len(got) != 1 || got[0] != nil {
		t.Fatalf("probe saw %v, want one nil policy", got)
	}
}

func TestImportGetsPolicyWhenAllowlistSet(t *testing.T) {
	e, seen := newProbeEnv(t)
	id := e.newCase("with policy")
	setAllowlist(t, e, id, "sid")
	res := e.upload(commitPath(id), ackFields, &upload{"a.csv", toyCSV(toyRow(1, "web-a", "alpha", 5))})
	if res.Code != http.StatusCreated && res.Code != http.StatusOK {
		t.Fatalf("import = %d %s", res.Code, res.Body)
	}
	got := seen()
	if len(got) != 1 || got[0] == nil {
		t.Fatalf("probe saw %v, want one policy", got)
	}
	if !reflect.DeepEqual(got[0].Allowed, []string{"sid"}) || len(got[0].HMACKey) != casestore.CookieKeyBytes {
		t.Fatalf("policy = %v with a %d-byte key", got[0].Allowed, len(got[0].HMACKey))
	}
}

func TestPreviewImportGetsPolicyButWritesNoKey(t *testing.T) {
	e, seen := newProbeEnv(t)
	id := e.newCase("preview")
	res := e.upload(commitPath(id)+"&preview=1", nil, &upload{"a.csv", toyCSV(toyRow(1, "web-a", "alpha", 5))})
	if res.Code != http.StatusOK {
		t.Fatalf("preview = %d %s", res.Code, res.Body)
	}
	got := seen()
	if len(got) != 1 || got[0] != nil {
		t.Fatalf("probe saw %v, want one nil policy", got)
	}
	if pol := policyOf(t, e, id); pol.Allowed != nil || pol.HMACKey != nil {
		t.Fatalf("policy after a preview = %+v, want the zero policy", pol)
	}
}

func TestImportResponseNeverContainsKey(t *testing.T) {
	e, _ := newProbeEnv(t)
	id := e.newCase("no key leak")
	setAllowlist(t, e, id, "sid")
	key := policyOf(t, e, id).HMACKey
	if len(key) != casestore.CookieKeyBytes {
		t.Fatalf("key length = %d", len(key))
	}
	needles := []string{
		hex.EncodeToString(key), strings.ToUpper(hex.EncodeToString(key)),
		base64.StdEncoding.EncodeToString(key), base64.URLEncoding.EncodeToString(key),
		base64.RawStdEncoding.EncodeToString(key), base64.RawURLEncoding.EncodeToString(key),
	}
	check := func(what string, res *httptest.ResponseRecorder) {
		t.Helper()
		body := res.Body.String()
		for _, n := range needles {
			if strings.Contains(body, n) {
				t.Fatalf("%s response contains the cookie key", what)
			}
		}
	}
	res := e.upload(commitPath(id), ackFields, &upload{"a.csv", toyCSV(toyRow(1, "web-a", "alpha", 5))})
	if res.Code != http.StatusCreated && res.Code != http.StatusOK {
		t.Fatalf("import = %d %s", res.Code, res.Body)
	}
	check("commit import", res)
	info := e.get("/api/cases/" + id)
	if info.Code != http.StatusOK {
		t.Fatalf("case info = %d %s", info.Code, info.Body)
	}
	check("case info", info)
}
