package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// cancelAfterReader hands out data in small reads and cancels the job after
// cancelAt of them, so a copy that ignores the cancel keeps reading.
type cancelAfterReader struct {
	reads    int
	cancelAt int
	cancel   context.CancelFunc
	total    int
}

func (c *cancelAfterReader) Read(p []byte) (int, error) {
	c.reads++
	if c.reads == c.cancelAt {
		c.cancel()
	}
	if c.reads > 1000 {
		return 0, io.EOF
	}
	n := min(len(p), 16)
	for i := range n {
		p[i] = 'x'
	}
	c.total += n
	return n, nil
}

// RV-SRV-4: a cancel during the spool copy stops it at the next read, and closing
// the spool removes the partial file.
func TestSpoolFillStopsAtOnceWhenTheJobIsCancelled(t *testing.T) {
	dir := t.TempDir()
	spool, err := newSpool(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	src := &cancelAfterReader{cancelAt: 5, cancel: cancel}
	_, err = spool.fill(ctx, src)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("fill error = %v, want context.Canceled", err)
	}
	if src.reads != src.cancelAt {
		t.Fatalf("reads = %d, want the copy to stop right after the cancel at read %d", src.reads, src.cancelAt)
	}
	spool.close()
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Fatalf("spool left %d file(s) after a cancel", len(left))
	}
}

// gatedBody serves head, then blocks the next read (telling blocked) until gate is
// closed, then serves tail: a slow upload that stalls in the middle of the file.
type gatedBody struct {
	head, tail []byte
	blocked    chan struct{}
	gate       chan struct{}
	once       sync.Once
}

func (g *gatedBody) Read(p []byte) (int, error) {
	if len(g.head) > 0 {
		n := copy(p, g.head)
		g.head = g.head[n:]
		return n, nil
	}
	g.once.Do(func() { close(g.blocked) })
	<-g.gate
	if len(g.tail) == 0 {
		return 0, io.EOF
	}
	n := copy(p, g.tail)
	g.tail = g.tail[n:]
	return n, nil
}

// stallingUpload posts path with a body that stalls inside the file part. While it
// stalls, stall runs; after it, the rest of the body arrives.
func stallingUpload(t *testing.T, e *caseEnv, path string, fields []formField, stall func()) *httptest.ResponseRecorder {
	t.Helper()
	body, contentType := uploadBody(t, fields, &upload{"a.csv", bigCSV()})
	raw := body.Bytes()
	cut := bytes.Index(raw, []byte("2001-01-01")) + 12
	gb := &gatedBody{head: raw[:cut], tail: raw[cut:], blocked: make(chan struct{}), gate: make(chan struct{})}
	req := httptest.NewRequest(http.MethodPost, path, gb)
	req.Header.Set("X-Audit-Token", testToken)
	req.Header.Set("Content-Type", contentType)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- e.serve(req) }()
	select {
	case <-gb.blocked:
	case <-time.After(10 * time.Second):
		close(gb.gate)
		t.Fatal("the upload never reached the stall")
	}
	stall()
	close(gb.gate)
	select {
	case res := <-done:
		return res
	case <-time.After(10 * time.Second):
		t.Fatal("the upload did not end after the cancel")
		return nil
	}
}

// A shutdown (it cancels every job) while an upload is spooling stops the spool
// at the next read: the answer is "canceled", no job is left, and no spool file.
func TestAsyncImportSpoolStopsWhenJobsAreCancelled(t *testing.T) {
	spool := t.TempDir()
	e := newCaseEnv(t, envConfig{options: []Option{withJobHooks(func(j *importJobs) { j.spoolDir = spool })}})
	id := e.newCase("Spool cancel")
	res := stallingUpload(t, e, asyncPath(id), ackFields, func() { e.handler().jobs.shutdown(0) })
	if res.Code != http.StatusBadRequest || failureOf(t, res).Reason != "canceled" {
		t.Fatalf("answer = %d %s, want 400 canceled", res.Code, res.Body)
	}
	if left, _ := os.ReadDir(spool); len(left) != 0 {
		t.Fatalf("spool left %d file(s)", len(left))
	}
	if n := len(e.importsOf(id)); n != 0 {
		t.Fatalf("imports = %d, want 0", n)
	}
}

// The same for the check route, which spools each file the same way.
func TestCheckSpoolStopsWhenJobsAreCancelled(t *testing.T) {
	spool := t.TempDir()
	e := newCaseEnv(t, envConfig{options: []Option{withJobHooks(func(j *importJobs) { j.spoolDir = spool })}})
	id := e.newCase("Check spool cancel")
	res := stallingUpload(t, e, checkPath(id), nil, func() { e.handler().jobs.shutdown(0) })
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "canceled") {
		t.Fatalf("answer = %d %s, want 400 canceled", res.Code, res.Body)
	}
	if left, _ := os.ReadDir(spool); len(left) != 0 {
		t.Fatalf("spool left %d file(s)", len(left))
	}
}

// bigCSV is several hundred KB, so the spool copy needs many reads.
func bigCSV() string {
	rows := make([]string, 0, 3000)
	for i := range 3000 {
		rows = append(rows, toyRow(i%60, "web-a", strings.Repeat("n", 150), 5))
	}
	return toyCSV(rows...)
}
