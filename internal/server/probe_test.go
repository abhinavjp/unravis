package server

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"
)

func probeBody(host string, port int) string {
	b, _ := json.Marshal(map[string]any{"host": host, "port": port})
	return string(b)
}

func probeCall(t *testing.T, h http.Handler, host string, port int) (int, probeResponse, string) {
	t.Helper()
	res := request(h, http.MethodPost, "/api/probe/reachable", probeBody(host, port), true)
	var out probeResponse
	_ = json.Unmarshal(res.Body.Bytes(), &out)
	return res.Code, out, res.Body.String()
}

func newProbeHandler() http.Handler { return New(&fakeRepository{}, testAssets(), testToken, false) }

func TestProbeReachableLocalListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan []byte, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_ = c.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
		buf := make([]byte, 16)
		n, _ := c.Read(buf)
		accepted <- buf[:n]
	}()
	port := ln.Addr().(*net.TCPAddr).Port

	code, out, _ := probeCall(t, newProbeHandler(), "127.0.0.1", port)
	if code != http.StatusOK || !out.Reachable || out.Reason != "" {
		t.Fatalf("got %d %+v, want reachable", code, out)
	}
	// The probe sends nothing: the listener sees a connect and a close, no bytes.
	select {
	case got := <-accepted:
		if len(got) != 0 {
			t.Errorf("probe sent %d bytes", len(got))
		}
	case <-time.After(2 * time.Second):
		t.Error("listener never saw the connection")
	}
}

func TestProbeReachableByName(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	old := probeLookup
	probeLookup = func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	}
	defer func() { probeLookup = old }()
	code, out, _ := probeCall(t, newProbeHandler(), "sql-box.corp.local", ln.Addr().(*net.TCPAddr).Port)
	if code != http.StatusOK || !out.Reachable {
		t.Fatalf("got %d %+v", code, out)
	}
}

func TestProbeClosedPortIsRefused(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close() // nothing listens here any more
	code, out, _ := probeCall(t, newProbeHandler(), "127.0.0.1", port)
	if code != http.StatusOK || out.Reachable || out.Reason != "refused" {
		t.Fatalf("got %d %+v, want refused", code, out)
	}
}

func TestProbeTimeoutIsBoundedAndReportedAsTimedOut(t *testing.T) {
	oldDial, oldTimeout := probeDial, probeTimeout
	probeTimeout = 150 * time.Millisecond
	probeDial = func(ctx context.Context, _ string) (net.Conn, error) {
		<-ctx.Done() // a filtered address: nothing ever answers
		return nil, ctx.Err()
	}
	defer func() { probeDial, probeTimeout = oldDial, oldTimeout }()
	start := time.Now()
	code, out, _ := probeCall(t, newProbeHandler(), "192.0.2.1", 1433)
	if code != http.StatusOK || out.Reachable || out.Reason != "timed-out" {
		t.Fatalf("got %d %+v, want timed-out", code, out)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("probe took %s, want about the test timeout", elapsed)
	}
}

func TestProbeRealNonRoutableAddressNeverHangs(t *testing.T) {
	old := probeTimeout
	probeTimeout = 200 * time.Millisecond
	defer func() { probeTimeout = old }()
	start := time.Now()
	code, out, _ := probeCall(t, newProbeHandler(), "192.0.2.1", 1433) // TEST-NET-1, reserved
	if code != http.StatusOK || out.Reachable {
		t.Fatalf("got %d %+v", code, out)
	}
	// A machine with no route says "unreachable" at once (refused); one with a
	// route times out. Either way the reason is from the fixed set and it is fast.
	if out.Reason != "timed-out" && out.Reason != "refused" {
		t.Errorf("reason = %q", out.Reason)
	}
	if time.Since(start) > 2*time.Second {
		t.Error("probe was not bounded")
	}
}

func TestProbeUnknownNameIsNoSuchHost(t *testing.T) {
	old := probeLookup
	probeLookup = func(context.Context, string) ([]netip.Addr, error) {
		return nil, &net.DNSError{Err: "no such host", Name: "secret-host.example", IsNotFound: true}
	}
	defer func() { probeLookup = old }()
	code, out, body := probeCall(t, newProbeHandler(), "secret-host.example", 1433)
	if code != http.StatusOK || out.Reachable || out.Reason != "no-such-host" {
		t.Fatalf("got %d %+v", code, out)
	}
	if strings.Contains(body, "secret-host") || strings.Contains(strings.ToLower(body), "lookup") {
		t.Errorf("response echoes host or raw error: %s", body)
	}
}

func TestProbeRefusesMetadataLinkLocalAndUnspecified(t *testing.T) {
	dialed := false
	oldDial := probeDial
	probeDial = func(context.Context, string) (net.Conn, error) { dialed = true; return nil, net.ErrClosed }
	defer func() { probeDial = oldDial }()
	h := newProbeHandler()
	for _, host := range []string{
		"169.254.169.254", "169.254.1.1", "[fe80::1]", "fd00:ec2::254", "::ffff:169.254.169.254",
		"0.0.0.0", "::", "0.1.2.3", "100.100.100.200", "224.0.0.1", "255.255.255.255",
	} {
		code, out, _ := probeCall(t, h, host, 80)
		if code != http.StatusOK || out.Reachable || out.Reason != "blocked" {
			t.Errorf("%s: got %d %+v, want blocked", host, code, out)
		}
	}
	if dialed {
		t.Error("a blocked address was dialled")
	}
}

func TestProbeRefusesNameThatResolvesToMetadata(t *testing.T) {
	dialed := false
	oldDial, oldLookup := probeDial, probeLookup
	probeDial = func(context.Context, string) (net.Conn, error) { dialed = true; return nil, net.ErrClosed }
	probeLookup = func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("10.0.0.5"), netip.MustParseAddr("169.254.169.254")}, nil
	}
	defer func() { probeDial, probeLookup = oldDial, oldLookup }()
	code, out, _ := probeCall(t, newProbeHandler(), "rebind.example.com", 80)
	if code != http.StatusOK || out.Reason != "blocked" || out.Reachable || dialed {
		t.Fatalf("got %d %+v dialed=%v, want blocked and no dial", code, out, dialed)
	}
}

func TestProbeAllowsLoopbackAndPrivateRanges(t *testing.T) {
	for _, s := range []string{"127.0.0.1", "::1", "10.1.2.3", "172.16.0.9", "192.168.1.20", "100.64.0.1", "fd12::1"} {
		if probeBlockedAddr(netip.MustParseAddr(s)) {
			t.Errorf("%s should be allowed", s)
		}
	}
}

func TestProbeRejectsGarbageHostAndPort(t *testing.T) {
	h := newProbeHandler()
	for _, host := range []string{
		"", "   ", "http://example.com", "example.com/path", "user@example.com", "example.com:1433",
		"exa mple.com", "example.com?x=1", "ex#ample", "-bad.com", "bad-.com", "a..b", "2130706433",
		"10.1", "fe80::1%eth0", strings.Repeat("a", 254), strings.Repeat("a", 64) + ".com", "ex\x00ample", "exämple.com",
	} {
		if code, _, body := probeCall(t, h, host, 1433); code != http.StatusBadRequest {
			t.Errorf("host %q = %d %s, want 400", host, code, body)
		}
	}
	for _, port := range []int{0, -1, 65536, 100000} {
		if code, _, _ := probeCall(t, h, "127.0.0.1", port); code != http.StatusBadRequest {
			t.Errorf("port %d = %d, want 400", port, code)
		}
	}
	for _, body := range []string{`{"host":"127.0.0.1","port":"1433"}`, `{"host":"127.0.0.1","port":1.5}`, `{"host":"127.0.0.1"}`, `{"host":"a","port":80,"extra":1}`, `not json`} {
		if res := request(h, http.MethodPost, "/api/probe/reachable", body, true); res.Code != http.StatusBadRequest {
			t.Errorf("body %s = %d, want 400", body, res.Code)
		}
	}
}

func TestProbeErrorsNeverEchoTheHost(t *testing.T) {
	h := newProbeHandler()
	_, _, body := probeCall(t, h, "top-secret-host/with/path", 1433)
	if strings.Contains(body, "top-secret") {
		t.Errorf("400 echoes the host: %s", body)
	}
}

func TestProbeRequiresTokenAndJSON(t *testing.T) {
	h := newProbeHandler()
	if res := request(h, http.MethodPost, "/api/probe/reachable", probeBody("127.0.0.1", 80), false); res.Code != http.StatusUnauthorized {
		t.Errorf("no token = %d, want 401", res.Code)
	}
	if res := request(h, http.MethodGet, "/api/probe/reachable", "", true); res.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET = %d, want 405", res.Code)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/probe/reachable", strings.NewReader(probeBody("127.0.0.1", 80)))
	req.Host = "127.0.0.1:8765"
	req.Header.Set("X-Audit-Token", testToken)
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusUnsupportedMediaType {
		t.Errorf("no content type = %d, want 415", res.Code)
	}
}

func TestProbeRejectsForeignOrigin(t *testing.T) {
	h := newProbeHandler()
	req := httptest.NewRequest(http.MethodPost, "/api/probe/reachable", strings.NewReader(probeBody("127.0.0.1", 80)))
	req.Host = "127.0.0.1:8765"
	req.Header.Set("X-Audit-Token", testToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example")
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Errorf("foreign origin = %d, want 403", res.Code)
	}
}

func TestProbeBodyIsSizeLimited(t *testing.T) {
	h := newProbeHandler()
	big := `{"host":"` + strings.Repeat("a", 2<<20) + `","port":80}`
	if res := request(h, http.MethodPost, "/api/probe/reachable", big, true); res.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("big body = %d, want 413", res.Code)
	}
}

func TestProbeIsRateLimited(t *testing.T) {
	oldDial := probeDial
	probeDial = func(context.Context, string) (net.Conn, error) { return nil, net.ErrClosed }
	defer func() { probeDial = oldDial }()
	h := newProbeHandler()
	for i := 0; i < probesPerWindow; i++ {
		if code, _, _ := probeCall(t, h, "127.0.0.1", 9); code == http.StatusTooManyRequests {
			t.Fatalf("limited early at call %d", i)
		}
	}
	code, _, body := probeCall(t, h, "127.0.0.1", 9)
	if code != http.StatusTooManyRequests {
		t.Fatalf("call past the limit = %d, want 429", code)
	}
	if strings.Contains(body, "127.0.0.1") {
		t.Errorf("429 echoes host: %s", body)
	}
	// Another handler (another server) has its own budget.
	if code, _, _ := probeCall(t, newProbeHandler(), "127.0.0.1", 9); code == http.StatusTooManyRequests {
		t.Error("limit leaked across handlers")
	}
}

func TestProbeRealDialOutcomeNeverCarriesRawError(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	_, _, body := probeCall(t, newProbeHandler(), "127.0.0.1", port)
	if strings.Contains(body, "dial") || strings.Contains(body, "connectex") || strings.Contains(body, strconv.Itoa(port)) {
		t.Errorf("raw Go error leaked: %s", body)
	}
}
