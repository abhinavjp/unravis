package update

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLiveFeed reads the real feed with the real keys. It is skipped unless UNRAVIS_LIVE_FEED=1 (CI never sets it).
// It starts nothing and writes only into a temp folder. Settings, all optional:
//
//	UNRAVIS_LIVE_CURRENT   the running version to pretend to be (default 0.1.0-rc.54)
//	UNRAVIS_LIVE_WANT      the channel (default rc)
//	UNRAVIS_LIVE_EXPECT    available | held | latest
//	UNRAVIS_LIVE_DETAIL    text the result detail must hold
//	UNRAVIS_LIVE_PRIMARY   "off": the main hosts fail, so only the backup address is read
//	UNRAVIS_LIVE_NEED_CONTROL=1  the saved state must show a verified control file was read
//	UNRAVIS_LIVE_STATE     a state file to start from (copied; the original is never written)
//	UNRAVIS_LIVE_SCENARIO  "kept-block": an expired control that withdrew the offered version, above any live seq
func TestLiveFeed(t *testing.T) {
	if os.Getenv("UNRAVIS_LIVE_FEED") != "1" {
		t.Skip("live feed test: set UNRAVIS_LIVE_FEED=1")
	}
	env := func(k, d string) string {
		if v := os.Getenv(k); v != "" {
			return v
		}
		return d
	}
	keys, err := TrustedKeys()
	if err != nil {
		t.Fatal(err)
	}
	cur, want := env("UNRAVIS_LIVE_CURRENT", "0.1.0-rc.54"), env("UNRAVIS_LIVE_WANT", ChannelRC)
	v, err := ParseVersion(cur)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	sp := filepath.Join(dir, "state.json")
	if src := os.Getenv("UNRAVIS_LIVE_STATE"); src != "" {
		b, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(sp, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	f := &Fetcher{Keys: keys, Build: ChannelOf(v), Want: want, Current: cur, StatePath: sp}
	if env("UNRAVIS_LIVE_PRIMARY", "") == "off" {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.Proxy = http.ProxyFromEnvironment
		f.HTTP = &http.Client{Transport: refuseMainHosts{tr}, Timeout: fetchTimeout}
	}
	if env("UNRAVIS_LIVE_SCENARIO", "") == "kept-block" {
		// Learn what is on offer, then pretend this machine once saw an expired control that withdrew exactly that version.
		first := f.Check(t.Context())
		if first.Manifest == nil {
			t.Fatalf("nothing on offer to withdraw: %+v", first)
		}
		st, _, lerr := f.loadState()
		if lerr != nil {
			t.Fatal(lerr)
		}
		st.Control, st.ControlHash = nil, nil
		st.ControlSeq = map[string]uint64{want: 1<<40 - 1}
		st.KeptBlocked = map[string][]string{want: {first.Manifest.Version}}
		stateMu.Lock()
		err := f.saveState(st)
		stateMu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		restart()
	}
	r := f.Check(t.Context())
	t.Logf("status=%s held=%v detail=%q", r.Status, r.Held, r.Detail)
	if r.Manifest != nil {
		t.Logf("version=%s seq=%d", r.Manifest.Version, r.Manifest.Seq)
	}
	st, _, _ := f.loadState()
	t.Logf("controlSeq=%d controlHash=%q kept=%v", st.ControlSeq[want], st.ControlHash[want], st.KeptBlocked[want])
	switch env("UNRAVIS_LIVE_EXPECT", "") {
	case "available":
		if r.Status != StatusAvailable {
			t.Errorf("want available, got %s (%s)", r.Status, r.Detail)
		}
	case "held":
		if !r.Held {
			t.Errorf("want held, got %s (%s)", r.Status, r.Detail)
		}
	case "latest":
		if r.Status != StatusLatest {
			t.Errorf("want latest, got %s (%s)", r.Status, r.Detail)
		}
	}
	if d := os.Getenv("UNRAVIS_LIVE_DETAIL"); d != "" && !strings.Contains(r.Detail, d) {
		t.Errorf("detail %q does not hold %q", r.Detail, d)
	}
	if os.Getenv("UNRAVIS_LIVE_NEED_CONTROL") == "1" && (st.ControlSeq[want] == 0 || st.ControlHash[want] == "") {
		t.Error("no verified control file was read")
	}
	if os.Getenv("UNRAVIS_LIVE_STATE") != "" {
		if m, _ := filepath.Glob(sp + ".corrupt-*"); len(m) > 0 {
			t.Error("the supplied state file was set aside")
		}
	}
}

// refuseMainHosts fails every request to the main release hosts, so only the backup address can answer.
type refuseMainHosts struct{ next http.RoundTripper }

func (r refuseMainHosts) RoundTrip(req *http.Request) (*http.Response, error) {
	if DefaultAllowHost(req.URL.Hostname()) {
		return nil, http.ErrHandlerTimeout
	}
	return r.next.RoundTrip(req)
}
