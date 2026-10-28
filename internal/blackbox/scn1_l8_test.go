package blackbox

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Scenario 1 defect tests for lane L8 (Blackbox recorder). Helpers (packCfg, unzipAll, memProgram, newSrvRig, newRig) are in pack_test.go, server_test.go and recorder_test.go.

// D-B-43: the README step is the same command the page shows: install, with no --name (the name is in blackbox.json).
func TestScn1L8PackReadmeInstallCommandHasNoName(t *testing.T) {
	var buf bytes.Buffer
	if _, err := BuildPack(&buf, packCfg(), memProgram{[]byte("MZ-fake-program")}); err != nil {
		t.Fatal(err)
	}
	readme := string(unzipAll(t, buf.Bytes())[PackReadmeName])
	if readme == "" {
		t.Fatal("no README.txt in the pack")
	}
	if !strings.Contains(readme, "3. Run:   .\\unravis-blackbox.exe install\r\n") {
		t.Fatalf("step 3 is not the plain install command:\n%s", readme)
	}
	if strings.Contains(readme, "--name") {
		t.Fatalf("the README still tells the user to pass --name:\n%s", readme)
	}
	// Step 1 still names the server, so the person knows which server the pack is for.
	if !strings.Contains(readme, "1. Copy this folder to the server "+packCfg().Name+" and unzip it there.") {
		t.Fatalf("step 1 changed:\n%s", readme)
	}
}

func scn1L8Do(t *testing.T, g *srvRig, method, path, key string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, g.ts.URL+path, strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if key != "" {
		req.Header.Set(KeyHeader, key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, body
}

// D-B-44: "GET and HEAD only" is verified for HEAD too, on every route.
func TestScn1L8HeadAnswersWithNoBodyAndStillNeedsTheKey(t *testing.T) {
	g := newSrvRig(t)
	name := g.s.CurrentFile()
	for _, p := range []string{"/status", "/manifest", "/files", "/files/" + name} {
		resp, body := scn1L8Do(t, g, http.MethodHead, p, g.key)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("HEAD %s = %d", p, resp.StatusCode)
		}
		if len(body) != 0 {
			t.Fatalf("HEAD %s sent a body of %d bytes", p, len(body))
		}
		if resp.Header.Get(TimeHeader) == "" {
			t.Fatalf("HEAD %s lacks the clock header", p)
		}
	}
	resp, _ := scn1L8Do(t, g, http.MethodHead, "/status", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("HEAD with no key = %d, want 401", resp.StatusCode)
	}
}

// D-B-44: PUT, DELETE, POST and PATCH on every route are 405 with Allow: GET, HEAD, and nothing on disk changes.
func TestScn1L8WriteMethodsAreRefusedOnEveryRoute(t *testing.T) {
	g := newSrvRig(t)
	name := g.s.CurrentFile()
	file := filepath.Join(g.s.Dir(), name)
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{http.MethodPut, http.MethodDelete, http.MethodPost, http.MethodPatch} {
		for _, p := range []string{"/status", "/manifest", "/files", "/files/" + name} {
			resp, _ := scn1L8Do(t, g, m, p, g.key)
			if resp.StatusCode != http.StatusMethodNotAllowed {
				t.Fatalf("%s %s = %d, want 405", m, p, resp.StatusCode)
			}
			if got := resp.Header.Get("Allow"); got != "GET, HEAD" {
				t.Fatalf("%s %s Allow = %q, want %q", m, p, got, "GET, HEAD")
			}
		}
	}
	after, err := os.ReadFile(file)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("the recording file changed through the server (err %v)", err)
	}
	if resp, _ := scn1L8Do(t, g, http.MethodGet, "/status", g.key); resp.StatusCode != http.StatusOK {
		t.Fatalf("the server stopped answering after the refused writes: %d", resp.StatusCode)
	}
}

// D-B-44: nine real readers at once. Eight get in; the ninth is told to come back (503, Retry-After 5). No hand-filled semaphore.
func TestScn1L8NineRealReadersEightFitOneIsTurnedAway(t *testing.T) {
	g := newSrvRig(t)
	g.r.mu.Lock() // /status blocks inside Recorder.Status until this is released
	var once sync.Once
	release := func() { once.Do(g.r.mu.Unlock) }
	defer release()

	codes := make(chan int, maxConcurrent)
	var wg sync.WaitGroup
	for i := 0; i < maxConcurrent; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, _ := http.NewRequest(http.MethodGet, g.ts.URL+"/status", nil)
			req.Header.Set(KeyHeader, g.key)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				codes <- -1
				return
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			codes <- resp.StatusCode
		}()
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(g.srv.sem) < maxConcurrent {
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d readers got in", len(g.srv.sem), maxConcurrent)
		}
		time.Sleep(5 * time.Millisecond)
	}

	resp, _ := scn1L8Do(t, g, http.MethodGet, "/status", g.key)
	if resp.StatusCode != http.StatusServiceUnavailable || resp.Header.Get("Retry-After") != "5" {
		t.Fatalf("ninth reader = %d, Retry-After %q; want 503 and 5", resp.StatusCode, resp.Header.Get("Retry-After"))
	}

	release()
	wg.Wait()
	close(codes)
	for c := range codes {
		if c != http.StatusOK {
			t.Fatalf("one of the first eight readers got %d", c)
		}
	}
	if n := len(g.srv.sem); n != 0 {
		t.Fatalf("%d slots still held after every reader finished", n)
	}
	if resp, _ := scn1L8Do(t, g, http.MethodGet, "/status", g.key); resp.StatusCode != http.StatusOK {
		t.Fatalf("a reader after the rush = %d, want 200", resp.StatusCode)
	}
}
