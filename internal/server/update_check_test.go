package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/version"
)

// updateCheckRoundTripFunc lets a test fake GitHub without any network.
type updateCheckRoundTripFunc func(*http.Request) (*http.Response, error)

func (f updateCheckRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func updateCheckHTTPResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func setUpdateCheckVersion(t *testing.T, v string) {
	t.Helper()
	old := version.Version
	t.Cleanup(func() { version.Version = old })
	version.Version = v
}

func updateCheckHandler(rt http.RoundTripper) http.Handler {
	return New(audit.NewDemo(time.Now()), nil, testToken, true, WithUpdateCheckHTTP(&http.Client{Transport: rt}))
}

func postUpdateCheck(t *testing.T, h http.Handler) updateCheckReply {
	t.Helper()
	res := request(h, http.MethodPost, "/api/update-check", "", true)
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", res.Code, res.Body.String())
	}
	var reply updateCheckReply
	if err := json.Unmarshal(res.Body.Bytes(), &reply); err != nil {
		t.Fatalf("body %q: %v", res.Body.String(), err)
	}
	return reply
}

func TestUpdateCheckLatest(t *testing.T) {
	setUpdateCheckVersion(t, "1.2.3")
	var calls atomic.Int64
	h := updateCheckHandler(updateCheckRoundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return updateCheckHTTPResponse(http.StatusOK, `{"tag_name":"v1.2.3","html_url":"https://github.com/abhinavjp/unravis/releases/tag/v1.2.3"}`), nil
	}))
	reply := postUpdateCheck(t, h)
	if reply.Status != "latest" || reply.Current != "1.2.3" || reply.Latest != "" || reply.URL != "" {
		t.Errorf("reply = %+v", reply)
	}
	if calls.Load() != 1 {
		t.Errorf("calls = %d", calls.Load())
	}
}

func TestUpdateCheckAvailable(t *testing.T) {
	setUpdateCheckVersion(t, "1.2.3")
	h := updateCheckHandler(updateCheckRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return updateCheckHTTPResponse(http.StatusOK, `{"tag_name":"v1.4.0","html_url":"https://github.com/abhinavjp/unravis/releases/tag/v1.4.0"}`), nil
	}))
	reply := postUpdateCheck(t, h)
	want := updateCheckReply{Status: "available", Current: "1.2.3", Latest: "1.4.0", URL: "https://github.com/abhinavjp/unravis/releases/tag/v1.4.0"}
	if reply != want {
		t.Errorf("reply = %+v, want %+v", reply, want)
	}
}

func TestUpdateCheckOlderRelease(t *testing.T) {
	setUpdateCheckVersion(t, "1.2.3")
	h := updateCheckHandler(updateCheckRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return updateCheckHTTPResponse(http.StatusOK, `{"tag_name":"v1.1.9","html_url":"https://github.com/abhinavjp/unravis/releases/tag/v1.1.9"}`), nil
	}))
	if reply := postUpdateCheck(t, h); reply.Status != "latest" || reply.Latest != "" || reply.URL != "" {
		t.Errorf("reply = %+v", reply)
	}
}

func TestUpdateCheckDraftAndPrereleaseIgnored(t *testing.T) {
	setUpdateCheckVersion(t, "1.2.3")
	for _, body := range []string{
		`{"tag_name":"v2.0.0","draft":true,"html_url":"https://github.com/abhinavjp/unravis/releases/tag/v2.0.0"}`,
		`{"tag_name":"v2.0.0","prerelease":true,"html_url":"https://github.com/abhinavjp/unravis/releases/tag/v2.0.0"}`,
	} {
		h := updateCheckHandler(updateCheckRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return updateCheckHTTPResponse(http.StatusOK, body), nil
		}))
		if reply := postUpdateCheck(t, h); reply.Status != "latest" || reply.Latest != "" || reply.URL != "" {
			t.Errorf("body %s: reply = %+v", body, reply)
		}
	}
}

func TestUpdateCheckDevBuildMakesNoCall(t *testing.T) {
	for _, v := range []string{"dev", "", "snapshot", "vdev"} {
		setUpdateCheckVersion(t, v)
		var calls atomic.Int64
		h := updateCheckHandler(updateCheckRoundTripFunc(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return updateCheckHTTPResponse(http.StatusOK, `{"tag_name":"v9.9.9"}`), nil
		}))
		reply := postUpdateCheck(t, h)
		if reply.Status != "dev" || reply.Current != v || reply.Latest != "" || reply.URL != "" {
			t.Errorf("version %q: reply = %+v", v, reply)
		}
		if calls.Load() != 0 {
			t.Errorf("version %q: the fake was hit %d times", v, calls.Load())
		}
	}
}

func TestUpdateCheckOffline(t *testing.T) {
	setUpdateCheckVersion(t, "1.2.3")
	h := updateCheckHandler(updateCheckRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("dial tcp: no such host")
	}))
	reply := postUpdateCheck(t, h)
	if reply.Status != "unreachable" || reply.Current != "1.2.3" || reply.Latest != "" || reply.URL != "" {
		t.Errorf("reply = %+v", reply)
	}
}

func TestUpdateCheckRateLimitedAndForbidden(t *testing.T) {
	setUpdateCheckVersion(t, "1.2.3")
	for _, status := range []int{http.StatusForbidden, http.StatusTooManyRequests, http.StatusBadGateway} {
		h := updateCheckHandler(updateCheckRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return updateCheckHTTPResponse(status, `{"message":"API rate limit exceeded"}`), nil
		}))
		if reply := postUpdateCheck(t, h); reply.Status != "unreachable" {
			t.Errorf("status %d: reply = %+v", status, reply)
		}
	}
}

func TestUpdateCheckBadJSON(t *testing.T) {
	setUpdateCheckVersion(t, "1.2.3")
	h := updateCheckHandler(updateCheckRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return updateCheckHTTPResponse(http.StatusOK, `this is not json`), nil
	}))
	if reply := postUpdateCheck(t, h); reply.Status != "unreachable" {
		t.Errorf("reply = %+v", reply)
	}
}

func TestUpdateCheckMissingTag(t *testing.T) {
	setUpdateCheckVersion(t, "1.2.3")
	h := updateCheckHandler(updateCheckRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return updateCheckHTTPResponse(http.StatusOK, `{"html_url":"https://github.com/abhinavjp/unravis/releases"}`), nil
	}))
	if reply := postUpdateCheck(t, h); reply.Status != "unreachable" {
		t.Errorf("reply = %+v", reply)
	}
}

func TestUpdateCheckForeignURLDropped(t *testing.T) {
	setUpdateCheckVersion(t, "1.2.3")
	for _, url := range []string{
		"https://evil.example.com/release",
		"https://github.com/abhinavjma/unravis/releases/tag/v2.0.0",
		"http://github.com/abhinavjp/unravis/releases/tag/v2.0.0",
	} {
		h := updateCheckHandler(updateCheckRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return updateCheckHTTPResponse(http.StatusOK, `{"tag_name":"v2.0.0","html_url":`+strconvQuote(url)+`}`), nil
		}))
		reply := postUpdateCheck(t, h)
		if reply.Status != "available" || reply.Latest != "2.0.0" || reply.URL != "" {
			t.Errorf("url %q: reply = %+v", url, reply)
		}
	}
}

func TestUpdateCheckDirtySuffix(t *testing.T) {
	setUpdateCheckVersion(t, "1.2.3-dirty")
	h := updateCheckHandler(updateCheckRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return updateCheckHTTPResponse(http.StatusOK, `{"tag_name":"v1.2.3","html_url":"https://github.com/abhinavjp/unravis/releases/tag/v1.2.3"}`), nil
	}))
	if reply := postUpdateCheck(t, h); reply.Status != "latest" {
		t.Errorf("same numbers with -dirty: reply = %+v", reply)
	}

	h2 := updateCheckHandler(updateCheckRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return updateCheckHTTPResponse(http.StatusOK, `{"tag_name":"v1.2.4","html_url":"https://github.com/abhinavjp/unravis/releases/tag/v1.2.4"}`), nil
	}))
	if reply := postUpdateCheck(t, h2); reply.Status != "available" || reply.Latest != "1.2.4" {
		t.Errorf("newer tag against -dirty build: reply = %+v", reply)
	}
}

func TestUpdateCheckRejectsGet(t *testing.T) {
	setUpdateCheckVersion(t, "1.2.3")
	var calls atomic.Int64
	h := updateCheckHandler(updateCheckRoundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return updateCheckHTTPResponse(http.StatusOK, `{}`), nil
	}))
	if res := request(h, http.MethodGet, "/api/update-check", "", true); res.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET status = %d", res.Code)
	}
	if calls.Load() != 0 {
		t.Errorf("the fake was hit %d times", calls.Load())
	}
}

func TestUpdateCheckNeedsTheSessionToken(t *testing.T) {
	setUpdateCheckVersion(t, "1.2.3")
	h := updateCheckHandler(updateCheckRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return updateCheckHTTPResponse(http.StatusOK, `{}`), nil
	}))
	if res := request(h, http.MethodPost, "/api/update-check", "", false); res.Code != http.StatusUnauthorized {
		t.Errorf("without a token status = %d", res.Code)
	}
}

func TestUpdateCheckOutgoingRequest(t *testing.T) {
	setUpdateCheckVersion(t, "1.2.3")
	var seen *http.Request
	h := updateCheckHandler(updateCheckRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		seen = r
		return updateCheckHTTPResponse(http.StatusOK, `{"tag_name":"v1.2.3"}`), nil
	}))
	postUpdateCheck(t, h)
	if seen == nil {
		t.Fatal("the fake was never called")
	}
	if seen.Method != http.MethodGet {
		t.Errorf("method = %s", seen.Method)
	}
	if seen.URL.String() != "https://api.github.com/repos/abhinavjp/unravis/releases/latest" {
		t.Errorf("url = %s", seen.URL.String())
	}
	if got := seen.Header.Get("Accept"); got != "application/vnd.github+json" {
		t.Errorf("Accept = %q", got)
	}
	if got := seen.Header.Get("User-Agent"); got != "Unravis/1.2.3" {
		t.Errorf("User-Agent = %q", got)
	}
	if got := seen.Header.Get("Authorization"); got != "" {
		t.Errorf("Authorization = %q", got)
	}
	if got := seen.Header.Get("Cookie"); got != "" {
		t.Errorf("Cookie = %q", got)
	}
}

func TestUpdateCheckHugeBody(t *testing.T) {
	setUpdateCheckVersion(t, "1.2.3")
	big := `{"tag_name":"v9.9.9","html_url":"https://github.com/abhinavjp/unravis/releases/tag/v9.9.9","notes":"` + strings.Repeat("x", 300*1024) + `"}`
	h := updateCheckHandler(updateCheckRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return updateCheckHTTPResponse(http.StatusOK, big), nil
	}))
	if reply := postUpdateCheck(t, h); reply.Status != "unreachable" {
		t.Errorf("reply = %+v", reply)
	}
}

func TestUpdateCheckCachesForFiveSeconds(t *testing.T) {
	setUpdateCheckVersion(t, "1.2.3")
	var calls atomic.Int64
	h := updateCheckHandler(updateCheckRoundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return updateCheckHTTPResponse(http.StatusOK, `{"tag_name":"v1.2.3"}`), nil
	}))
	postUpdateCheck(t, h)
	postUpdateCheck(t, h)
	if calls.Load() != 1 {
		t.Fatalf("two presses made %d calls, want 1", calls.Load())
	}
	// Age the cache past the five second TTL; the next press calls out again.
	state := updateCheckStateFor(h.(*handler))
	state.mu.Lock()
	state.at = state.now().Add(-time.Minute)
	state.mu.Unlock()
	postUpdateCheck(t, h)
	if calls.Load() != 2 {
		t.Errorf("after the cache expired calls = %d, want 2", calls.Load())
	}
}

func TestUpdateCheckSerialisesConcurrentChecks(t *testing.T) {
	setUpdateCheckVersion(t, "1.2.3")
	var calls atomic.Int64
	h := updateCheckHandler(updateCheckRoundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		time.Sleep(20 * time.Millisecond)
		return updateCheckHTTPResponse(http.StatusOK, `{"tag_name":"v1.2.3"}`), nil
	}))
	var wg sync.WaitGroup
	replies := make([]updateCheckReply, 4)
	for i := range replies {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := request(h, http.MethodPost, "/api/update-check", "", true)
			if res.Code != http.StatusOK {
				t.Errorf("status = %d", res.Code)
				return
			}
			_ = json.Unmarshal(res.Body.Bytes(), &replies[i])
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Errorf("concurrent presses made %d calls, want 1", calls.Load())
	}
	for i, reply := range replies {
		if reply.Status != "latest" {
			t.Errorf("replies[%d] = %+v", i, reply)
		}
	}
}

func strconvQuote(s string) string {
	data, _ := json.Marshal(s)
	return string(data)
}
