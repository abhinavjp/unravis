package server

import (
	"net/http"
	"testing"

	"api-audit-explorer/internal/blackbox"
)

// The panel offers "Add it to the list" only when the Blackbox found on this PC is
// not in the list. A Blackbox the person added by hand (its folder, an address, a
// share) is the same one, so it must count as listed, however it was typed.

const bbLocalHost = "WEB-HOST"

func bbListedEnv(t *testing.T) *bbEnv {
	t.Helper()
	b := newBBEnv(t, func(d *BlackboxDeps) {
		d.LocalNames = func() []string { return []string{bbLocalHost, "10.1.2.3"} }
	})
	cfg := blackbox.Config{Name: "web", Folder: `D:\UnravisBlackbox`, Port: blackbox.DefaultPort, Bind: "0.0.0.0", IntervalSeconds: 60}
	rec := func(r *simRec) { r.Key = "ubk-listed-test"; r.Host = bbLocalHost }
	b.pc.seed(cfg, bbRec("web", rec))
	for _, loc := range []string{`D:\UnravisBlackbox`, `\\` + bbLocalHost + `\UnravisBlackbox`, "http://localhost:7788", "http://10.1.2.3:7788", "http://web-host.corp.local:7788"} {
		b.world.put(loc, bbRec("web", rec))
	}
	return b
}

func TestBlackboxHandAddedEntryCountsAsListedOnThisPC(t *testing.T) {
	cases := []struct{ name, typed string }{
		{"folder", `D:\UnravisBlackbox`},
		{"folder other spelling", `d:/unravisblackbox/`},
		{"share of this PC", `\\` + bbLocalHost + `\UnravisBlackbox`},
		{"loopback address", "http://127.0.0.1:7788"},
		{"localhost address", "http://localhost:7788"},
		{"LAN address of this PC", "http://10.1.2.3:7788"},
		{"host name, fully qualified", "http://web-host.corp.local:7788"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := bbListedEnv(t)
			if l := b.list(); !l.ThisPC.Installed || l.ThisPC.Listed {
				t.Fatalf("before adding: %+v", l.ThisPC)
			}
			key := ""
			if len(c.typed) > 4 && c.typed[:4] == "http" {
				key = "ubk-listed-test"
				b.world.put("http://127.0.0.1:7788", bbRec("web", func(r *simRec) { r.Key = key; r.Host = bbLocalHost }))
			}
			hand := b.mustAdd(c.typed, key)
			if hand.ThisPC {
				t.Fatal("a hand-added entry started as this PC")
			}
			l := b.list()
			if !l.ThisPC.Installed || !l.ThisPC.Listed || l.ThisPC.ListID != hand.ID {
				t.Fatalf("this PC = %+v, want listed as %s", l.ThisPC, hand.ID)
			}
			// The rescan the panel does after a change answers the same.
			var again bbThisPC
			decodeBody(t, b.e.get("/api/blackbox/this-pc"), &again)
			if !again.Listed || again.ListID != hand.ID {
				t.Fatalf("rescan = %+v", again)
			}
		})
	}
}

func TestBlackboxOtherBlackboxesDoNotHideThePrompt(t *testing.T) {
	b := bbListedEnv(t)
	// Another folder, another port on this PC, the same name on another PC, and a
	// remote address with this PC's port: none is the Blackbox found here.
	other := func(name, host, key string) *simBB {
		return bbRec(name, func(r *simRec) { r.Key = key; r.Host = host })
	}
	b.world.put(`E:\OtherBlackbox`, other("db", "OTHER-PC", ""))
	b.world.put("http://127.0.0.1:7790", other("cache", "OTHER-PC", "ubk-cache"))
	b.world.put(`\\other-pc\UnravisBlackbox`, other("files", "OTHER-PC", ""))
	b.world.put("http://other-pc:7788", other("web", "OTHER-PC", "ubk-other-web"))
	b.mustAdd(`E:\OtherBlackbox`, "")
	b.mustAdd("http://127.0.0.1:7790", "ubk-cache")
	b.mustAdd(`\\other-pc\UnravisBlackbox`, "")
	b.mustAdd("http://other-pc:7788", "ubk-other-web")
	l := b.list()
	if len(l.Items) != 4 || !l.ThisPC.Installed || l.ThisPC.Listed || l.ThisPC.ListID != "" {
		t.Fatalf("this PC = %+v with %d entries", l.ThisPC, len(l.Items))
	}
}

func TestBlackboxFlaggedEntryStillWinsAndNothingFoundClaimsNothing(t *testing.T) {
	b := bbListedEnv(t)
	hand := b.mustAdd(`D:\UnravisBlackbox`, "")
	if err := b.bbs.SetThisPC(hand.ID, true); err != nil {
		t.Fatal(err)
	}
	if l := b.list(); !l.ThisPC.Listed || l.ThisPC.ListID != hand.ID {
		t.Fatalf("this PC = %+v", l.ThisPC)
	}
	// Nothing installed on this PC: an entry by the same folder is not "this PC".
	b2 := newBBEnv(t, nil)
	b2.world.put(`D:\UnravisBlackbox`, bbRec("web", nil))
	b2.mustAdd(`D:\UnravisBlackbox`, "")
	if l := b2.list(); l.ThisPC.Installed || l.ThisPC.Listed {
		t.Fatalf("this PC = %+v", l.ThisPC)
	}
	_ = http.StatusOK
}
