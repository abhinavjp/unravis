package blackbox

import (
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// SEC-09: a request with no key is not a guess, so it cannot lock out a reader.
func TestRequestsWithoutAKeyAreNotCountedAsWrongGuesses(t *testing.T) {
	g := newSrvRig(t)
	for i := 0; i < 3*maxFailures; i++ {
		if resp, _ := g.get(t, "/status", ""); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("no key, attempt %d: %d", i, resp.StatusCode)
		}
	}
	if resp, _ := g.get(t, "/status", g.key); resp.StatusCode != http.StatusOK {
		t.Fatalf("the reader was locked out by requests without a key: %d", resp.StatusCode)
	}
}

// SEC-09: a web page cannot read the recorder, even with the right key.
func TestBrowserRequestsAreRefused(t *testing.T) {
	g := newSrvRig(t)
	for name, h := range map[string]http.Header{
		"origin":     {"Origin": {"http://evil.example"}},
		"cross-site": {"Sec-Fetch-Site": {"cross-site"}},
		"same-site":  {"Sec-Fetch-Site": {"same-site"}},
	} {
		req, _ := http.NewRequest("GET", g.ts.URL+"/status", nil)
		req.Header.Set(KeyHeader, g.key)
		for k, v := range h {
			req.Header[k] = v
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s: %d", name, resp.StatusCode)
		}
	}
	// The reader program sends neither header, and "none" or "same-origin" are not cross-site.
	req, _ := http.NewRequest("GET", g.ts.URL+"/status", nil)
	req.Header.Set(KeyHeader, g.key)
	req.Header.Set("Sec-Fetch-Site", "none")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Sec-Fetch-Site none: %d", resp.StatusCode)
	}
}

// SEC-12: the server bounds how long and how big a request may be.
func TestServerBoundsRequestSizeAndTime(t *testing.T) {
	g := newSrvRig(t)
	s := g.srv.srv
	if s.ReadTimeout <= 0 || s.MaxHeaderBytes <= 0 || s.MaxHeaderBytes > 16<<10 {
		t.Fatalf("read timeout %v, max header bytes %d", s.ReadTimeout, s.MaxHeaderBytes)
	}
}

// SEC-12: connections past the cap are closed at once, and a closed one frees its slot.
func TestLimitListenerCapsOpenConnections(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln := newLimitListener(inner, 2, 2)
	defer ln.Close()
	accepted := make(chan net.Conn, 8)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepted <- c
		}
	}()
	dial := func() net.Conn {
		c, err := net.Dial("tcp", inner.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	var clients []net.Conn
	for i := 0; i < 2; i++ {
		clients = append(clients, dial())
	}
	var held []net.Conn
	for i := 0; i < 2; i++ {
		select {
		case c := <-accepted:
			held = append(held, c)
		case <-time.After(2 * time.Second):
			t.Fatal("a connection under the cap was not accepted")
		}
	}
	over := dial()
	defer over.Close()
	_ = over.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := over.Read(make([]byte, 1)); err == nil || strings.Contains(err.Error(), "timeout") {
		t.Fatalf("the connection over the cap was not closed: %v", err)
	}
	held[0].Close()
	held[0].Close() // closing twice frees one slot only
	again := dial()
	defer again.Close()
	select {
	case c := <-accepted:
		c.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("a freed slot was not reused")
	}
	for _, c := range clients {
		c.Close()
	}
	held[1].Close()
}

// SEC-10: the status port has no TLS, so listening beyond this PC is warned about.
func TestBindWarning(t *testing.T) {
	for _, b := range []string{"127.0.0.1", "::1", "localhost", "LOCALHOST", " 127.0.0.1 ", "127.1.2.3"} {
		if w := BindWarning(b); w != "" {
			t.Errorf("%q warned: %s", b, w)
		}
	}
	for _, b := range []string{"0.0.0.0", "::", "192.168.1.20", "web-b", "web-b.corp.example"} {
		w := BindWarning(b)
		if w == "" {
			t.Errorf("%q gave no warning", b)
			continue
		}
		if !strings.Contains(w, "Firewall") || !strings.Contains(w, "127.0.0.1") || strings.Contains(w, "\u2014") {
			t.Errorf("%q: warning lacks the next step or has a long dash: %s", b, w)
		}
	}
}

// SEC-07: a network share is not a log folder for a SYSTEM service.
func TestLogPathsRefuseNetworkShares(t *testing.T) {
	c := Default()
	c.Name = "web-b"
	c.LogPaths = []string{`\\files01\logs`}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "network share") {
		t.Fatalf("%v", err)
	}
	c.LogPaths = []string{`//files01/logs`}
	if err := c.Validate(); err == nil {
		t.Fatal("forward-slash share accepted")
	}
	c.LogPaths = []string{`D:\Logs`}
	if err := c.Validate(); err != nil {
		t.Fatalf("a local log folder was refused: %v", err)
	}
}

// fakeAddrListener hands out connections that claim to come from chosen addresses.
type fakeAddrListener struct {
	net.Listener
	from chan string
}

type addrConn struct {
	net.Conn
	remote net.Addr
}

func (c addrConn) RemoteAddr() net.Addr { return c.remote }

func (f *fakeAddrListener) Accept() (net.Conn, error) {
	a, ok := <-f.from
	if !ok {
		return nil, net.ErrClosed
	}
	c1, c2 := net.Pipe()
	_ = c2
	return addrConn{Conn: c1, remote: &net.TCPAddr{IP: net.ParseIP(a), Port: 50000}}, nil
}

// SEC-FIX-12: one address cannot fill every slot; another address still gets in.
func TestLimitListenerCapsConnectionsPerAddress(t *testing.T) {
	inner, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer inner.Close()
	fl := &fakeAddrListener{Listener: inner, from: make(chan string, 16)}
	ln := newLimitListener(fl, 4, 2)
	accepted := make(chan net.Conn, 16)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepted <- c
		}
	}()
	for _, a := range []string{"10.0.0.1", "10.0.0.1", "10.0.0.1", "10.0.0.2"} {
		fl.from <- a
	}
	var got []net.Conn
	for i := 0; i < 3; i++ {
		select {
		case c := <-accepted:
			got = append(got, c)
		case <-time.After(2 * time.Second):
			t.Fatalf("connection %d was not accepted", i+1)
		}
	}
	select {
	case <-accepted:
		t.Fatal("a third connection from one address was accepted")
	case <-time.After(200 * time.Millisecond):
	}
	// Closing one frees the address's slot again.
	got[0].Close()
	fl.from <- "10.0.0.1"
	select {
	case c := <-accepted:
		c.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("a freed slot was not usable")
	}
	close(fl.from)
	for _, c := range got {
		c.Close()
	}
}
