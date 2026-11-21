package update

import (
	"net/http"
	"strings"
	"testing"
)

func TestMirrorUsedOnlyWhenMainCannotBeReached(t *testing.T) {
	k, priv := key(t, ChannelRC)
	main, mir := newFeed(t), newFeed(t)
	main.publish(t, "rc", goodManifest("0.1.0-rc.48", ChannelRC), k, priv)
	mir.publish(t, "rc", goodManifest("0.1.0-rc.48", ChannelRC), k, priv)
	f := main.fetcher(t, k, ChannelRC, "0.1.0-rc.47")
	f.Mirrors = []string{mir.srv.URL}

	// Main is fine: the mirror is never asked.
	if r := f.Check(t.Context()); r.Status != StatusAvailable || mir.hits.Load() != 0 {
		t.Fatalf("%+v mirror hits %d", r, mir.hits.Load())
	}
	// Main is rate limited: the mirror answers.
	main.override = func(w http.ResponseWriter, r *http.Request) bool {
		w.WriteHeader(http.StatusTooManyRequests)
		return true
	}
	r := f.Check(t.Context())
	// The backup can say a newer version exists, but it never starts an install, and the main address's slow-down is kept.
	if r.Status != StatusLatest || !r.Held || !strings.Contains(r.Detail, "rc.48") || !r.Backoff {
		t.Fatalf("%+v", r)
	}
	// Main answers but its file is forged: that is a refusal, and the mirror is not consulted to override it.
	main.override = nil
	main.etag = `"v2"`
	main.files["/rc.json"] = []byte(`{"seq":1}`)
	hits := mir.hits.Load()
	if r := f.Check(t.Context()); r.Status != StatusRejected || mir.hits.Load() != hits {
		t.Fatalf("%+v", r)
	}
}

func TestMirrorCannotRollBackOrForge(t *testing.T) {
	k, priv := key(t, ChannelRC)
	main, mir := newFeed(t), newFeed(t)
	newer := goodManifest("0.1.0-rc.48", ChannelRC)
	main.publish(t, "rc", newer, k, priv)
	f := main.fetcher(t, k, ChannelRC, "0.1.0-rc.40")
	f.Mirrors = []string{mir.srv.URL}
	if r := f.Check(t.Context()); r.Status != StatusAvailable {
		t.Fatalf("%+v", r)
	}
	// Main goes down; the mirror is stale (older seq than already seen): not accepted, the honest answer is "unreachable".
	old := goodManifest("0.1.0-rc.41", ChannelRC)
	old.Seq = 41
	mir.publish(t, "rc", old, k, priv)
	main.override = func(w http.ResponseWriter, r *http.Request) bool {
		w.WriteHeader(http.StatusServiceUnavailable)
		return true
	}
	if r := f.Check(t.Context()); r.Status != StatusUnreachable {
		t.Fatalf("stale mirror accepted: %+v", r)
	}
	// A mirror holding a manifest signed by an untrusted key is not accepted either.
	_, other := key(t, ChannelRC)
	ok2, _ := key(t, ChannelRC)
	mir.publish(t, "rc", newer, ok2, other)
	if r := f.Check(t.Context()); r.Status != StatusUnreachable {
		t.Fatalf("forged mirror accepted: %+v", r)
	}
	// A mirror with the same release, validly signed: informs only (no install from a copy that cannot prove it is current).
	mir.publish(t, "rc", newer, k, priv)
	if r := f.Check(t.Context()); r.Status != StatusLatest || !r.Held {
		t.Fatalf("good mirror: %+v", r)
	}
}
