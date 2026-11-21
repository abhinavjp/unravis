package update

import (
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func goodControl(channel string) Control {
	return Control{Schema: ControlSchema, Product: Product, Channel: channel, Seq: 100,
		Published: now.Add(-time.Hour), Expires: now.Add(30 * 24 * time.Hour), Blocked: []string{}}
}

func (fs *feedServer) publishControl(t *testing.T, channel string, c Control, k Key, priv ed25519.PrivateKey) {
	t.Helper()
	b, _ := json.Marshal(c)
	fs.files["/"+channel+"-control.json"] = b
	fs.files["/"+channel+"-control.json.sig"] = []byte(FormatSignature(k.Pub, ed25519.Sign(priv, b)))
}

func TestControlPauseWithdrawRollout(t *testing.T) {
	k, priv := key(t, ChannelRC)
	fs := newFeed(t)
	fs.publish(t, "rc", goodManifest("0.1.0-rc.48", ChannelRC), k, priv)
	f := fs.fetcher(t, k, ChannelRC, "0.1.0-rc.47")

	// No control file: updates flow.
	if r := f.Check(t.Context()); r.Status != StatusAvailable {
		t.Fatalf("no control: %+v", r)
	}
	// Pause.
	c := goodControl(ChannelRC)
	c.Pause, c.Note = true, "checking a problem"
	fs.publishControl(t, "rc", c, k, priv)
	r := f.Check(t.Context())
	if r.Status != StatusLatest || !strings.Contains(r.Detail, "paused") || !strings.Contains(r.Detail, "checking a problem") {
		t.Fatalf("pause: %+v", r)
	}
	// The control file goes missing: the remembered pause still holds until it expires.
	delete(fs.files, "/rc-control.json")
	delete(fs.files, "/rc-control.json.sig")
	if r := f.Check(t.Context()); r.Status != StatusLatest {
		t.Fatalf("remembered pause lost: %+v", r)
	}
	// A replayed older control file (lower seq, no pause) does not lift the pause.
	old := goodControl(ChannelRC)
	old.Seq = 50
	fs.publishControl(t, "rc", old, k, priv)
	if r := f.Check(t.Context()); r.Status != StatusLatest {
		t.Fatalf("replay lifted the pause: %+v", r)
	}
	// A newer one lifts it.
	nw := goodControl(ChannelRC)
	nw.Seq = 200
	fs.publishControl(t, "rc", nw, k, priv)
	if r := f.Check(t.Context()); r.Status != StatusAvailable {
		t.Fatalf("not lifted: %+v", r)
	}
	// Withdrawn version.
	nw.Seq, nw.Blocked = 300, []string{"0.1.0-rc.48"}
	fs.publishControl(t, "rc", nw, k, priv)
	if r := f.Check(t.Context()); r.Status != StatusLatest || !strings.Contains(r.Detail, "withdrawn") {
		t.Fatalf("withdrawn: %+v", r)
	}
	// A rollout for another version does not hold this one.
	nw.Seq, nw.Blocked, nw.RolloutVersion, nw.RolloutPercent = 400, []string{}, "0.1.0-rc.49", 1
	fs.publishControl(t, "rc", nw, k, priv)
	if r := f.Check(t.Context()); r.Status != StatusAvailable {
		t.Fatalf("rollout for another version held this one: %+v", r)
	}
}

func TestControlRolloutDrawIsStable(t *testing.T) {
	k, priv := key(t, ChannelRC)
	fs := newFeed(t)
	fs.publish(t, "rc", goodManifest("0.1.0-rc.48", ChannelRC), k, priv)
	c := goodControl(ChannelRC)
	c.RolloutVersion, c.RolloutPercent = "0.1.0-rc.48", 50
	fs.publishControl(t, "rc", c, k, priv)
	f := fs.fetcher(t, k, ChannelRC, "0.1.0-rc.47")
	first := f.Check(t.Context()).Status
	for i := 0; i < 5; i++ {
		if s := f.Check(t.Context()).Status; s != first {
			t.Fatalf("answer changed from %s to %s", first, s)
		}
	}
	// Raising the share to 100 lets every machine in.
	c.Seq, c.RolloutPercent = 101, 100
	fs.publishControl(t, "rc", c, k, priv)
	if r := f.Check(t.Context()); r.Status != StatusAvailable {
		t.Fatalf("100%%: %+v", r)
	}
	// 1% with a draw forced to 99 holds; forced to 0 lets in.
	c.Seq, c.RolloutPercent = 102, 1
	fs.publishControl(t, "rc", c, k, priv)
	st, _, _ := f.loadState()
	st.DrawFor["rc"], st.Draw["rc"] = "0.1.0-rc.48", 99
	if st.DrawFor == nil {
		t.Fatal("no draw state")
	}
	stateMu.Lock()
	_ = f.saveState(st)
	stateMu.Unlock()
	if r := f.Check(t.Context()); r.Status != StatusLatest || !strings.Contains(r.Detail, "stages") {
		t.Fatalf("draw 99 at 1%%: %+v", r)
	}
}

func TestControlRefusals(t *testing.T) {
	k, priv := key(t, ChannelRC)
	other, otherPriv := key(t, ChannelStable)
	good := goodControl(ChannelRC)
	enc := func(c Control) []byte { b, _ := json.Marshal(c); return b }
	sign := func(b []byte, kk Key, p ed25519.PrivateKey) []byte {
		return []byte(FormatSignature(kk.Pub, ed25519.Sign(p, b)))
	}
	trust := Trust{Keys: []Key{k, other}, Channel: ChannelRC, Want: ChannelRC}
	if _, err := VerifyControl(enc(good), sign(enc(good), k, priv), trust, now); err != nil {
		t.Fatal(err)
	}
	bad := map[string]func(*Control){
		"expired":      func(c *Control) { c.Expires = now.Add(-time.Minute) },
		"too long":     func(c *Control) { c.Expires = c.Published.Add(91 * 24 * time.Hour) },
		"future":       func(c *Control) { c.Published = now.Add(time.Hour) },
		"bad blocked":  func(c *Control) { c.Blocked = []string{"x"} },
		"rollout 0":    func(c *Control) { c.RolloutVersion, c.RolloutPercent = "0.1.0-rc.1", 0 },
		"percent only": func(c *Control) { c.RolloutPercent = 40 },
		"note ctrl":    func(c *Control) { c.Note = "a\nb" },
		"wrong chan":   func(c *Control) { c.Channel = ChannelStable },
		"zero seq":     func(c *Control) { c.Seq = 0 },
	}
	for name, mut := range bad {
		c := good
		mut(&c)
		if _, err := VerifyControl(enc(c), sign(enc(c), k, priv), trust, now); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// A key of the wrong role, a wrong signature, unknown fields, duplicate keys.
	sc := goodControl(ChannelStable)
	if _, err := VerifyControl(enc(sc), sign(enc(sc), other, otherPriv), trust, now); err == nil {
		t.Error("stable control accepted by the rc channel request")
	}
	if _, err := VerifyControl(enc(good), sign(enc(sc), k, priv), trust, now); err == nil {
		t.Error("signature over other bytes accepted")
	}
	extra := strings.Replace(string(enc(good)), `"pause"`, `"surprise":1,"pause"`, 1)
	if _, err := VerifyControl([]byte(extra), sign([]byte(extra), k, priv), trust, now); err == nil {
		t.Error("unknown field accepted")
	}
	dup := strings.Replace(string(enc(good)), `"pause":false`, `"pause":false,"pause":true`, 1)
	if _, err := VerifyControl([]byte(dup), sign([]byte(dup), k, priv), trust, now); err == nil {
		t.Error("duplicate key accepted")
	}
	// A manifest is never a control file and the reverse.
	mb, _ := json.Marshal(goodManifest("0.1.0-rc.48", ChannelRC))
	if _, err := VerifyControl(mb, sign(mb, k, priv), trust, now); err == nil {
		t.Error("manifest accepted as control")
	}
}

func TestControlBadFileIsIgnored(t *testing.T) {
	k, priv := key(t, ChannelRC)
	fs := newFeed(t)
	fs.publish(t, "rc", goodManifest("0.1.0-rc.48", ChannelRC), k, priv)
	c := goodControl(ChannelRC)
	c.Pause = true
	fs.publishControl(t, "rc", c, k, priv)
	fs.files["/rc-control.json"] = []byte(`{"pause":true}`) // body no longer matches the signature
	f := fs.fetcher(t, k, ChannelRC, "0.1.0-rc.47")
	if r := f.Check(t.Context()); r.Status != StatusAvailable {
		t.Fatalf("a forged control file held an update: %+v", r)
	}
}

func TestControlNotAskedWhenNothingToInstall(t *testing.T) {
	k, priv := key(t, ChannelRC)
	fs := newFeed(t)
	fs.publish(t, "rc", goodManifest("0.1.0-rc.48", ChannelRC), k, priv)
	var asked atomic.Bool
	fs.override = func(w http.ResponseWriter, r *http.Request) bool {
		if strings.Contains(r.URL.Path, "control") {
			asked.Store(true)
		}
		return false
	}
	f := fs.fetcher(t, k, ChannelRC, "0.1.0-rc.48")
	f.Check(t.Context())
	if asked.Load() {
		t.Fatal("asked for the control file with nothing to install")
	}
}
