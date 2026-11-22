package update

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func readStateFile(t *testing.T, f *Fetcher) State {
	t.Helper()
	st, _, err := f.loadState()
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestControlEqualSeqDifferentBytesRefused(t *testing.T) {
	k, priv := key(t, ChannelRC)
	fs := newFeed(t)
	fs.publish(t, "rc", goodManifest("0.1.0-rc.48", ChannelRC), k, priv)
	f := fs.fetcher(t, k, ChannelRC, "0.1.0-rc.47")
	a := goodControl(ChannelRC)
	fs.publishControl(t, "rc", a, k, priv)
	if r := f.Check(t.Context()); r.Status != StatusAvailable {
		t.Fatalf("%+v", r)
	}
	hash := readStateFile(t, f).ControlHash[ChannelRC]
	if hash == "" {
		t.Fatal("no control hash kept")
	}
	b := goodControl(ChannelRC)
	b.Pause = true
	fs.publishControl(t, "rc", b, k, priv)
	if r := f.Check(t.Context()); r.Status != StatusAvailable {
		t.Fatalf("a different file at the same number was taken: %+v", r)
	}
	if got := readStateFile(t, f).ControlHash[ChannelRC]; got != hash {
		t.Fatal("the hash changed")
	}
	// The other way round, in a fresh fetcher: the pause is seen first and stays.
	restart()
	g := fs.fetcher(t, k, ChannelRC, "0.1.0-rc.47")
	if r := g.Check(t.Context()); r.Status != StatusLatest || !r.Held {
		t.Fatalf("%+v", r)
	}
	fs.publishControl(t, "rc", a, k, priv)
	if r := g.Check(t.Context()); r.Status != StatusLatest || !r.Held {
		t.Fatalf("the lifting file at the same number was taken: %+v", r)
	}
}

func TestControlEqualSeqSameBytesAccepted(t *testing.T) {
	k, priv := key(t, ChannelRC)
	fs := newFeed(t)
	fs.publish(t, "rc", goodManifest("0.1.0-rc.48", ChannelRC), k, priv)
	f := fs.fetcher(t, k, ChannelRC, "0.1.0-rc.47")
	fs.publishControl(t, "rc", goodControl(ChannelRC), k, priv)
	for i := 0; i < 2; i++ {
		if r := f.Check(t.Context()); r.Status != StatusAvailable {
			t.Fatalf("round %d: %+v", i, r)
		}
	}
}

// A state file written by rc.55 has the control but no hash: the file at that number is compared by what it says.
func TestControlEqualSeqRc55State(t *testing.T) {
	k, priv := key(t, ChannelRC)
	fs := newFeed(t)
	fs.publish(t, "rc", goodManifest("0.1.0-rc.48", ChannelRC), k, priv)
	f := fs.fetcher(t, k, ChannelRC, "0.1.0-rc.47")
	fs.publishControl(t, "rc", goodControl(ChannelRC), k, priv)
	if r := f.Check(t.Context()); r.Status != StatusAvailable {
		t.Fatal(r)
	}
	st := readStateFile(t, f)
	st.ControlHash, st.KeptBlocked = nil, nil // what rc.55 wrote
	if err := f.saveState(st); err != nil {
		t.Fatal(err)
	}
	restart()
	if r := f.Check(t.Context()); r.Status != StatusAvailable {
		t.Fatalf("value-equal file refused: %+v", r)
	}
	if readStateFile(t, f).ControlHash[ChannelRC] == "" {
		t.Error("the hash was not learned")
	}
	st = readStateFile(t, f)
	st.ControlHash = nil
	_ = f.saveState(st)
	restart()
	b := goodControl(ChannelRC)
	b.Pause = true
	fs.publishControl(t, "rc", b, k, priv)
	if r := f.Check(t.Context()); r.Status != StatusAvailable {
		t.Fatalf("a different file at the same number was taken: %+v", r)
	}
}

func expiringBlock(t *testing.T) (*feedServer, *Fetcher, Key, func(ver string, c Control)) {
	t.Helper()
	k, priv := key(t, ChannelRC)
	fs := newFeed(t)
	long := goodManifest("0.1.0-rc.48", ChannelRC)
	long.Expires = now.Add(300 * 24 * time.Hour)
	fs.publish(t, "rc", long, k, priv)
	f := fs.fetcher(t, k, ChannelRC, "0.1.0-rc.47")
	c := goodControl(ChannelRC)
	c.Blocked, c.Expires = []string{"0.1.0-rc.48"}, now.Add(10*24*time.Hour)
	fs.publishControl(t, "rc", c, k, priv)
	pub := func(_ string, c Control) { fs.publishControl(t, "rc", c, k, priv) }
	return fs, f, k, pub
}

func TestControlBlockedSurvivesExpiry(t *testing.T) {
	fs, f, k, _ := expiringBlock(t)
	if r := f.Check(t.Context()); !r.Held || !strings.Contains(r.Detail, "withdrawn") {
		t.Fatalf("%+v", r)
	}
	delete(fs.files, "/rc-control.json")
	delete(fs.files, "/rc-control.json.sig")
	later := now.Add(11 * 24 * time.Hour)
	f.Now = func() time.Time { return later }
	if r := f.Check(t.Context()); !r.Held || !strings.Contains(r.Detail, "withdrawn") {
		t.Fatalf("after expiry: %+v", r)
	}
	restart()
	g := fs.fetcher(t, k, ChannelRC, "0.1.0-rc.47")
	g.StatePath = f.StatePath
	g.Now = f.Now
	if r := g.Check(t.Context()); !r.Held || !strings.Contains(r.Detail, "withdrawn") {
		t.Fatalf("after a restart: %+v", r)
	}
	// The state file is lost, the program keeps running: memory holds it.
	if err := os.Remove(g.StatePath); err != nil {
		t.Fatal(err)
	}
	if r := g.Check(t.Context()); !r.Held {
		t.Fatalf("after a lost state file: %+v", r)
	}
}

func TestControlPauseAndRolloutStillExpire(t *testing.T) {
	k, priv := key(t, ChannelRC)
	fs := newFeed(t)
	long := goodManifest("0.1.0-rc.48", ChannelRC)
	long.Expires = now.Add(300 * 24 * time.Hour)
	fs.publish(t, "rc", long, k, priv)
	f := fs.fetcher(t, k, ChannelRC, "0.1.0-rc.47")
	c := goodControl(ChannelRC)
	c.Pause, c.Expires = true, now.Add(10*24*time.Hour)
	fs.publishControl(t, "rc", c, k, priv)
	if r := f.Check(t.Context()); !r.Held {
		t.Fatalf("%+v", r)
	}
	delete(fs.files, "/rc-control.json")
	delete(fs.files, "/rc-control.json.sig")
	f.Now = func() time.Time { return now.Add(11 * 24 * time.Hour) }
	if r := f.Check(t.Context()); r.Status != StatusAvailable {
		t.Fatalf("an expired pause still held: %+v", r)
	}
}

func TestControlNewerFileLiftsKeptBlock(t *testing.T) {
	fs, f, _, pub := expiringBlock(t)
	f.Check(t.Context())
	delete(fs.files, "/rc-control.json")
	delete(fs.files, "/rc-control.json.sig")
	f.Now = func() time.Time { return now.Add(11 * 24 * time.Hour) }
	if r := f.Check(t.Context()); !r.Held {
		t.Fatalf("%+v", r)
	}
	c := goodControl(ChannelRC)
	c.Seq, c.Blocked, c.Published, c.Expires = 200, []string{}, now.Add(10*24*time.Hour), now.Add(40*24*time.Hour)
	pub("", c)
	if r := f.Check(t.Context()); r.Status != StatusAvailable {
		t.Fatalf("not lifted: %+v", r)
	}
	if kb := readStateFile(t, f).KeptBlocked[ChannelRC]; len(kb) != 0 {
		t.Fatalf("kept %v", kb)
	}
	c.Seq, c.Blocked = 300, []string{"0.1.0-rc.47"}
	pub("", c)
	if r := f.Check(t.Context()); r.Status != StatusAvailable {
		t.Fatalf("%+v", r)
	}
	if kb := readStateFile(t, f).KeptBlocked[ChannelRC]; len(kb) != 1 || kb[0] != "0.1.0-rc.47" {
		t.Fatalf("kept %v", kb)
	}
}

func TestControlKeptBlockOnlyExactVersion(t *testing.T) {
	k, priv := key(t, ChannelRC)
	fs := newFeed(t)
	m48 := goodManifest("0.1.0-rc.48", ChannelRC)
	m48.Expires = now.Add(300 * 24 * time.Hour)
	fs.publish(t, "rc", m48, k, priv)
	f := fs.fetcher(t, k, ChannelRC, "0.1.0-rc.47")
	c := goodControl(ChannelRC)
	c.Blocked, c.Expires = []string{"0.1.0-rc.48"}, now.Add(10*24*time.Hour)
	fs.publishControl(t, "rc", c, k, priv)
	if r := f.Check(t.Context()); !r.Held {
		t.Fatal(r)
	}
	delete(fs.files, "/rc-control.json")
	delete(fs.files, "/rc-control.json.sig")
	m49 := goodManifest("0.1.0-rc.49", ChannelRC)
	m49.Seq, m49.Expires = m48.Seq+1, now.Add(300*24*time.Hour)
	fs.publish(t, "rc", m49, k, priv)
	fs.etag = `"v2"`
	f.Now = func() time.Time { return now.Add(11 * 24 * time.Hour) }
	if r := f.Check(t.Context()); r.Status != StatusAvailable {
		t.Fatalf("the next release was held by an old list: %+v", r)
	}
}

func TestKeptBlockMigratesFromRc55LiveControl(t *testing.T) {
	fs, f, _, _ := expiringBlock(t)
	f.Check(t.Context())
	st := readStateFile(t, f)
	st.ControlHash, st.KeptBlocked = nil, nil
	_ = f.saveState(st)
	restart()
	delete(fs.files, "/rc-control.json")
	delete(fs.files, "/rc-control.json.sig")
	if r := f.Check(t.Context()); !r.Held { // the remembered live control holds, and its list is kept from now on
		t.Fatalf("%+v", r)
	}
	if len(readStateFile(t, f).KeptBlocked[ChannelRC]) != 1 {
		t.Fatal("not migrated")
	}
	f.Now = func() time.Time { return now.Add(11 * 24 * time.Hour) }
	if r := f.Check(t.Context()); !r.Held {
		t.Fatalf("after expiry: %+v", r)
	}
}

func TestStateFileRc55LoadsAndTheNewOneReadsBackInRc55Shape(t *testing.T) {
	fs, f, _, _ := expiringBlock(t)
	f.Check(t.Context())
	b, err := os.ReadFile(f.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	var old struct {
		SeenSeq    map[string]uint64   `json:"seenSeq"`
		SeenHash   map[string]string   `json:"seenHash"`
		Control    map[string]*Control `json:"control"`
		ControlSeq map[string]uint64   `json:"controlSeq"`
	}
	if err := json.Unmarshal(b, &old); err != nil {
		t.Fatal(err)
	}
	if old.SeenSeq[ChannelRC] == 0 || old.ControlSeq[ChannelRC] == 0 {
		t.Fatalf("%+v", old)
	}
	_ = fs
	entries, _ := os.ReadDir(strings.TrimSuffix(f.StatePath, "state.json"))
	for _, e := range entries {
		if strings.Contains(e.Name(), ".corrupt-") {
			t.Fatal("a state file was set aside")
		}
	}
}

// A saved state that is behind the memory (a failed save, or an old file put back) must not make the genuine newer control
// file look like a different file at the same number.
func TestControlStaleStateDoesNotRefuseTheNewerFile(t *testing.T) {
	k, priv := key(t, ChannelRC)
	fs := newFeed(t)
	fs.publish(t, "rc", goodManifest("0.1.0-rc.48", ChannelRC), k, priv)
	f := fs.fetcher(t, k, ChannelRC, "0.1.0-rc.47")
	pause := goodControl(ChannelRC)
	pause.Pause, pause.Blocked = true, []string{"0.1.0-rc.48"}
	fs.publishControl(t, "rc", pause, k, priv)
	if r := f.Check(t.Context()); !r.Held {
		t.Fatalf("%+v", r)
	}
	old, err := os.ReadFile(f.StatePath) // the seq 100 state
	if err != nil {
		t.Fatal(err)
	}
	lift := goodControl(ChannelRC)
	lift.Seq = 200
	fs.publishControl(t, "rc", lift, k, priv)
	if r := f.Check(t.Context()); r.Status != StatusAvailable {
		t.Fatalf("%+v", r)
	}
	if err := os.WriteFile(f.StatePath, old, 0o600); err != nil { // the old file comes back
		t.Fatal(err)
	}
	if r := f.Check(t.Context()); r.Status != StatusAvailable {
		t.Fatalf("the genuine newer file was refused: %+v", r)
	}
	restart()
	if r := f.Check(t.Context()); r.Status != StatusAvailable {
		t.Fatalf("after a restart: %+v", r)
	}
	if kb := readStateFile(t, f).KeptBlocked[ChannelRC]; len(kb) != 0 {
		t.Fatalf("the old withdrawn list is held again: %v", kb)
	}
}

// After a lost state file the saved state is rebuilt from the memory: seq, hash and the withdrawn list.
func TestControlLostStateIsRebuiltFromMemory(t *testing.T) {
	fs, f, k, _ := expiringBlock(t)
	if r := f.Check(t.Context()); !r.Held {
		t.Fatalf("%+v", r)
	}
	if err := os.Remove(f.StatePath); err != nil {
		t.Fatal(err)
	}
	delete(fs.files, "/rc-control.json")
	delete(fs.files, "/rc-control.json.sig")
	if r := f.Check(t.Context()); !r.Held {
		t.Fatalf("%+v", r)
	}
	st := readStateFile(t, f)
	if st.ControlSeq[ChannelRC] == 0 || st.ControlHash[ChannelRC] == "" || len(st.KeptBlocked[ChannelRC]) != 1 {
		t.Fatalf("the saved state lost what the memory knew: %+v", st)
	}
	_ = k
}
