package blackbox

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

func sampleSourcesDoc() SourcesDoc {
	return SourcesDoc{Schema: SourcesSchema, Host: "H1", Generated: time.Unix(1700000000, 0).UTC(),
		Sources: []SourceEntry{{ID: "iis.site.1", Kind: SourceIIS, Label: "IIS site A", Dir: `D:\Logs\W3SVC1`, Pattern: "*.log", Origin: "applicationHost.config", Exists: true, Readable: true, Files: 3, Bytes: 900}}}
}

func TestParseSourcesRefusesNewerSchemaAndGarbage(t *testing.T) {
	good, _ := json.Marshal(sampleSourcesDoc())
	if d, err := ParseSources(good); err != nil || len(d.Sources) != 1 {
		t.Fatalf("good: %+v %v", d, err)
	}
	newer := sampleSourcesDoc()
	newer.Schema = SourcesSchema + 1
	nb, _ := json.Marshal(newer)
	if _, err := ParseSources(nb); err == nil || err.Kind != ReadNewerFormat {
		t.Fatalf("newer: %v", err)
	}
	for _, bad := range []string{"", "not json", "{}", `{"schema":0}`} {
		if _, err := ParseSources([]byte(bad)); err == nil || err.Kind != ReadNotFound {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestParseSourcesCapsEntries(t *testing.T) {
	d := sampleSourcesDoc()
	for i := 0; i < maxSourceEntries+50; i++ {
		d.Sources = append(d.Sources, d.Sources[0])
	}
	b, _ := json.Marshal(d)
	got, err := ParseSources(b)
	if err != nil || len(got.Sources) != maxSourceEntries {
		t.Fatalf("%d %v", len(got.Sources), err)
	}
}

func TestFolderReadsTheSourcesManifest(t *testing.T) {
	dir := t.TempDir()
	src := NewFolderSource(Location{Kind: LocFolder, Path: dir})
	if _, err := src.Sources(context.Background()); KindOf(err) != ReadNotFound {
		t.Fatalf("no file yet: %v", err)
	}
	if err := WriteSourcesFile(dir, sampleSourcesDoc()); err != nil {
		t.Fatal(err)
	}
	d, err := src.Sources(context.Background())
	if err != nil || d.Host != "H1" || len(d.Sources) != 1 {
		t.Fatalf("%+v %v", d, err)
	}
}

func TestServerServesSourcesWithKeyOnly(t *testing.T) {
	g := newRig(t, nil)
	key, _ := NewReadKey()
	hash, _ := HashKey(key)
	kc, _ := NewKeyCheck(hash)
	var have bool
	doc := sampleSourcesDoc()
	srv := NewServer(ServerOptions{Store: g.s, Recorder: g.r, Config: g.r.o.Config, Keys: kc, Now: g.clk.now,
		Sources: func() (SourcesDoc, bool) { return doc, have }})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	sr := &srvRig{rig: g, srv: srv, ts: ts, key: key, hash: hash}

	if resp, _ := sr.get(t, "/sources", ""); resp.StatusCode != 401 { //nolint:bodyclose // closed by get
		t.Fatalf("no key: %d", resp.StatusCode)
	}
	if resp, _ := sr.get(t, "/sources", key); resp.StatusCode != 404 { //nolint:bodyclose // closed by get
		t.Fatalf("before the first look: %d", resp.StatusCode)
	}
	have = true
	resp, body := sr.get(t, "/sources", key) //nolint:bodyclose // closed by get
	if resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	got, err := httpSource(t, ts, key).Sources(context.Background())
	if err != nil || len(got.Sources) != 1 || got.Sources[0].Dir != `D:\Logs\W3SVC1` {
		t.Fatalf("%+v %v", got, err)
	}
}
