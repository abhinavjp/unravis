package lite

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"api-audit-explorer/internal/bbsaved"
	"api-audit-explorer/internal/blackbox"
	"api-audit-explorer/internal/server"
)

func TestAllowedIsAnAllowlist(t *testing.T) {
	yes := [][2]string{
		{"GET", "/"}, {"GET", "/assets/lite.js"}, {"HEAD", "/index.html"}, {"GET", "/launch"},
		{"GET", "/api/blackbox"}, {"POST", "/api/blackbox/" + ID + "/series"},
	}
	no := [][2]string{
		{"POST", "/"}, {"PUT", "/assets/x.js"}, {"POST", "/launch"},
		{"POST", "/api/blackbox"}, {"DELETE", "/api/blackbox/" + ID}, {"GET", "/api/blackbox/" + ID + "/series"},
		{"POST", "/api/blackbox/bb-other/series"}, {"POST", "/api/blackbox/" + ID + "/key"},
		{"POST", "/api/blackbox/" + ID + "/reveal-key"}, {"POST", "/api/blackbox/" + ID + "/check"},
		{"POST", "/api/blackbox/" + ID + "/host-reading"}, {"GET", "/api/blackbox/this-pc"},
		{"POST", "/api/blackbox/this-pc/install"}, {"POST", "/api/blackbox/this-pc/remove"},
		{"POST", "/api/blackbox/this-pc/update"}, {"POST", "/api/blackbox/test"},
		{"GET", "/api/sources"}, {"POST", "/api/cases"}, {"GET", "/api/session-proof"},
		{"POST", "/api/update/install"}, {"GET", "/api/"}, {"GET", "/api"},
		{"GET", "/api/blackbox/"}, {"GET", "/api/blackbox/../sources"},
	}
	for _, c := range yes {
		if !Allowed(c[0], c[1]) {
			t.Errorf("%s %s should be allowed", c[0], c[1])
		}
	}
	for _, c := range no {
		if Allowed(c[0], c[1]) {
			t.Errorf("%s %s should be refused", c[0], c[1])
		}
	}
}

// fakeCollector gives the real recorder one reading per tick.
type fakeCollector struct{}

func (fakeCollector) Collect(time.Time) (blackbox.Sample, error) {
	v := 42.0
	return blackbox.Sample{CPU: &v}, nil
}
func (fakeCollector) Close() error { return nil }

type okDisk struct{}

func (okDisk) Free(string) (blackbox.DiskInfo, error) {
	return blackbox.DiskInfo{Free: 80, Total: 100}, nil
}

// record writes a real recordings folder with the real recorder and returns it.
func record(t *testing.T, start time.Time, ticks int) string {
	t.Helper()
	cfg := blackbox.Default()
	cfg.Name = "box"
	cfg.Folder = t.TempDir()
	cfg.Record.LogsCatalogue = false
	now := start
	var mono time.Duration
	store, err := blackbox.OpenStore(cfg.Folder, blackbox.StoreOptions{Name: "box", Version: "test", Host: "H", IntervalSeconds: 15, RunID: "r1", Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	rec := blackbox.NewRecorder(blackbox.RecorderOptions{Config: cfg, Store: store, Collector: fakeCollector{}, Disk: okDisk{}, RunID: "r1",
		Now: func() time.Time { return now }, Mono: func() time.Duration { return mono }})
	rec.Tick(now)
	for i := 0; i < ticks; i++ {
		now = now.Add(15 * time.Second)
		mono += 15 * time.Second
		rec.Tick(now)
	}
	if err := store.Close("test"); err != nil {
		t.Fatal(err)
	}
	return cfg.Folder
}

func TestLiteServesOneBlackboxEndToEnd(t *testing.T) {
	start := time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Second)
	dir := record(t, start, 20)
	// The saved place must be a Windows path to pass the server's own check; the reader is pointed at the real folder.
	loc := blackbox.Location{Kind: blackbox.LocFolder, Path: `D:\lite-test`}
	const token = "0123456789abcdef0123456789abcdef"
	ui := fstest.MapFS{"index.html": {Data: []byte("<html>lite</html>")}}
	h := Wrap(server.New(NoData{}, ui, token, false,
		server.WithBlackbox(server.BlackboxDeps{Store: NewStore("This PC", loc, time.Now()),
			Open: func(l blackbox.Location, _ string) blackbox.Source {
				return &blackbox.FolderSource{Loc: l, Access: blackbox.DirAccess{Dir: dir}}
			}}),
		server.WithLaunchCode("launch-code-launch-code", time.Now().Add(time.Minute))))
	srv := httptest.NewServer(h)
	defer srv.Close()

	do := func(method, path, body string, withToken bool) (int, []byte) {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		if withToken {
			req.Header.Set("X-Audit-Token", token)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b
	}

	if code, b := do("GET", "/", "", false); code != 200 || !strings.Contains(string(b), "lite") {
		t.Fatalf("page: %d %s", code, b)
	}
	if code, _ := do("GET", "/api/blackbox", "", false); code != 401 {
		t.Fatalf("list without token: %d", code)
	}
	code, b := do("GET", "/api/blackbox", "", true)
	if code != 200 {
		t.Fatalf("list: %d %s", code, b)
	}
	var list struct {
		Items []struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Facts *struct {
				Samples int `json:"samples"`
			} `json:"facts"`
		} `json:"items"`
	}
	if err := json.Unmarshal(b, &list); err != nil || len(list.Items) != 1 || list.Items[0].ID != ID {
		t.Fatalf("list body: %v %s", err, b)
	}
	if list.Items[0].Facts == nil || list.Items[0].Facts.Samples < 20 {
		t.Fatalf("the real recordings were not read: %s", b)
	}
	body := `{"start":"` + start.Format(time.RFC3339) + `","end":"` + start.Add(6*time.Minute).Format(time.RFC3339) + `","points":50}`
	code, b = do("POST", "/api/blackbox/"+ID+"/series", body, true)
	if code != 200 || !strings.Contains(string(b), "42") {
		t.Fatalf("series: %d %s", code, b)
	}
	// Everything else is gone, with or without the token.
	for _, c := range [][2]string{{"POST", "/api/blackbox/" + ID + "/key"}, {"DELETE", "/api/blackbox/" + ID}, {"GET", "/api/sources"}, {"POST", "/api/blackbox/this-pc/install"}, {"GET", "/api/session-proof?n=x"}} {
		if code, _ := do(c[0], c[1], "{}", true); code != 404 {
			t.Errorf("%s %s answered %d, want 404", c[0], c[1], code)
		}
	}
}

func TestStoreWritesNothing(t *testing.T) {
	loc := blackbox.Location{Kind: blackbox.LocFolder, Path: t.TempDir()}
	s := NewStore("x", loc, time.Now())
	if _, err := s.Add(bbsaved.AddInput{Name: "y", Location: loc}); err == nil {
		t.Fatal("Add must fail")
	}
	if s.SetKey(ID, "k") == nil || s.Remove(ID) == nil || s.SetThisPC(ID, true) == nil || s.SetRecorder(ID, "a", "b") == nil {
		t.Fatal("writes must fail")
	}
	if k, err := s.Key(ID); k != "" || err != nil {
		t.Fatal("no key is ever held")
	}
	if _, err := s.Get("bb-other"); err == nil {
		t.Fatal("unknown id must fail")
	}
}
