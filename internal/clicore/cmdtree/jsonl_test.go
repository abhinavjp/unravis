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
	"sync"
	"syscall"
	"testing"
	"time"

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
	// metaFn, when set, makes the meta of call i (from 0); hasNext tells whether the page has a cursor. extra is added to the data.
	metaFn    func(i int, hasNext bool) string
	extra     string
	deadlines []float64
}

func newPager(n int) *pager {
	return &pager{n: n, fail: -1, row: func(i int) string { return fmt.Sprintf(`{"id":"r%d"}`, i) }}
}

func (p *pager) handler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Args       map[string]any `json:"args"`
		DeadlineMs float64        `json:"deadlineMs"`
	}
	b, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(b, &req)
	p.calls = append(p.calls, req.Args)
	p.deadlines = append(p.deadlines, req.DeadlineMs)
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
	if p.metaFn != nil {
		meta = p.metaFn(len(p.calls)-1, next != "")
	}
	if meta == "" {
		meta = `{"coverage":"complete"}`
	}
	_, _ = io.WriteString(w, `{"ok":true,"data":{"rows":[`+strings.Join(rows, ",")+`]`+next+p.extra+`},"meta":`+meta+`}`)
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
	parts := strings.SplitAfter(full.out, "\n")
	meta, item := len(parts[0]), len(parts[1])
	cap := meta + 3*item + jsonlEndReserve // the meta line, one page of three rows and the room of the complete line
	r := o.run(streamArgs("--limit", "3", "--max-bytes", strconv.Itoa(cap))...)
	ls := lines(t, r.out)
	end := ls[len(ls)-1]
	if r.code != 0 || strings.Join(ids(ls), ",") != "r0,r1,r2" || end["coverage"] != "partial" || end["nextCursor"] != "c3" || end["rows"] != float64(3) {
		t.Fatalf("%+v", r)
	}
	if len(r.out) > cap {
		t.Errorf("the output (%d bytes) is over the cap (%d)", len(r.out), cap)
	}
	// one byte less and the first page does not fit
	if r := o.run(streamArgs("--limit", "3", "--max-bytes", strconv.Itoa(cap-1))...); r.code == 0 && len(ids(lines(t, r.out))) == 3 {
		t.Errorf("a page that does not fit was printed: %s", r.out)
	}
	// a first page that does not fit is an error, with nothing on stdout
	r = o.run(streamArgs("--limit", "3", "--max-bytes", "10")...)
	if r.code != 5 || errCode2(t, r) != "RESULT_TOO_LARGE" || r.out != "" {
		t.Errorf("first page: %+v", r)
	}
	// the whole output, the complete line with a long cursor included, stays inside the cap whatever the cursor
	p = newPager(50)
	o = p.owner(t)
	for c := 1500; c < 1800; c += 7 {
		r = o.run(streamArgs("--limit", "1", "--max-bytes", strconv.Itoa(c))...)
		if len(r.out) > c {
			t.Fatalf("cap %d, output %d", c, len(r.out))
		}
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
		Getenv: func(string) string { return "" }, Exe: func() (string, error) { return testExe, nil }, Attach: o.attacher().Attach})
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

// The owner marks every slowest page that has a cursor as cut (truncated): that is paging, not a cut list. Only a source that failed, or
// a last page that is partial, makes the stream partial.
func TestJSONLCoverageIgnoresPagingCuts(t *testing.T) {
	cut := `{"coverage":"partial","truncated":"page"}`
	p := newPager(7)
	p.metaFn = func(i int, hasNext bool) string {
		if hasNext {
			return cut
		}
		return `{"coverage":"complete"}`
	}
	ls := lines(t, p.owner(t).run(streamArgs("--limit", "3")...).out)
	if e := ls[len(ls)-1]; e["coverage"] != "complete" || e["nextCursor"] != nil || e["rows"] != float64(7) {
		t.Errorf("paging cuts only: %v", e)
	}
	// the last page is partial: the list itself was cut
	p = newPager(7)
	p.metaFn = func(i int, hasNext bool) string {
		if hasNext {
			return `{"coverage":"complete"}`
		}
		return cut
	}
	if e := lines(t, p.owner(t).run(streamArgs("--limit", "3")...).out); e[len(e)-1]["coverage"] != "partial" {
		t.Errorf("last page partial: %v", e[len(e)-1])
	}
	// a source that did not answer on the first page
	p = newPager(7)
	p.extra = `,"sources":[{"sourceId":"a","ok":false}]`
	if e := lines(t, p.owner(t).run(streamArgs("--limit", "3", "--max-rows", "3")...).out); e[len(e)-1]["coverage"] != "partial" {
		t.Errorf("source failed: %v", e[len(e)-1])
	}
	// the real thing: audit slowest paged to its end on the demo owner is complete
	o := newOwner(t, nil)
	id, _ := compareKey(t, o)
	sl := o.run("audit", "slowest", "--result-id", id, "-o", "jsonl", "--limit", "20", "--max-rows", "10000")
	ls = lines(t, sl.out)
	end := ls[len(ls)-1]
	if sl.code != 0 || end["nextCursor"] != nil || end["coverage"] != "complete" || len(ids(ls)) < 3 {
		t.Errorf("slowest to the end: %s", end)
	}
}

func TestJSONLRowCapHoldsAgainstTheOwner(t *testing.T) {
	// a limit of null is no limit: the stream asks for what it may print
	p := newPager(30)
	r := runOn(t, p.owner(t).attacher(), `{"resultId":"r","apiKey":"k","limit":null}`, nil, "audit", "requests", "-o", "jsonl", "--max-rows", "2", "--request", "-")
	if got := ids(lines(t, r.out)); r.code != 0 || len(got) != 2 {
		t.Errorf("null limit: %+v", r)
	}
	// an owner that sends more rows than were asked for is an error, not extra output
	o := newOwner(t, nil)
	o.serve(200, o.helloBody(nil), func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"ok":true,"data":{"rows":[{"id":"a"},{"id":"b"},{"id":"c"}]}}`)
	})
	if r := o.run(streamArgs("--limit", "2")...); r.code != 1 || errCode2(t, r) != "INTERNAL" || r.out != "" {
		t.Errorf("too many rows: %+v", r)
	}
	// a limit the stream does not understand goes to the owner as it is; what comes back must still fit the row cap
	r = o.run(streamArgs("--limit", "0", "--max-rows", "2")...)
	if r.code != 5 || errCode2(t, r) != "RESULT_TOO_LARGE" || r.out != "" {
		t.Errorf("limit 0: %+v", r)
	}
}

func TestJSONLRequestNamesInAnyCase(t *testing.T) {
	p := newPager(7)
	o := p.owner(t)
	for _, req := range []string{`{"resultId":"r","apiKey":"k","Limit":3}`, `{"resultId":"r","apiKey":"k","LIMIT":3,"Cursor":"c1"}`, `{"resultId":"r","apiKey":"k","cursor":"c1"}`} {
		p.calls = nil
		r := runOn(t, o.attacher(), req, nil, "audit", "requests", "-o", "jsonl", "--request", "-", "--max-rows", "10")
		if r.code != 0 {
			t.Errorf("%s: %+v", req, r)
			continue
		}
		for _, c := range p.calls {
			if len(c) < 3 { // resultId, apiKey, limit (and cursor after the first page)
				t.Errorf("%s: call %v", req, c)
			}
			for k := range c {
				if k != strings.ToLower(k) && k != "resultId" && k != "apiKey" {
					t.Errorf("%s: the name %q reached the owner", req, k)
				}
			}
		}
	}
	// the cursor of the request is the start of the stream
	p.calls = nil
	r := runOn(t, o.attacher(), `{"resultId":"r","apiKey":"k","cursor":"c5"}`, nil, "audit", "requests", "-o", "jsonl", "--request", "-")
	if got := strings.Join(ids(lines(t, r.out)), ","); got != "r5,r6" || p.calls[0]["cursor"] != "c5" {
		t.Errorf("cursor: %s %v", got, p.calls)
	}
	// two names that differ only in case are refused, not merged
	r = runOn(t, o.attacher(), `{"resultId":"r","apiKey":"k","limit":3,"LIMIT":4}`, nil, "audit", "requests", "-o", "jsonl", "--request", "-")
	if r.code != 2 {
		t.Errorf("case twins: %+v", r)
	}
}

func TestJSONLRejectsLimitAboveToolMaximumBeforeCallingOwner(t *testing.T) {
	for op, maximum := range map[string]int{"requests": 100, "slowest": 50} {
		p := newPager(1)
		o := p.owner(t)
		args := []string{"audit", op, "--result-id", "r", "-o", "jsonl", "--limit", strconv.Itoa(maximum + 1), "--max-rows", "1"}
		if op == "requests" {
			args = append(args, "--api-key", "k")
		}
		r := o.run(args...)
		if r.code != 2 || errCode2(t, r) != "INVALID_ARGUMENT" || r.out != "" || len(p.calls) != 0 {
			t.Errorf("%s: %+v, owner calls %v", op, r, p.calls)
		}
	}
}

func TestJSONLOwnerWarningsGoOnceToStderr(t *testing.T) {
	p := newPager(4)
	p.metaFn = func(i int, _ bool) string {
		warnings := `["source slow","source slow"]`
		if i > 0 {
			warnings = `["source slow","source slow\ninjected"]`
		}
		return `{"coverage":"complete","warnings":` + warnings + `}`
	}
	r := p.owner(t).run(streamArgs("--limit", "2")...)
	if r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if want := "unravis: source slow\nunravis: source slow injected\n"; r.err != want {
		t.Errorf("stderr = %q, want %q", r.err, want)
	}
	for _, line := range lines(t, r.out) {
		if _, has := line["warnings"]; has {
			t.Errorf("a warning field entered the stream: %v", line)
		}
	}
	if end := lines(t, r.out)[5]; end["type"] != "complete" || end["coverage"] != "complete" {
		t.Errorf("complete line: %v", end)
	}
}

func TestJSONLDefaultPageSizeIsTheMostTheToolTakes(t *testing.T) {
	for op, want := range map[string][]string{"requests": {"100", "100"}, "slowest": {"50", "50"}} {
		p := newPager(2)
		o := p.owner(t)
		args := []string{"audit", op, "--result-id", "r", "-o", "jsonl"}
		if op == "requests" {
			args = append(args, "--api-key", "k")
		}
		if r := o.run(args...); r.code != 0 {
			t.Fatalf("%s: %+v", op, r)
		}
		if p.calls[0]["limit"] != mustNum(want[0]) {
			t.Errorf("%s asks for %v", op, p.calls[0]["limit"])
		}
	}
}

func mustNum(s string) float64 { n, _ := strconv.Atoi(s); return float64(n) }

func TestJSONLOddOwnerPages(t *testing.T) {
	// cursors that go round, a row that is not an object, a cursor that is long or has a quote in it
	n := 0
	o := newOwner(t, nil)
	cyc := []string{"A", "B", "A", "B"}
	o.serve(200, o.helloBody(nil), func(w http.ResponseWriter, _ *http.Request) {
		n++
		_, _ = io.WriteString(w, `{"ok":true,"data":{"rows":[{"id":"x"}],"nextCursor":"`+cyc[(n-1)%len(cyc)]+`"}}`)
	})
	if r := o.run(streamArgs("--limit", "1", "--max-rows", "100")...); r.code != 1 || errCode2(t, r) != "INTERNAL" || n > 4 {
		t.Errorf("cycle: %+v after %d calls", r, n)
	}
	for name, body := range map[string]string{
		"row is a number": `{"ok":true,"data":{"rows":[5]}}`,
		"row is text":     `{"ok":true,"data":{"rows":["a"]}}`,
		"row is null":     `{"ok":true,"data":{"rows":[null]}}`,
		"long cursor":     `{"ok":true,"data":{"rows":[{"id":"a"}],"nextCursor":"` + strings.Repeat("a", 1025) + `"}}`,
		"cursor quote":    `{"ok":true,"data":{"rows":[{"id":"a"}],"nextCursor":"a\"b"}}`,
		"cursor escape":   `{"ok":true,"data":{"rows":[{"id":"a"}],"nextCursor":"a\u001bb"}}`,
		"cursor space":    `{"ok":true,"data":{"rows":[{"id":"a"}],"nextCursor":"a b"}}`,
	} {
		o = newOwner(t, nil)
		o.serve(200, o.helloBody(nil), func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) })
		if r := o.run(streamArgs()...); r.code != 1 || errCode2(t, r) != "INTERNAL" || r.out != "" {
			t.Errorf("%s: %+v", name, r)
		}
	}
	// a long but plain cursor is passed on
	o = newOwner(t, nil)
	long := strings.Repeat("a", 1024)
	o.serve(200, o.helloBody(nil), func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"ok":true,"data":{"rows":[{"id":"a"}],"nextCursor":"`+long+`"}}`)
	})
	ls := lines(t, o.run(streamArgs("--max-rows", "1")...).out)
	if ls[len(ls)-1]["nextCursor"] != long {
		t.Errorf("the cursor was changed")
	}
}

func TestJSONLLimitsAtTheEdgeAndTimesAndWrites(t *testing.T) {
	o := newPager(3).owner(t)
	if r := o.run(streamArgs("--max-rows", "10000")...); r.code != 0 {
		t.Errorf("10000 is allowed: %+v", r)
	}
	if r := o.run(streamArgs("--max-rows", "10001")...); r.code != 2 {
		t.Errorf("10001 is not: %+v", r)
	}
	// each page gets the time that is left, never more than the command has
	p := newPager(9)
	if r := p.owner(t).run(streamArgs("--limit", "3", "--timeout", "20s")...); r.code != 0 {
		t.Fatalf("%+v", r)
	}
	if len(p.deadlines) != 3 || p.deadlines[0] > 20000 || p.deadlines[2] > p.deadlines[0] || p.deadlines[0] <= 0 {
		t.Errorf("deadlines: %v", p.deadlines)
	}
	// a failed write of the complete line stops the command too
	p = newPager(3)
	var errb bytes.Buffer
	w := &failAt{at: 2}
	code := Run(context.Background(), streamArgs("--limit", "3"), IO{In: strings.NewReader(""), Out: w, Err: &errb,
		Getenv: func(string) string { return "" }, Exe: func() (string, error) { return testExe, nil }, Attach: p.owner(t).attacher().Attach})
	if code != clicore.ExitStopped || errb.Len() != 0 || w.n != 2 {
		t.Errorf("code %d, stderr %q, writes %d", code, errb.String(), w.n)
	}
}

type failAt struct{ n, at int }

func (w *failAt) Write(p []byte) (int, error) {
	w.n++
	if w.n >= w.at {
		return 0, syscall.EPIPE
	}
	return len(p), nil
}

type blockAt struct {
	at, n         int
	started       chan struct{}
	release, done chan struct{}
	once          sync.Once
	buf           bytes.Buffer
}

func newBlockAt(at int) *blockAt {
	return &blockAt{at: at, started: make(chan struct{}), release: make(chan struct{}), done: make(chan struct{})}
}

func (w *blockAt) Write(p []byte) (int, error) {
	w.n++
	if w.n < w.at {
		return w.buf.Write(p)
	}
	w.once.Do(func() { close(w.started) })
	defer close(w.done)
	<-w.release
	return len(p), nil
}

func runBlockedWrite(t *testing.T, p *pager, at int, args ...string) result {
	t.Helper()
	o := p.owner(t)
	w := newBlockAt(at)
	var errb bytes.Buffer
	finished := make(chan int, 1)
	go func() {
		finished <- Run(context.Background(), args, IO{In: strings.NewReader(""), Out: w, Err: &errb,
			Getenv: func(string) string { return "" }, Exe: func() (string, error) { return testExe, nil }, Attach: o.attacher().Attach})
	}()
	select {
	case <-w.started:
	case code := <-finished:
		close(w.release)
		t.Fatalf("the command ended before its blocked write: code %d, stderr %q", code, errb.String())
	}
	var code int
	select {
	case code = <-finished:
	case <-time.After(2 * time.Second):
		close(w.release)
		<-w.done
		t.Fatal("a blocked write outlived --timeout")
	}
	close(w.release)
	<-w.done
	if code != clicore.ExitStopped || errCode2(t, result{err: errb.String()}) != "TIMEOUT" {
		t.Errorf("code %d, stderr %q", code, errb.String())
	}
	return result{code: code, out: w.buf.String(), err: errb.String()}
}

func TestJSONAndJSONLBlockedWritesHonorTimeout(t *testing.T) {
	// The timeout covers the attach and the query before the first write, so it must be long enough for a slow or busy PC to reach the
	// blocked write; it is still far below the 2 s the helper waits for the command to end.
	for _, tc := range []struct {
		name string
		p    *pager
		at   int
		args []string
	}{
		{"jsonl first page", newPager(1), 1, streamArgs("--timeout", "400ms")},
		{"jsonl complete", newPager(0), 2, streamArgs("--timeout", "400ms")},
		{"json", newPager(1), 1, []string{"audit", "requests", "--result-id", "r", "--api-key", "k", "--json", "--timeout", "400ms"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := runBlockedWrite(t, tc.p, tc.at, tc.args...)
			if !strings.Contains(r.err, `"code":"TIMEOUT"`) {
				t.Errorf("stderr: %q", r.err)
			}
		})
	}
}
