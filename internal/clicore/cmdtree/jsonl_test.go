package cmdtree

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"api-audit-explorer/internal/clicore"
)

// pager makes an owner that answers audit.requests with n rows r0.. in pages, cursor "cK" meaning "start at row K". fail is the
// index of the call (from 0) that answers an error instead; calls records the arguments of every call.
type pager struct {
	n     int
	fail  int
	row   func(i int) string
	meta  string
	calls []map[string]any
}

func newPager(n int) *pager {
	return &pager{n: n, fail: -1, row: func(i int) string { return fmt.Sprintf(`{"id":"r%d"}`, i) }}
}

func (p *pager) handler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Args map[string]any `json:"args"`
	}
	b, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(b, &req)
	p.calls = append(p.calls, req.Args)
	if len(p.calls)-1 == p.fail {
		_, _ = io.WriteString(w, `{"ok":false,"error":{"code":"RESULT_EXPIRED","message":"gone"}}`)
		return
	}
	start := 0
	if c, ok := req.Args["cursor"].(string); ok {
		start, _ = strconv.Atoi(strings.TrimPrefix(c, "c"))
	}
	limit := 20
	if l, ok := req.Args["limit"].(float64); ok {
		limit = int(l)
	}
	end := min(start+limit, p.n)
	rows := []string{}
	for i := start; i < end; i++ {
		rows = append(rows, p.row(i))
	}
	next := ""
	if end < p.n {
		next = fmt.Sprintf(`,"nextCursor":"c%d"`, end)
	}
	meta := p.meta
	if meta == "" {
		meta = `{"coverage":"complete"}`
	}
	_, _ = io.WriteString(w, `{"ok":true,"data":{"rows":[`+strings.Join(rows, ",")+`]`+next+`},"meta":`+meta+`}`)
}

func (p *pager) owner(t *testing.T) *owner {
	o := newOwner(t, nil)
	o.serve(200, o.helloBody(nil), p.handler)
	return o
}

func streamArgs(extra ...string) []string {
	return append([]string{"audit", "requests", "--result-id", "r", "--api-key", "k", "-o", "jsonl"}, extra...)
}

func lines(t *testing.T, s string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, l := range strings.Split(strings.TrimSuffix(s, "\n"), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("not JSON: %q", l)
		}
		out = append(out, m)
	}
	return out
}

func ids(ls []map[string]any) []string {
	var out []string
	for _, l := range ls {
		if l["type"] == "item" {
			out = append(out, l["data"].(map[string]any)["id"].(string))
		}
	}
	return out
}

func TestJSONLStreamsEveryPage(t *testing.T) {
	p := newPager(7)
	r := p.owner(t).run(streamArgs("--limit", "3")...)
	if r.code != 0 || r.err != "" {
		t.Fatalf("%+v", r)
	}
	ls := lines(t, r.out)
	if len(ls) != 9 || ls[0]["type"] != "meta" || ls[0]["operation"] != "audit.requests" || ls[0]["schemaVersion"] != "1" || ls[0]["requestId"] == "" {
		t.Fatalf("lines: %s", r.out)
	}
	if got := strings.Join(ids(ls), ","); got != "r0,r1,r2,r3,r4,r5,r6" {
		t.Errorf("rows: %s", got)
	}
	end := ls[8]
	if end["type"] != "complete" || end["coverage"] != "complete" || end["nextCursor"] != nil || end["rows"] != float64(7) {
		t.Errorf("end: %v", end)
	}
	// one page in flight, three calls of three rows, the cursor is the owner's
	if len(p.calls) != 3 || p.calls[0]["cursor"] != nil || p.calls[1]["cursor"] != "c3" || p.calls[2]["cursor"] != "c6" || p.calls[0]["limit"] != float64(3) {
		t.Errorf("calls: %v", p.calls)
	}
	for _, l := range ls {
		if l["requestId"] != ls[0]["requestId"] {
			t.Errorf("request id changes: %v", l)
		}
	}
}

func TestJSONLRowCapEndsPartialWithACursorToContinue(t *testing.T) {
	p := newPager(7)
	o := p.owner(t)
	r := o.run(streamArgs("--limit", "3", "--max-rows", "5")...)
	ls := lines(t, r.out)
	end := ls[len(ls)-1]
	if r.code != 0 || end["rows"] != float64(5) || end["coverage"] != "partial" || end["nextCursor"] != "c5" {
		t.Fatalf("%+v", r)
	}
	if p.calls[1]["limit"] != float64(2) {
		t.Errorf("the last page asks only for what is left: %v", p.calls)
	}
	r = o.run(streamArgs("--limit", "3", "--cursor", "c5")...)
	ls = lines(t, r.out)
	if strings.Join(ids(ls), ",") != "r5,r6" || ls[len(ls)-1]["coverage"] != "complete" {
		t.Errorf("continue: %s", r.out)
	}
}

func TestJSONLByteCapStopsBetweenPages(t *testing.T) {
	p := newPager(7)
	o := p.owner(t)
	full := o.run(streamArgs("--limit", "3", "--max-rows", "3")...)
	meta, item := 0, 0
	for i, l := range strings.SplitAfter(full.out, "\n") {
		if i == 0 {
			meta = len(l)
		}
		if i == 1 {
			item = len(l)
		}
	}
	cap := meta + 3*item // the meta line and one page of three rows
	r := o.run(streamArgs("--limit", "3", "--max-bytes", strconv.Itoa(cap))...)
	ls := lines(t, r.out)
	end := ls[len(ls)-1]
	if r.code != 0 || strings.Join(ids(ls), ",") != "r0,r1,r2" || end["coverage"] != "partial" || end["nextCursor"] != "c3" || end["rows"] != float64(3) {
		t.Fatalf("%+v", r)
	}
	if len(r.out)-len(strings.SplitAfter(r.out, "\n")[len(ls)-1]) > cap {
		t.Errorf("more than the cap was written")
	}
	// a first page that does not fit is an error, with nothing on stdout
	r = o.run(streamArgs("--limit", "3", "--max-bytes", "10")...)
	if r.code != 5 || errCode2(t, r) != "RESULT_TOO_LARGE" || r.out != "" {
		t.Errorf("first page: %+v", r)
	}
}

// errCode2 reads the error object of a failed stream (the stdout of it is not empty, so errCode cannot be used).
func errCode2(t *testing.T, r result) string {
	t.Helper()
	m := decode(t, r.err)
	e, _ := m["error"].(map[string]any)
	if e == nil {
		t.Fatalf("no error object: %q", r.err)
	}
	return e["code"].(string)
}

func TestJSONLFailureMidStreamHasNoCompleteLine(t *testing.T) {
	p := newPager(9)
	p.fail = 1
	r := p.owner(t).run(streamArgs("--limit", "3")...)
	ls := lines(t, r.out)
	if r.code != 5 || errCode2(t, r) != "RESULT_EXPIRED" {
		t.Errorf("%+v", r)
	}
	if strings.Join(ids(ls), ",") != "r0,r1,r2" {
		t.Errorf("the first page is printed: %s", r.out)
	}
	for _, l := range ls {
		if l["type"] == "complete" {
			t.Error("a complete line after a failure")
		}
	}
	// a failure of the first page prints nothing at all
	p = newPager(9)
	p.fail = 0
	if r := p.owner(t).run(streamArgs()...); r.code != 5 || r.out != "" {
		t.Errorf("first page: %+v", r)
	}
}

func TestJSONLRowTooLargeAndNonASCII(t *testing.T) {
	p := newPager(2)
	p.row = func(i int) string { return `{"id":"` + strings.Repeat("a", 70<<10) + `"}` }
	if r := p.owner(t).run(streamArgs()...); r.code != 5 || errCode2(t, r) != "RESULT_TOO_LARGE" || r.out != "" {
		t.Errorf("long row: %.200v", r)
	}
	p = newPager(2)
	p.row = func(i int) string { return `{"id":"é😀 é😀"}` }
	r := p.owner(t).run(streamArgs()...)
	for _, c := range []byte(r.out) {
		if c >= 0x80 {
			t.Fatalf("not 7-bit: %q", r.out)
		}
	}
	if got := ids(lines(t, r.out))[0]; got != "é😀 é😀" {
		t.Errorf("row: %q", got)
	}
	if err := strings.Count(r.out, "\n"); err != 4 { // meta, two rows, complete
		t.Errorf("a row spans lines: %q", r.out)
	}
}

func TestJSONLCoverageAndEmptyAndGuards(t *testing.T) {
	p := newPager(4)
	p.meta = `{"coverage":"partial"}`
	ls := lines(t, p.owner(t).run(streamArgs()...).out)
	if ls[len(ls)-1]["coverage"] != "partial" || ls[len(ls)-1]["nextCursor"] != nil {
		t.Errorf("a partial page makes a partial stream: %v", ls[len(ls)-1])
	}
	ls = lines(t, newPager(0).owner(t).run(streamArgs()...).out)
	if len(ls) != 2 || ls[1]["rows"] != float64(0) || ls[1]["coverage"] != "complete" {
		t.Errorf("empty: %v", ls)
	}
	// an owner that sends a cursor with no rows, or the cursor it was given, would loop for ever
	o := newOwner(t, nil)
	o.serve(200, o.helloBody(nil), func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"ok":true,"data":{"rows":[],"nextCursor":"c1"}}`)
	})
	if r := o.run(streamArgs()...); r.code != 1 || errCode2(t, r) != "INTERNAL" {
		t.Errorf("empty page with a cursor: %+v", r)
	}
	// an empty page with a new cursor each time must not keep the command running until the timeout
	n := 0
	o = newOwner(t, nil)
	o.serve(200, o.helloBody(nil), func(w http.ResponseWriter, _ *http.Request) {
		n++
		_, _ = io.WriteString(w, fmt.Sprintf(`{"ok":true,"data":{"rows":[],"nextCursor":"c%d"}}`, n))
	})
	if r := o.run(streamArgs("--timeout", "3s")...); r.code != 1 || errCode2(t, r) != "INTERNAL" || n > 2 {
		t.Errorf("empty pages: %+v calls %d", r, n)
	}
	o = newOwner(t, nil)
	o.serve(200, o.helloBody(nil), func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"ok":true,"data":{"rows":[{"id":"a"}],"nextCursor":"same"}}`)
	})
	if r := o.run(streamArgs("--cursor", "same")...); r.code != 1 || errCode2(t, r) != "INTERNAL" {
		t.Errorf("same cursor: %+v", r)
	}
	for _, body := range []string{`{"ok":true,"data":{"nothing":1}}`, `{"ok":true,"data":[]}`} {
		o = newOwner(t, nil)
		o.serve(200, o.helloBody(nil), func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) })
		if r := o.run(streamArgs()...); r.code != 1 || r.out != "" {
			t.Errorf("%s: %+v", body, r)
		}
	}
}

func TestJSONLFlagChecks(t *testing.T) {
	o := newPager(3).owner(t)
	for name, args := range map[string][]string{
		"rows zero":      streamArgs("--max-rows", "0"),
		"rows too many":  streamArgs("--max-rows", "10001"),
		"bytes zero":     streamArgs("--max-bytes", "0"),
		"bytes too many": streamArgs("--max-bytes", "67108865"),
		"rows in json":   {"audit", "requests", "--result-id", "r", "--api-key", "k", "--json", "--max-rows", "5"},
		"bytes in table": {"audit", "requests", "--result-id", "r", "--api-key", "k", "--max-bytes", "5"},
		"output file":    streamArgs("--output-file", "x.jsonl"),
		"other command":  {"audit", "timeline", "--result-id", "r", "--api-key", "k", "-o", "jsonl"},
	} {
		if r := o.run(args...); r.code == 0 {
			t.Errorf("%s: %+v", name, r)
		}
	}
	if r := o.run(streamArgs("--max-rows", "0")...); r.code != 2 {
		t.Errorf("rows zero: %+v", r)
	}
	if r := o.run("audit", "timeline", "--result-id", "r", "--api-key", "k", "-o", "jsonl"); r.code != 2 || errCode2(t, r) != "UNSUPPORTED_FORMAT" {
		t.Errorf("a command with no stream: %+v", r)
	}
	// the request file is read as a whole: a name that comes twice is refused, a limit in it is the page size
	r := runOn(t, o.attacher(), `{"resultId":"r","apiKey":"k","limit":2}`, nil, "audit", "requests", "-o", "jsonl", "--request", "-")
	if r.code != 0 || len(lines(t, r.out)) != 5 {
		t.Errorf("request: %+v", r)
	}
	r = runOn(t, o.attacher(), `{"resultId":"r","apiKey":"k","limit":2,"limit":3}`, nil, "audit", "requests", "-o", "jsonl", "--request", "-")
	if r.code != 2 {
		t.Errorf("repeated name: %+v", r)
	}
}

// A limit the owner refuses is the owner's to refuse, in a stream too.
func TestJSONLBadLimitIsTheOwnersToRefuse(t *testing.T) {
	o := newOwner(t, nil)
	id, key := compareKey(t, o)
	r := o.run("audit", "requests", "--result-id", id, "--api-key", key, "-o", "jsonl", "--limit", "0")
	if r.code != 2 || errCode2(t, r) != "INVALID_ARGUMENT" || r.out != "" {
		t.Errorf("%+v", r)
	}
}

// The real path over the demo data: the stream holds the rows that json mode pages, in the same order.
func TestJSONLEqualsJSONPages(t *testing.T) {
	o := newOwner(t, nil)
	id, key := compareKey(t, o)
	d := data(t, o.run("audit", "requests", "--result-id", id, "--api-key", key, "--limit", "100", "--json"))
	var want []string
	for _, r := range d["rows"].([]any) {
		want = append(want, r.(map[string]any)["id"].(string))
	}
	if len(want) < 4 {
		t.Fatalf("the demo group has %d rows", len(want))
	}
	r := o.run("audit", "requests", "--result-id", id, "--api-key", key, "-o", "jsonl", "--limit", "3")
	got := ids(lines(t, r.out))
	if r.code != 0 || len(got) < len(want) || strings.Join(got[:len(want)], ",") != strings.Join(want, ",") {
		t.Errorf("stream %v, pages %v", got, want)
	}
	if d["nextCursor"] == nil || d["nextCursor"] == "" {
		if len(got) != len(want) {
			t.Errorf("the stream has more rows than the only page: %d vs %d", len(got), len(want))
		}
	}
	// and slowest
	sl := o.run("audit", "slowest", "--result-id", id, "-o", "jsonl", "--limit", "2", "--max-rows", "3")
	ls := lines(t, sl.out)
	if sl.code != 0 || ls[len(ls)-1]["operation"] != "audit.slowest" || len(ids(ls)) == 0 || len(ids(ls)) > 3 {
		t.Errorf("slowest: %+v", sl)
	}
}

type epipeWriter struct{ n int }

func (w *epipeWriter) Write(p []byte) (int, error) {
	w.n++
	if w.n > 1 {
		return 0, syscall.EPIPE
	}
	return len(p), nil
}

// A reader that closes the pipe stops the stream: no more pages are asked for, and nothing is written to stderr.
func TestJSONLClosedPipeStopsQuietly(t *testing.T) {
	p := newPager(30)
	o := p.owner(t)
	var errb bytes.Buffer
	code := Run(context.Background(), streamArgs("--limit", "3"), IO{In: strings.NewReader(""), Out: &epipeWriter{}, Err: &errb,
		Getenv: func(string) string { return "" }, Exe: func() (string, error) { return "/opt/unravis/bin/unravis.exe", nil }, Attach: o.attacher().Attach})
	if code != clicore.ExitStopped || errb.Len() != 0 || len(p.calls) != 2 {
		t.Errorf("code %d, stderr %q, calls %d", code, errb.String(), len(p.calls))
	}
}

// A result id belongs to the app that made it: after a restart (a new owner) the id is gone, with RESULT_EXPIRED and exit 5, and
// every follow-up says the same (BL-388).
func TestFollowUpAfterRestartIsExpired(t *testing.T) {
	a := newOwner(t, nil)
	id, key := compareKey(t, a)
	b := newOwner(t, nil)
	for _, args := range [][]string{
		{"audit", "requests", "--result-id", id, "--api-key", key, "--json"},
		{"audit", "slowest", "--result-id", id, "--json"},
		{"audit", "breakdown", "--result-id", id, "--dim", "statusCode", "--json"},
		{"audit", "groups", "--result-id", id, "--json"},
		{"audit", "requests", "--result-id", id, "--api-key", key, "-o", "jsonl"},
	} {
		if r := b.run(args...); r.code != 5 || errCode2(t, r) != "RESULT_EXPIRED" || r.out != "" {
			t.Errorf("%v: %+v", args, r)
		}
	}
	if r := a.run("audit", "slowest", "--result-id", id, "--json"); r.code != 0 {
		t.Errorf("the owner that made it still has it: %+v", r)
	}
}
