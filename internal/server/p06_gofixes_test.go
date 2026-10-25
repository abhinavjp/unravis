package server

// TSK-21 Go fixes: one passphrase throttle for unlock and export (L1), the
// probe's metadata blocklist (L5), and a half re-wrapped case that opens or says
// what to do (M1).

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"testing"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/masterkey"
)

/* ------------------------------------------- L1: one shared throttle */

// Wrong passphrases at export and at unlock are one stream of guesses at the one
// master passphrase, so they share one counter: three misses at export and two
// at unlock make the five that start the wait, and the next unlock is refused.
func TestP06_ExportFailuresCountAgainstUnlock(t *testing.T) {
	pc := newCFSPC(t, cfsPassA, true)
	id := pc.seed("shared throttle", 3)
	out := t0Dir(t)
	for i := 0; i < 3; i++ {
		cfsReason(t, pc.export(id, out, cfsPassB), http.StatusUnauthorized, "wrong-key")
	}
	if res := pc.do(http.MethodPost, "/api/master-key/lock", ""); res.Code != http.StatusOK {
		t.Fatalf("lock = %d %s", res.Code, res.Body)
	}
	for i := 0; i < 2; i++ {
		if res := pc.do(http.MethodPost, "/api/master-key/unlock", unlockBody(cfsPassB)); res.Code != http.StatusUnauthorized {
			t.Fatalf("wrong unlock %d = %d %s", i, res.Code, res.Body)
		}
	}
	res := pc.do(http.MethodPost, "/api/master-key/unlock", unlockBody(cfsPassA))
	cfsReason(t, res, http.StatusTooManyRequests, "throttled")
	if p06Retry(t, res.Body.String()) < 1 || res.Header().Get("Retry-After") == "" {
		t.Fatalf("throttled unlock has no wait: %s", res.Body)
	}
	if st := pc.sess.State(); st != MasterKeyLocked {
		t.Fatalf("state after a throttled unlock = %v, want locked", st)
	}
}

// And the other way round: misses at unlock count against export. A wrong
// unlock cannot happen while the key is unlocked (the route then judges nothing),
// so the misses are recorded on the shared counter directly, which is what a
// failed unlock does. A right unlock would reset the counter, as it should.
func TestP06_UnlockFailuresCountAgainstExport(t *testing.T) {
	pc := newCFSPC(t, cfsPassA, true)
	id := pc.seed("shared throttle", 3)
	out := t0Dir(t)
	for i := 0; i < throttleAfter-1; i++ {
		pc.sess.unlockTh.fail()
	}
	// One wrong export is the fifth miss.
	cfsReason(t, pc.export(id, out, cfsPassB), http.StatusUnauthorized, "wrong-key")
	// The right passphrase is now refused until the wait is over.
	res := pc.export(id, out, cfsPassA)
	cfsReason(t, res, http.StatusTooManyRequests, "throttled")
	if p06Retry(t, res.Body.String()) < 1 {
		t.Fatalf("throttled export has no wait: %s", res.Body)
	}
	if entries, _ := filepath.Glob(filepath.Join(out, "*")); len(entries) != 0 {
		t.Fatalf("a throttled export left %v", entries)
	}
}

// A right passphrase at export resets the shared counter, like a right unlock.
func TestP06_RightExportPassphraseResetsTheSharedCount(t *testing.T) {
	pc := newCFSPC(t, cfsPassA, true)
	id := pc.seed("shared throttle", 3)
	out := t0Dir(t)
	for i := 0; i < throttleAfter-1; i++ {
		cfsReason(t, pc.export(id, out, cfsPassB), http.StatusUnauthorized, "wrong-key")
	}
	if res := pc.export(id, filepath.Join(out, "ok"+casestore.CaseFileExt), cfsPassA); res.Code != http.StatusOK {
		t.Fatalf("right export = %d %s", res.Code, res.Body)
	}
	if w := pc.sess.throttleWait(); w != 0 {
		t.Fatalf("wait after a right passphrase = %v", w)
	}
	for i := 0; i < throttleAfter-1; i++ {
		cfsReason(t, pc.export(id, out, cfsPassB), http.StatusUnauthorized, "wrong-key")
	}
}

// The re-auth check neither leaves a second key ring open nor changes the
// session: the ring that proved the passphrase is locked at once.
func TestP06_VerifyPassphraseLeavesTheSessionAsItWas(t *testing.T) {
	pc := newCFSPC(t, cfsPassA, true)
	if _, err := pc.sess.VerifyPassphrase([]byte(cfsPassA)); err != nil {
		t.Fatalf("VerifyPassphrase = %v", err)
	}
	if st := pc.sess.State(); st != MasterKeyUnlocked {
		t.Fatalf("state after a verify = %v, want unlocked", st)
	}
	if _, err := pc.sess.VerifyPassphrase([]byte(cfsPassB)); !errors.Is(err, masterkey.ErrWrongKey) {
		t.Fatalf("VerifyPassphrase(wrong) = %v, want ErrWrongKey", err)
	}
	if st := pc.sess.State(); st != MasterKeyUnlocked {
		t.Fatalf("state after a failed verify = %v, want unlocked", st)
	}
}

/* --------------------------------------- L5: metadata address blocklist */

func TestP06_ProbeBlocksMetadataAddressesInEveryCostume(t *testing.T) {
	for _, tc := range []struct {
		addr    string
		blocked bool
		why     string
	}{
		{"169.254.169.254", true, "link-local metadata"},
		{"168.63.129.16", true, "Azure WireServer"},
		{"::ffff:168.63.129.16", true, "WireServer, IPv4-mapped"},
		{"100.100.100.200", true, "Alibaba metadata"},
		{"fd00:ec2::254", true, "AWS IPv6 metadata"},
		{"64:ff9b::a9fe:a9fe", true, "NAT64 well-known prefix around 169.254.169.254"},
		{"64:ff9b::169.254.169.254", true, "the same, dotted spelling"},
		{"64:ff9b::a83f:8110", true, "NAT64 around the WireServer"},
		{"64:ff9b::6464:64c8", true, "NAT64 around the Alibaba address"},
		{"64:ff9b:1::a9fe:a9fe", true, "local-use NAT64, /96 position"},
		{"64:ff9b:1:a9fe:a9::", true, "local-use NAT64, /48 position"},
		{"64:ff9b:1:0:a9:fea9:fe00:0", true, "local-use NAT64, /64 position"},
		{"64:ff9b:1::1", true, "local-use NAT64 holding 0.0.0.1"},
		{"2002:a9fe:a9fe::1", true, "6to4 around 169.254.169.254"},
		{"2002:a83f:8110::", true, "6to4 around the WireServer"},
		{"2002:0:0::1", true, "6to4 around 0.0.0.0"},
		{"64:ff9b::808:808", false, "NAT64 around 8.8.8.8"},
		{"2002:808:808::1", false, "6to4 around 8.8.8.8"},
		{"2002:a00:1::", false, "6to4 around 10.0.0.1 (private ranges are allowed here)"},
		{"64:ff9b::a00:1", false, "NAT64 around 10.0.0.1"},
		{"8.8.8.8", false, "public IPv4"},
		{"10.0.0.1", false, "private IPv4 is a normal target"},
		{"2001:4860:4860::8888", false, "public IPv6"},
		{"2001:db8::1", false, "ordinary IPv6"},
	} {
		a, err := netip.ParseAddr(tc.addr)
		if err != nil {
			t.Fatalf("%s: %v", tc.addr, err)
		}
		if got := probeBlockedAddr(a); got != tc.blocked {
			t.Errorf("probeBlockedAddr(%s) = %v, want %v (%s)", tc.addr, got, tc.blocked, tc.why)
		}
	}
}

/* ---------------------------------------- M1: half re-wrapped cases */

func p06Status(t *testing.T, pc *cfsPC) masterKeyStatus {
	t.Helper()
	res := pc.do(http.MethodGet, "/api/master-key/status", "")
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d %s", res.Code, res.Body)
	}
	var st masterKeyStatus
	if err := json.Unmarshal(res.Body.Bytes(), &st); err != nil {
		t.Fatalf("status body %q: %v", res.Body, err)
	}
	return st
}

// The status route says how many cases a cut re-wrap left half done, as a subset
// of the cases still sealed by Windows; opening the case finishes it and both
// counts fall.
func TestP06_HalfRewrappedCaseShowsInStatusThenSelfHeals(t *testing.T) {
	pc, ids := p06Legacy(t, "legacy", "untouched")
	id := ids[0]
	if st := p06Status(t, pc); st.State != "unlocked" || st.DPAPICases != 2 || st.NeedsRewrap != 0 {
		t.Fatalf("before the cut: %+v", st)
	}
	p06HalfRewrap(t, pc, id, "manifest-replaced")
	if st := p06Status(t, pc); st.DPAPICases != 2 || st.NeedsRewrap != 1 {
		t.Fatalf("after the cut: %+v, want 2 DPAPI cases of which 1 half done", st)
	}
	if res := pc.do(http.MethodGet, "/api/cases/"+id, ""); res.Code != http.StatusOK {
		t.Fatalf("GET on the half re-wrapped case = %d %s", res.Code, res.Body)
	}
	if st := p06Status(t, pc); st.DPAPICases != 1 || st.NeedsRewrap != 0 {
		t.Fatalf("after the open: %+v, want the healed case counted as protected", st)
	}
	if k := pc.kind(id); k != casestore.KindMasterKey {
		t.Fatalf("kind after the open = %s", k)
	}
	if n := pc.eventCount(id); n != 4 {
		t.Fatalf("events after the heal = %d, want 4", n)
	}
}

// After a restart the app starts locked and the status leaks nothing about
// cases. Once unlocked the half done case is flagged, and the explicit re-wrap
// route finishes it.
func TestP06_HalfRewrappedCaseFinishedByTheRewrapRoute(t *testing.T) {
	pc, ids := p06Legacy(t, "legacy")
	id := ids[0]
	p06HalfRewrap(t, pc, id, "manifest-replaced")
	np := p06Restart(pc)
	if st := p06Status(t, np); st.State != "locked" || st.DPAPICases != 0 || st.NeedsRewrap != 0 {
		t.Fatalf("locked status leaks case state: %+v", st)
	}
	if res := np.do(http.MethodPost, "/api/master-key/unlock", unlockBody(cfsPassA)); res.Code != http.StatusOK {
		t.Fatalf("unlock = %d %s", res.Code, res.Body)
	}
	if st := p06Status(t, np); st.DPAPICases != 1 || st.NeedsRewrap != 1 {
		t.Fatalf("after unlock: %+v", st)
	}
	if res := np.do(http.MethodPost, "/api/master-key/rewrap", `{"all":true}`); res.Code != http.StatusOK {
		t.Fatalf("rewrap = %d %s", res.Code, res.Body)
	}
	if st := p06Status(t, np); st.DPAPICases != 0 || st.NeedsRewrap != 0 {
		t.Fatalf("after the re-wrap: %+v", st)
	}
	if res := np.do(http.MethodGet, "/api/cases/"+id, ""); res.Code != http.StatusOK {
		t.Fatalf("GET after the re-wrap = %d %s", res.Code, res.Body)
	}
}

// When a case cannot be healed the store says ErrNeedsRewrap, and the route
// turns it into a plain 409 the screens can act on, never a 500.
func TestP06_NeedsRewrapIsA409WithAReason(t *testing.T) {
	for _, err := range []error{casestore.ErrNeedsRewrap, errors.Join(errors.New("open"), casestore.ErrNeedsRewrap)} {
		w := httptest.NewRecorder()
		writeStoreError(w, err)
		f := cfsReason(t, w, http.StatusConflict, "needs-rewrap")
		if f.Error != needsRewrapText {
			t.Errorf("message = %q", f.Error)
		}
	}
	// A plain key-file error stays a server error: only the half shape is a 409.
	w := httptest.NewRecorder()
	writeStoreError(w, casestore.ErrKeyFile)
	if w.Code < 500 {
		t.Errorf("plain ErrKeyFile = %d, want a server error", w.Code)
	}
}
