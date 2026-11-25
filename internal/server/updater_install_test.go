package server

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/update"
)

type installRig struct {
	t       *testing.T
	u       *Updater
	feed    *fakeFeed
	payload []byte
	launch  atomic.Int32
	args    []string
	failure error
	h       http.Handler
	key     update.Key
	priv    ed25519.PrivateKey
	// setupName is the file name of the setup asset of the last published release.
	setupName string
}

func newInstallRig(t *testing.T) *installRig {
	t.Helper()
	setVersion(t, "0.1.0-rc.47")
	k, priv := rcKey(t)
	rg := &installRig{t: t, feed: newFakeFeed(t), payload: bytes.Repeat([]byte("setup-bytes"), 3000), key: k, priv: priv}
	rg.publish("0.1.0-rc.48", 4801, rg.payload)
	rg.u = testUpdater(t, rg.feed, k)
	dir := t.TempDir()
	rg.u.EnableInstall(dir, func(path, sha, ver string) error {
		rg.launch.Add(1)
		rg.args = []string{path, sha, ver}
		return rg.failure
	})
	rg.u.dl.Base, rg.u.dl.HTTP, rg.u.dl.AllowHost = rg.feed.srv.URL, rg.feed.srv.Client(), func(string) bool { return true }
	rg.h = updHandler(rg.u)
	return rg
}

// publish signs a release whose setup file is payload and serves that file.
func (rg *installRig) publish(ver string, seq uint64, payload []byte) {
	rg.t.Helper()
	rg.feed.publish(rg.t, "rc", ver, seq, rg.key, rg.priv)
	var m update.Manifest
	if err := json.Unmarshal(rg.feed.body["/rc.json"], &m); err != nil {
		rg.t.Fatal(err)
	}
	sum := sha256.Sum256(payload)
	a := m.Assets[update.AssetSetup]
	a.SHA256, a.Size = hex.EncodeToString(sum[:]), int64(len(payload))
	m.Assets[update.AssetSetup] = a
	b, _ := json.Marshal(m)
	rg.feed.body["/rc.json"] = b
	rg.feed.body["/rc.json.sig"] = []byte(update.FormatSignature(rg.key.Pub, signBytes(rg.priv, b)))
	rg.feed.body["/v"+ver+"/"+a.Name] = payload
	rg.setupName = a.Name
}

func (rg *installRig) post(path string) (int, UpdateStatus) {
	rec := request(rg.h, http.MethodPost, path, "", true)
	var s UpdateStatus
	_ = json.Unmarshal(rec.Body.Bytes(), &s)
	return rec.Code, s
}

func (rg *installRig) waitFor(state string) UpdateStatus {
	rg.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s := rg.u.Status(); s.Install != nil && s.Install.State == state {
			return s
		}
		time.Sleep(10 * time.Millisecond)
	}
	rg.t.Fatalf("never reached %q: %+v", state, rg.u.Status().Install)
	return UpdateStatus{}
}

func TestInstallFlowDownloadThenInstall(t *testing.T) {
	rg := newInstallRig(t)
	// nothing to download before a check found something
	if code, _ := rg.post("/api/update/download"); code != http.StatusConflict {
		t.Fatalf("download before any check = %d", code)
	}
	rg.u.CheckNow(t.Context())
	if s := rg.u.Status(); s.Status != "available" || !s.CanInstall || s.Install != nil {
		t.Fatalf("%+v", s)
	}
	// install before download is refused and starts nothing
	if code, _ := rg.post("/api/update/install"); code != http.StatusConflict || rg.launch.Load() != 0 {
		t.Fatalf("install before download = %d, launched %d", code, rg.launch.Load())
	}
	if code, _ := rg.post("/api/update/download"); code != http.StatusOK {
		t.Fatalf("download = %d", code)
	}
	s := rg.waitFor("ready")
	if s.Install.Done != int64(len(rg.payload)) || s.Install.Version != "0.1.0-rc.48" {
		t.Fatalf("%+v", s.Install)
	}
	code, s := rg.post("/api/update/install")
	if code != http.StatusOK || s.Install == nil || s.Install.State != "launched" || rg.launch.Load() != 1 {
		t.Fatalf("install = %d %+v launched %d", code, s.Install, rg.launch.Load())
	}
	sum := sha256.Sum256(rg.payload)
	if rg.args[1] != hex.EncodeToString(sum[:]) || rg.args[2] != "0.1.0-rc.48" || !strings.HasPrefix(rg.args[0], rg.u.dl.Dir) {
		t.Errorf("handed over %v", rg.args)
	}
}

func TestUpdateDownloadAndInstallTouchPower(t *testing.T) {
	rg := newInstallRig(t)
	var touches atomic.Int32
	rg.u.SetPowerTouch(func() { touches.Add(1) })
	rg.u.CheckNow(t.Context())
	if code, _ := rg.post("/api/update/download"); code != http.StatusOK {
		t.Fatalf("download = %d", code)
	}
	rg.waitFor("ready")
	if got := touches.Load(); got != 1 {
		t.Fatalf("download touched power %d times", got)
	}
	if code, _ := rg.post("/api/update/install"); code != http.StatusOK {
		t.Fatalf("install = %d", code)
	}
	if got := touches.Load(); got != 2 {
		t.Fatalf("download and install touched power %d times", got)
	}
}

func TestInstallRefusesAFileChangedAfterDownload(t *testing.T) {
	rg := newInstallRig(t)
	rg.u.CheckNow(t.Context())
	rg.post("/api/update/download")
	rg.waitFor("ready")
	files, _ := filepath.Glob(filepath.Join(rg.u.dl.Dir, "unravis-setup-*"))
	if len(files) != 1 {
		t.Fatalf("files: %v", files)
	}
	if err := os.WriteFile(files[0], bytes.Repeat([]byte("X"), len(rg.payload)), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _ := rg.post("/api/update/install"); code != http.StatusConflict || rg.launch.Load() != 0 {
		t.Fatalf("a swapped file was started: code %d launched %d", code, rg.launch.Load())
	}
	if s := rg.u.Status(); s.Install == nil || s.Install.State != "failed" {
		t.Errorf("%+v", s.Install)
	}
}

func TestDownloadFailureAndLaunchFailureAreShown(t *testing.T) {
	rg := newInstallRig(t)
	rg.feed.body["/v0.1.0-rc.48/unravis-setup-v0.1.0-rc.48-windows-x64.exe"] = bytes.Repeat([]byte("Z"), len(rg.payload)) // same size, wrong bytes
	rg.u.CheckNow(t.Context())
	rg.post("/api/update/download")
	s := rg.waitFor("failed")
	if !strings.Contains(s.Install.Error, "does not match") {
		t.Errorf("%q", s.Install.Error)
	}
	if code, _ := rg.post("/api/update/install"); code != http.StatusConflict || rg.launch.Load() != 0 {
		t.Error("installed after a failed download")
	}
	// the good file is back; try again, then the launcher fails
	rg.feed.body["/v0.1.0-rc.48/unravis-setup-v0.1.0-rc.48-windows-x64.exe"] = rg.payload
	rg.post("/api/update/download")
	rg.waitFor("ready")
	rg.failure = http.ErrAbortHandler
	if code, _ := rg.post("/api/update/install"); code != http.StatusConflict {
		t.Errorf("launch failure = %d", code)
	}
	if s := rg.u.Status(); s.Install == nil || s.Install.State != "failed" {
		t.Errorf("%+v", s.Install)
	}
}

func TestInstallNeedsAVerifiedUpdateAndABuildThatCan(t *testing.T) {
	// no EnableInstall: buttons are off
	setVersion(t, "0.1.0-rc.47")
	k, priv := rcKey(t)
	feed := newFakeFeed(t)
	feed.publish(t, "rc", "0.1.0-rc.48", 4801, k, priv)
	u := testUpdater(t, feed, k)
	u.CheckNow(t.Context())
	if s := u.Status(); s.CanInstall {
		t.Error("CanInstall without a launcher")
	}
	if code := request(updHandler(u), http.MethodPost, "/api/update/download", "", true).Code; code != http.StatusConflict {
		t.Errorf("download = %d", code)
	}
	// no token
	if code := request(updHandler(u), http.MethodPost, "/api/update/install", "", false).Code; code == http.StatusOK {
		t.Error("install worked without the token")
	}
	// an unverified answer offers nothing
	setVersion(t, "0.1.0-rc.47")
	rg := newInstallRig(t)
	rg.feed.body["/rc.json.sig"] = []byte("unravis-sig-v1 00 AAAA")
	rg.u.CheckNow(t.Context())
	if code, _ := rg.post("/api/update/download"); code != http.StatusConflict {
		t.Errorf("download after a bad signature = %d", code)
	}
}

func TestInstallDemoPreviews(t *testing.T) {
	h := New(audit.NewDemo(time.Now()), nil, testToken, true, WithUpdateCheckDemo())
	for _, c := range []struct{ path, want string }{
		{"/api/update/download?preview=available", "ready"},
		{"/api/update/install?preview=available", "launched"},
	} {
		rec := request(h, http.MethodPost, c.path, "", true)
		var s UpdateStatus
		if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil || rec.Code != 200 || s.Install == nil || s.Install.State != c.want {
			t.Errorf("%s: %d %s", c.path, rec.Code, rec.Body.String())
		}
	}
	rec := request(h, http.MethodGet, "/api/update/status?preview=downloading", "", true)
	var s UpdateStatus
	_ = json.Unmarshal(rec.Body.Bytes(), &s)
	if s.Install == nil || s.Install.State != "downloading" || s.Install.Total == 0 {
		t.Errorf("%s", rec.Body.String())
	}
}

func signBytes(priv ed25519.PrivateKey, b []byte) []byte { return ed25519.Sign(priv, b) }

func (rg *installRig) readyDownload() {
	rg.t.Helper()
	rg.u.CheckNow(rg.t.Context())
	rg.post("/api/update/download")
	rg.waitFor("ready")
}

// A later check that fails (offline, bad answer) must not hide a verified update or a finished download.
func TestAvailableSurvivesAFailedCheck(t *testing.T) {
	rg := newInstallRig(t)
	rg.readyDownload()
	rg.feed.code = http.StatusServiceUnavailable
	rg.u.CheckNow(t.Context())
	s := rg.u.Status()
	if s.Status != "available" || s.Install == nil || s.Install.State != "ready" {
		t.Fatalf("a failed check hid the update: %+v %+v", s, s.Install)
	}
	// The last look at the feed fails closed: with the feed down, nothing starts, and the download stays ready.
	if code, _ := rg.post("/api/update/install"); code != http.StatusConflict || rg.launch.Load() != 0 {
		t.Fatalf("install with the feed down = %d, launched %d", code, rg.launch.Load())
	}
	if s := rg.u.Status(); s.Install == nil || s.Install.State != "ready" {
		t.Fatalf("the download was lost: %+v", s.Install)
	}
	// a bad signature (unverified) leaves it on offer too
	rg.feed.code = 0
	rg.feed.body["/rc.json.sig"] = []byte("unravis-sig-v1 00 AAAA")
	rg.u.CheckNow(t.Context())
	if s := rg.u.Status(); s.Status != "available" {
		t.Errorf("an unverified answer replaced the verified update: %+v", s)
	}
}

// The same release signed again with a higher seq (an expiry refresh) keeps the download.
func TestSameFileNewSeqKeepsTheDownload(t *testing.T) {
	rg := newInstallRig(t)
	rg.readyDownload()
	rg.publish("0.1.0-rc.48", 4802, rg.payload)
	rg.u.CheckNow(t.Context())
	s := rg.u.Status()
	if s.Install == nil || s.Install.State != "ready" {
		t.Fatalf("the download was dropped: %+v", s.Install)
	}
	if code, _ := rg.post("/api/update/install"); code != http.StatusOK || rg.launch.Load() != 1 {
		t.Fatalf("install = %d, launched %d", code, rg.launch.Load())
	}
}

// A different file (a newer version) throws the old download away: no Install for it, and a new download works.
func TestNewerVersionDropsTheOldDownload(t *testing.T) {
	rg := newInstallRig(t)
	rg.readyDownload()
	newPayload := bytes.Repeat([]byte("newer-setup"), 2000)
	rg.publish("0.1.0-rc.49", 4901, newPayload)
	rg.u.CheckNow(t.Context())
	if s := rg.u.Status(); s.Latest != "0.1.0-rc.49" || s.Install != nil {
		t.Fatalf("%+v %+v", s, s.Install)
	}
	if code, _ := rg.post("/api/update/install"); code != http.StatusConflict || rg.launch.Load() != 0 {
		t.Fatalf("install of a dropped download = %d, launched %d", code, rg.launch.Load())
	}
	rg.post("/api/update/download")
	rg.waitFor("ready")
	if code, _ := rg.post("/api/update/install"); code != http.StatusOK || rg.args[2] != "0.1.0-rc.49" {
		t.Fatalf("install = %d %v", code, rg.args)
	}
	// the older setup file was pruned
	if files, _ := filepath.Glob(filepath.Join(rg.u.dl.Dir, "unravis-setup-*")); len(files) != 1 {
		t.Errorf("files left: %v", files)
	}
}

// A person who closed the installer window can open it again.
func TestInstallCanBePressedAgainAfterLaunched(t *testing.T) {
	rg := newInstallRig(t)
	rg.readyDownload()
	rg.post("/api/update/install")
	if code, s := rg.post("/api/update/install"); code != http.StatusOK || s.Install.State != "launched" || rg.launch.Load() != 2 {
		t.Fatalf("second install = %d %+v launched %d", code, s.Install, rg.launch.Load())
	}
}

// An expired signed answer offers nothing and installs nothing.
func TestExpiredAnswerIsNotInstalled(t *testing.T) {
	rg := newInstallRig(t)
	rg.readyDownload()
	rg.u.mu.Lock()
	m := *rg.u.avail
	m.Expires = time.Now().Add(-time.Minute)
	rg.u.avail = &m
	rg.u.mu.Unlock()
	if code, _ := rg.post("/api/update/install"); code != http.StatusConflict || rg.launch.Load() != 0 {
		t.Fatalf("install on an expired answer = %d, launched %d", code, rg.launch.Load())
	}
	if s := rg.u.Status(); s.Status == "available" {
		t.Errorf("an expired answer is still offered: %+v", s)
	}
}

// Only one download at a time, and no Install while one runs.
func TestOneDownloadAtATime(t *testing.T) {
	rg := newInstallRig(t)
	gate := make(chan struct{})
	inner := rg.feed.srv.Config.Handler
	rg.feed.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "unravis-setup") {
			<-gate
		}
		inner.ServeHTTP(w, r)
	})
	rg.u.CheckNow(t.Context())
	if code, _ := rg.post("/api/update/download"); code != http.StatusOK {
		t.Fatal(code)
	}
	if code, _ := rg.post("/api/update/download"); code != http.StatusConflict {
		t.Errorf("a second download started: %d", code)
	}
	if code, _ := rg.post("/api/update/install"); code != http.StatusConflict || rg.launch.Load() != 0 {
		t.Errorf("install during a download = %d", code)
	}
	close(gate)
	rg.waitFor("ready")
	// Close() stops a running download
	rg2 := newInstallRig(t)
	gate2 := make(chan struct{})
	t.Cleanup(func() { close(gate2) })
	inner2 := rg2.feed.srv.Config.Handler
	rg2.feed.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "unravis-setup") {
			select {
			case <-gate2:
			case <-r.Context().Done():
			}
		}
		inner2.ServeHTTP(w, r)
	})
	rg2.u.CheckNow(t.Context())
	rg2.post("/api/update/download")
	rg2.u.Close()
	s := rg2.waitFor("failed")
	if s.Install.Error == "" {
		t.Error("no reason shown")
	}
}

// "You are up to date" clears the offer: a failed check after it must not bring the old one back.
func TestLatestClearsTheOffer(t *testing.T) {
	rg := newInstallRig(t)
	rg.u.CheckNow(t.Context())
	rg.publish("0.1.0-rc.47", 4803, rg.payload) // the running version again, signed later
	rg.u.CheckNow(t.Context())
	if s := rg.u.Status(); s.Status != "latest" {
		t.Fatalf("%+v", s)
	}
	rg.feed.code = http.StatusServiceUnavailable
	rg.u.CheckNow(t.Context())
	if s := rg.u.Status(); s.Status == "available" {
		t.Errorf("the old offer came back: %+v", s)
	}
}

// After a failed hand-off the person presses Try again (download), not Install on the failed state.
func TestInstallIsRefusedInTheFailedState(t *testing.T) {
	rg := newInstallRig(t)
	rg.readyDownload()
	rg.failure = http.ErrAbortHandler
	rg.post("/api/update/install")
	rg.failure = nil
	if code, _ := rg.post("/api/update/install"); code != http.StatusConflict || rg.launch.Load() != 1 {
		t.Fatalf("install in the failed state = %d, launched %d", code, rg.launch.Load())
	}
	// Try again reuses the verified file at once
	rg.post("/api/update/download")
	rg.waitFor("ready")
	if code, _ := rg.post("/api/update/install"); code != http.StatusOK {
		t.Fatalf("install after trying again = %d", code)
	}
}

// A download of an old version that is still running when a newer version is found must not touch the new state.
func TestStaleDownloadResultIsIgnored(t *testing.T) {
	rg := newInstallRig(t)
	gate := make(chan struct{})
	inner := rg.feed.srv.Config.Handler
	rg.feed.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "v0.1.0-rc.48/unravis-setup") {
			<-gate // the old download hangs here (and finishes fine when released)
		}
		inner.ServeHTTP(w, r)
	})
	rg.u.CheckNow(t.Context())
	rg.post("/api/update/download")
	rg.publish("0.1.0-rc.49", 4901, bytes.Repeat([]byte("newer-setup"), 2000))
	rg.u.CheckNow(t.Context()) // finds the newer version: the old download is thrown away
	close(gate)
	time.Sleep(300 * time.Millisecond) // the old goroutine finishes now
	rg.u.inst.mu.Lock()
	state, running := rg.u.inst.info.State, rg.u.inst.running
	rg.u.inst.mu.Unlock()
	if state != "" || running {
		t.Fatalf("the old download changed the new state: %q running=%v", state, running)
	}
	// and the new version downloads normally
	if code, _ := rg.post("/api/update/download"); code != http.StatusOK {
		t.Fatalf("download of the new version = %d", code)
	}
	if s := rg.waitFor("ready"); s.Install.Version != "0.1.0-rc.49" {
		t.Fatalf("%+v", s.Install)
	}
}

// Pressing Install twice must start the installer once.
func TestDoubleInstallStartsOnce(t *testing.T) {
	rg := newInstallRig(t)
	rg.readyDownload()
	inLaunch, release := make(chan struct{}), make(chan struct{})
	rg.u.launch = func(path, sha, ver string) error {
		rg.launch.Add(1)
		close(inLaunch)
		<-release
		return nil
	}
	done := make(chan error, 1)
	go func() { done <- rg.u.Install() }()
	<-inLaunch
	if err := rg.u.Install(); err == nil {
		t.Error("a second Install ran while the first was starting")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if rg.launch.Load() != 1 {
		t.Errorf("launched %d times", rg.launch.Load())
	}
}

// publishControl signs a control file for the rc channel into the fake feed.
func (rg *installRig) publishControl(c update.Control) {
	rg.t.Helper()
	b, _ := json.Marshal(c)
	rg.feed.body["/rc-control.json"] = b
	rg.feed.body["/rc-control.json.sig"] = []byte(update.FormatSignature(rg.key.Pub, signBytes(rg.priv, b)))
}

func (rg *installRig) control(seq uint64, mut func(*update.Control)) update.Control {
	c := update.Control{Schema: update.ControlSchema, Product: update.Product, Channel: update.ChannelRC, Seq: seq,
		Published: time.Now().Add(-time.Hour), Expires: time.Now().Add(30 * 24 * time.Hour), Blocked: []string{}}
	if mut != nil {
		mut(&c)
	}
	return c
}

func TestInstallRechecksAndRefusesAPause(t *testing.T) {
	rg := newInstallRig(t)
	rg.readyDownload()
	rg.publishControl(rg.control(100, func(c *update.Control) { c.Pause = true }))
	code, _ := rg.post("/api/update/install")
	if code != http.StatusConflict || rg.launch.Load() != 0 {
		t.Fatalf("install = %d, launched %d", code, rg.launch.Load())
	}
	s := rg.u.Status()
	if !strings.Contains(s.Held, "paused") || s.Install != nil {
		t.Fatalf("%+v %+v", s, s.Install)
	}
}

func TestInstallRechecksAndRefusesAWithdrawal(t *testing.T) {
	rg := newInstallRig(t)
	rg.readyDownload()
	rg.publishControl(rg.control(100, func(c *update.Control) { c.Blocked = []string{"0.1.0-rc.48"} }))
	if code, _ := rg.post("/api/update/install"); code != http.StatusConflict || rg.launch.Load() != 0 {
		t.Fatalf("install = %d, launched %d", code, rg.launch.Load())
	}
	if s := rg.u.Status(); !strings.Contains(s.Held, "withdrawn") {
		t.Fatalf("%+v", s)
	}
}

func TestInstallRefusedWhenFeedUnreachableAtRecheckThenWorks(t *testing.T) {
	rg := newInstallRig(t)
	rg.readyDownload()
	rg.feed.code = http.StatusServiceUnavailable
	rec := request(rg.h, http.MethodPost, "/api/update/install", "", true)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "Could not reach") || rg.launch.Load() != 0 {
		t.Fatalf("%d %s launched %d", rec.Code, rec.Body.String(), rg.launch.Load())
	}
	rg.feed.code = 0
	if code, _ := rg.post("/api/update/install"); code != http.StatusOK || rg.launch.Load() != 1 {
		t.Fatalf("install after the feed came back = %d, launched %d", code, rg.launch.Load())
	}
}

func TestInstallRefusedWhenRecheckAnswerIsRejected(t *testing.T) {
	rg := newInstallRig(t)
	rg.readyDownload()
	rg.feed.body["/rc.json.sig"] = []byte("unravis-sig-v1 00 AAAA")
	if code, _ := rg.post("/api/update/install"); code != http.StatusConflict || rg.launch.Load() != 0 {
		t.Fatalf("install = %d, launched %d", code, rg.launch.Load())
	}
	if s := rg.u.Status(); s.Install == nil || s.Install.State != "ready" {
		t.Fatalf("the download was lost: %+v", s.Install)
	}
}

func TestInstallRunsAFreshCheck(t *testing.T) {
	rg := newInstallRig(t)
	rg.readyDownload()
	before := rg.feed.hits.Load()
	if code, _ := rg.post("/api/update/install"); code != http.StatusOK || rg.launch.Load() != 1 {
		t.Fatalf("install = %d, launched %d", code, rg.launch.Load())
	}
	if rg.feed.hits.Load() <= before {
		t.Fatal("the feed was not asked again before the start")
	}
}

func TestInstallAfterAHoldIsLiftedReusesTheFile(t *testing.T) {
	rg := newInstallRig(t)
	rg.readyDownload()
	rg.publishControl(rg.control(100, func(c *update.Control) { c.Pause = true }))
	if code, _ := rg.post("/api/update/install"); code != http.StatusConflict {
		t.Fatalf("install = %d", code)
	}
	rg.publishControl(rg.control(200, nil))
	rg.u.CheckNow(t.Context())
	s := rg.u.Status()
	if s.Status != "available" {
		t.Fatalf("%+v", s)
	}
	if code, _ := rg.post("/api/update/download"); code != http.StatusOK {
		t.Fatalf("download = %d", code)
	}
	rg.waitFor("ready")
	if code, _ := rg.post("/api/update/install"); code != http.StatusOK || rg.launch.Load() != 1 {
		t.Fatalf("install = %d, launched %d", code, rg.launch.Load())
	}
}
