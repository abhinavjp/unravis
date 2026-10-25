package discover_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/discover"
	"api-audit-explorer/internal/discover/fake"
)

func walkNames(r discover.WalkResult) string {
	var n []string
	for _, f := range r.Files {
		n = append(n, f.Path)
	}
	return strings.Join(n, "|")
}

func TestWalkListsFilesBreadthFirst(t *testing.T) {
	e := fake.New()
	e.FS.AddFile(`D:\L\a.log`, 1, day, []byte("x"))
	e.FS.AddFile(`D:\L\s1\b.log`, 1, day, []byte("x"))
	e.FS.AddFile(`D:\L\s1\s2\c.log`, 1, day, []byte("x"))
	r, err := discover.Walk(context.Background(), e.FS, `D:\L`, 3, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := walkNames(r); got != `D:\L\a.log|D:\L\s1\b.log|D:\L\s1\s2\c.log` {
		t.Errorf("files = %s", got)
	}
	if r.Denied || r.Truncated || len(r.Reparse) != 0 {
		t.Errorf("result = %+v", r)
	}
	if r.Files[1].Dir != `D:\L\s1` {
		t.Errorf("dir = %q", r.Files[1].Dir)
	}
}

func TestWalkKeepFilter(t *testing.T) {
	e := fake.New()
	e.FS.AddFile(`D:\L\a.log`, 1, day, []byte("x"))
	e.FS.AddFile(`D:\L\a.txt`, 1, day, []byte("x"))
	r, _ := discover.Walk(context.Background(), e.FS, `D:\L`, 0, func(fi discover.FileInfo) bool {
		return strings.HasSuffix(fi.Name, ".log")
	})
	if got := walkNames(r); got != `D:\L\a.log` {
		t.Errorf("files = %s", got)
	}
}

func TestWalkSkipsReparse(t *testing.T) {
	e := fake.New()
	e.FS.AddFile(`D:\L\a.log`, 1, day, []byte("x"))
	e.FS.AddReparse(`D:\L\link`)
	e.FS.AddFile(`D:\L\real\b.log`, 1, day, []byte("x"))
	r, err := discover.Walk(context.Background(), e.FS, `D:\L`, 3, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := walkNames(r); got != `D:\L\a.log|D:\L\real\b.log` {
		t.Errorf("files = %s", got)
	}
	if len(r.Reparse) != 1 || r.Reparse[0] != `D:\L\link` {
		t.Errorf("reparse = %v", r.Reparse)
	}
	if n := e.FS.Calls["readdir"]; n != 2 {
		t.Errorf("ReadDir calls = %d, want 2 (root and real)", n)
	}
}

func TestWalkDepthCap(t *testing.T) {
	e := fake.New()
	e.FS.AddFile(`D:\L\d1\d2\d3\d4\deep.log`, 1, day, []byte("x"))
	e.FS.AddFile(`D:\L\d1\d2\d3\ok.log`, 1, day, []byte("x"))
	// A caller asking for more than the cap still gets the cap.
	r, _ := discover.Walk(context.Background(), e.FS, `D:\L`, 10, nil)
	if got := walkNames(r); got != `D:\L\d1\d2\d3\ok.log` {
		t.Errorf("files = %s", got)
	}
	r, _ = discover.Walk(context.Background(), e.FS, `D:\L`, 1, nil)
	if len(r.Files) != 0 {
		t.Errorf("depth 1 files = %s", walkNames(r))
	}
	r, _ = discover.Walk(context.Background(), e.FS, `D:\L`, -4, nil)
	if len(r.Files) != 0 {
		t.Errorf("negative depth files = %s", walkNames(r))
	}
}

func TestWalkEntryCapIsTruncated(t *testing.T) {
	e := fake.New()
	for i := 0; i < 2600; i++ {
		e.FS.AddFile(fmt.Sprintf(`D:\L\a\f%05d.log`, i), 1, day, []byte("x"))
		e.FS.AddFile(fmt.Sprintf(`D:\L\b\f%05d.log`, i), 1, day, []byte("x"))
	}
	r, err := discover.Walk(context.Background(), e.FS, `D:\L`, 3, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Truncated || len(r.Files) > discover.WalkMaxEntries {
		t.Errorf("truncated = %v files = %d", r.Truncated, len(r.Files))
	}
}

func TestWalkRootErrorsAreReturned(t *testing.T) {
	e := fake.New()
	if _, err := discover.Walk(context.Background(), e.FS, `D:\Nope`, 1, nil); !discover.IsNotFound(err) {
		t.Errorf("missing root: %v", err)
	}
	e.FS.AddFile(`D:\L\a.log`, 1, day, []byte("x"))
	e.FS.Deny(`D:\L`)
	if _, err := discover.Walk(context.Background(), e.FS, `D:\L`, 1, nil); !discover.IsAccessDenied(err) {
		t.Errorf("denied root: %v", err)
	}
}

func TestWalkDeniedSubfolderDoesNotHideSiblings(t *testing.T) {
	e := fake.New()
	e.FS.AddFile(`D:\L\locked\a.log`, 1, day, []byte("x"))
	e.FS.AddFile(`D:\L\open\b.log`, 1, day, []byte("x"))
	e.FS.Deny(`D:\L\locked`)
	r, err := discover.Walk(context.Background(), e.FS, `D:\L`, 2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Denied || walkNames(r) != `D:\L\open\b.log` {
		t.Errorf("result = %+v", r)
	}
}

func TestWalkContextCancel(t *testing.T) {
	e := fake.New()
	e.FS.AddFile(`D:\L\a.log`, 1, day, []byte("x"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r, err := discover.Walk(ctx, e.FS, `D:\L`, 1, nil)
	if err != nil || !r.Truncated || len(r.Files) != 0 {
		t.Errorf("result = %+v err = %v", r, err)
	}
}

func TestWalkNonASCII(t *testing.T) {
	e := fake.New()
	e.FS.AddFile(`D:\Logs\Café-日本\日志.log`, 1, day, []byte("x"))
	r, err := discover.Walk(context.Background(), e.FS, `D:\Logs`, 2, nil)
	if err != nil || walkNames(r) != `D:\Logs\Café-日本\日志.log` {
		t.Errorf("files = %s err = %v", walkNames(r), err)
	}
}

func TestSortOldestFirst(t *testing.T) {
	mk := func(name string, d int) discover.Walked {
		return discover.Walked{Path: name, Info: discover.FileInfo{Name: name, ModTime: day.Add(time.Duration(d) * time.Hour)}}
	}
	files := []discover.Walked{mk("c", 2), mk("B", 1), mk("a", 1), mk("z", 0)}
	discover.SortOldestFirst(files)
	var got []string
	for _, f := range files {
		got = append(got, f.Path)
	}
	if strings.Join(got, ",") != "z,a,B,c" {
		t.Errorf("order = %v", got)
	}
}
