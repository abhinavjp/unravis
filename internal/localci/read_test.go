package localci

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReadLedgerSplitsKeysAndSortsNewestFirst(t *testing.T) {
	dir := t.TempDir()
	if rows, err := ReadLedger(dir); err != nil || rows != nil {
		t.Fatalf("a missing ledger is an empty list: %v %v", rows, err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "status"), 0o750); err != nil {
		t.Fatal(err)
	}
	body := `{"o/r@abc123 fast-build":{"state":"success","description":"ok","at":"2026-11-25T10:00:00Z"},"o/r@def456 fast-test":{"state":"failure","description":"bad","at":"2026-11-25T11:00:00Z"}}`
	if err := os.WriteFile(LedgerPath(dir), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	rows, err := ReadLedger(dir)
	if err != nil || len(rows) != 2 {
		t.Fatalf("%v %v", rows, err)
	}
	if rows[0].SHA != "def456" || rows[0].Repo != "o/r" || rows[0].Context != "fast-test" || rows[0].State != "failure" || rows[1].Context != "fast-build" {
		t.Fatalf("%+v", rows)
	}
}

func TestListRequestsNeverCreatesFolders(t *testing.T) {
	dir := t.TempDir()
	if rs, err := ListRequests(dir); err != nil || len(rs) != 0 {
		t.Fatal(rs, err)
	}
	if _, err := os.Stat(QueueDir(dir)); err == nil {
		t.Fatal("reading created the queue folder")
	}
	q, err := OpenQueue(QueueDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := q.Enqueue(Request{Kind: KindPush, Repo: "o/r", Ref: "main", SHA: "abc1234", Workflow: "fast-gate.yml"}); err != nil {
		t.Fatal(err)
	}
	rs, err := ListRequests(dir)
	if err != nil || len(rs) != 1 || rs[0].State != StateQueued {
		t.Fatalf("%+v %v", rs, err)
	}
}

func TestLiveRunIDMatchesWholeLabelsOnly(t *testing.T) {
	dir := t.TempDir()
	st, err := OpenStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, _, err := st.NewRun(now, "pr282-fast"); err != nil { // not the label of a "fast-gate" request
		t.Fatal(err)
	}
	want, _, err := st.NewRun(now, "pr282-fast-gate")
	if err != nil {
		t.Fatal(err)
	}
	req := Request{Kind: KindPR, Number: 282, Workflow: "fast-gate.yml", ClaimedAt: now.Add(-time.Second), State: StateRunning}
	if got := st.LiveRunID(req); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	// A run that has its record is over: it is not live.
	if err := WriteRunRecord(filepath.Join(st.RunsPath(), want), &RunResult{Conclusion: "success"}); err != nil {
		t.Fatal(err)
	}
	if got := st.LiveRunID(req); got != "" {
		t.Fatalf("a recorded run is not live: %q", got)
	}
	// A request claimed after the folder was made does not take it.
	req.ClaimedAt = now.Add(time.Hour)
	if got := st.LiveRunID(req); got != "" {
		t.Fatalf("%q", got)
	}
}

func TestIDTime(t *testing.T) {
	if tm, ok := IDTime("20261125-093314-pr1-x"); !ok || tm.Hour() != 9 || tm.Minute() != 33 {
		t.Fatal(tm, ok)
	}
	for _, bad := range []string{"", "short", "2026112-093314-x", "abcdefgh-ijklmn-x"} {
		if _, ok := IDTime(bad); ok {
			t.Errorf("%q", bad)
		}
	}
}

func TestSaveAndReadCompare(t *testing.T) {
	dir := t.TempDir()
	if rep, err := ReadCompare(dir); err != nil || rep != nil {
		t.Fatalf("missing is (nil, nil): %v %v", rep, err)
	}
	in := &CompareReport{Repo: "o/r", Generated: time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC), Agree: 3, Streak: 3, Gate: 20, Disagreeing: []string{"x"}}
	if err := SaveCompare(dir, in); err != nil {
		t.Fatal(err)
	}
	out, err := ReadCompare(dir)
	if err != nil || out == nil || out.Repo != "o/r" || out.Streak != 3 || len(out.Disagreeing) != 1 || !out.Generated.Equal(in.Generated) {
		t.Fatalf("%+v %v", out, err)
	}
	if ents, _ := os.ReadDir(filepath.Dir(ComparePath(dir))); len(ents) != 1 {
		t.Fatalf("a temporary file was left behind: %v", ents)
	}
	if err := os.WriteFile(ComparePath(dir), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCompare(dir); err == nil {
		t.Fatal("a broken file must be an error, not an empty report")
	}
}
