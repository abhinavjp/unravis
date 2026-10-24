package server

import (
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// slowUpload streams a multipart import over a pipe: the ack fields at once,
// then one row every step, so the request takes about rows*step to send.
func slowUpload(t *testing.T, name string, rows int, step time.Duration) (io.Reader, string) {
	t.Helper()
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		defer pw.Close()
		for _, f := range ackFields {
			if err := mw.WriteField(f.name, f.value); err != nil {
				return
			}
		}
		part, err := mw.CreateFormFile("file", name)
		if err != nil {
			return
		}
		if _, err := io.WriteString(part, toyHeader+"\n"); err != nil {
			return
		}
		for i := 0; i < rows; i++ {
			time.Sleep(step)
			if _, err := io.WriteString(part, toyRow(i%60, "web-a", name, 10+i)+"\n"); err != nil {
				return
			}
		}
		_ = mw.Close()
	}()
	return pr, mw.FormDataContentType()
}

func startTimedServer(t *testing.T, e *caseEnv, readTimeout time.Duration) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(e.h)
	srv.Config.ReadTimeout = readTimeout
	srv.Config.WriteTimeout = readTimeout * 4
	srv.Start()
	t.Cleanup(srv.Close)
	return srv
}

func postSlow(t *testing.T, base, caseID string, body io.Reader, contentType string) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+commitPath(caseID), body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Audit-Token", testToken)
	req.Header.Set("Content-Type", contentType)
	return http.DefaultClient.Do(req)
}

// A person importing a large file over a slow disk or link is not clumsy: the
// request may run longer than the server's read timeout and must still finish.
func TestImportLongerThanTheReadTimeoutStillSucceeds(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	srv := startTimedServer(t, e, 300*time.Millisecond)
	id := e.newCase("slow upload")

	body, ct := slowUpload(t, "slow.csv", 12, 100*time.Millisecond) // about 1.2 s
	res, err := postSlow(t, srv.URL, id, body, ct)
	if err != nil {
		t.Fatalf("slow import failed: %v", err)
	}
	defer res.Body.Close()
	text, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("slow import = %d %s, want 201", res.StatusCode, text)
	}
	if got := e.importsOf(id); len(got) != 1 || got[0].Rows != 12 {
		t.Fatalf("imports = %+v, want one import with 12 rows", got)
	}
}

// An import that waits for another one on the same case must not lose its body
// to the read timeout while it waits.
func TestImportQueuedBehindAnotherSucceedsAfterTheReadTimeout(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	srv := startTimedServer(t, e, 300*time.Millisecond)
	id := e.newCase("queued upload")

	var wg sync.WaitGroup
	results := make([]int, 2)
	bodies := make([]string, 2)
	run := func(i int, body io.Reader, ct string) {
		defer wg.Done()
		res, err := postSlow(t, srv.URL, id, body, ct)
		if err != nil {
			bodies[i] = err.Error()
			return
		}
		defer res.Body.Close()
		text, _ := io.ReadAll(res.Body)
		results[i], bodies[i] = res.StatusCode, string(text)
	}

	slow, slowCT := slowUpload(t, "first.csv", 10, 100*time.Millisecond) // about 1 s, holds the case
	wg.Add(1)
	go run(0, slow, slowCT)
	time.Sleep(150 * time.Millisecond) // let the first one take the gate

	quick, quickCT := uploadBody(t, ackFields, &upload{name: "second.csv", content: toyCSV(toyRow(1, "web-a", "second", 5))})
	wg.Add(1)
	go run(1, quick, quickCT)
	wg.Wait()

	for i, code := range results {
		if code != http.StatusCreated {
			t.Fatalf("import %d = %d %s, want 201", i, code, strings.TrimSpace(bodies[i]))
		}
	}
	if got := e.importsOf(id); len(got) != 2 {
		t.Fatalf("imports = %d, want 2", len(got))
	}
}

// A body that goes silent is still dropped: the rolling deadline is an idle
// limit, not a free pass. The recorder used by the other tests has no
// deadlines, so this guards the helper itself.
func TestRollingBodyIgnoresAWriterWithoutDeadlines(t *testing.T) {
	rec := httptest.NewRecorder()
	rb := newRollingBody(rec, io.NopCloser(strings.NewReader("abc")))
	buf := make([]byte, 8)
	n, err := rb.Read(buf)
	if err != nil || string(buf[:n]) != "abc" {
		t.Fatalf("read = %q, %v", buf[:n], err)
	}
	if err := rb.Close(); err != nil {
		t.Fatal(err)
	}
}
