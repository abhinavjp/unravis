package service

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"api-audit-explorer/internal/remote/registry"
)

func TestParseSources(t *testing.T) {
	e := registry.Entry{Folders: map[string][]string{registry.FolderIIS: {`C:\logs`}}}
	data := `{"schema":1,"host":" host ","version":"1","generated":"today","extra":true,"sources":[{"id":" a ","kind":"iis","dir":" C:\\logs ","pattern":"*.log","files":12,"bytes":42,"newestUnix":3,"exists":true,"readable":true,"unknown":1},{"dir":" "},{"kind":"iis","dir":"C:\\logs","pattern":"*.log"},{"kind":"sqlserver","dir":"D:\\sql"}]}`
	got, err := parseSources([]byte(data), e)
	if err != nil || !got.Found || got.Host != "host" || len(got.Sources) != 2 {
		t.Fatalf("parse = %+v, %v", got, err)
	}
	a, b := got.Sources[0], got.Sources[1]
	if a.ID != "a" || !a.Allowed || a.Folder != registry.FolderIIS || a.Files != 12 || a.Bytes != 42 || !a.Readable || b.Allowed || b.Folder != registry.FolderSQL {
		t.Fatalf("sources = %+v", got.Sources)
	}
	for _, tc := range []struct{ data, note string }{{`{"schema":2}`, "newer"}, {`{`, "did not publish"}, {strings.Repeat("x", sourcesLimit+1), "did not publish"}} {
		got, err := parseSources([]byte(tc.data), e)
		if err != nil || got.Found || !strings.Contains(got.Note, tc.note) {
			t.Errorf("invalid = %+v, %v", got, err)
		}
	}
	var entries []string
	for i := 0; i < 205; i++ {
		entries = append(entries, fmt.Sprintf(`{"dir":"/log/%d"}`, i))
	}
	got, _ = parseSources([]byte(`{"schema":1,"sources":[`+strings.Join(entries, ",")+`]}`), e)
	if len(got.Sources) != 200 {
		t.Fatalf("cap = %d", len(got.Sources))
	}
}

func TestDiscover(t *testing.T) {
	r := newRig(t, func(in *registry.AddInput) { in.Folders[registry.FolderBlackbox] = []string{`D:\A`, `D:\B`} })
	r.pin(t)
	r.conn.files[`D:\B`] = map[string]string{"sources.json": `{"schema":1,"sources":[{"kind":"iis","dir":"C:\\inetpub\\logs\\LogFiles\\W3SVC1"},{"kind":"iis","dir":"C:\\other"}]}`}
	got, err := r.svc.Discover(context.Background(), r.id, "")
	if err != nil || !got.Found || got.From != `D:\B\sources.json` || len(got.Sources) != 2 || !got.Sources[0].Allowed || got.Sources[1].Allowed {
		t.Fatalf("discover = %+v, %v", got, err)
	}
	delete(r.conn.files, `D:\B`)
	got, err = r.svc.Discover(context.Background(), r.id, "")
	if err != nil || got.Found || got.Note == "" {
		t.Fatalf("missing = %+v, %v", got, err)
	}
}
