package blackbox

import "testing"

func TestLocalFoundIsEntry(t *testing.T) {
	// The fully qualified spelling of this PC points at its address; a host in
	// another domain with the same first label points elsewhere or nowhere.
	old := ResolveHost
	t.Cleanup(func() { ResolveHost = old })
	ResolveHost = func(h string) []string {
		switch h {
		case "web-host.corp":
			return []string{"10.1.2.3"}
		case "web-host.partner.example":
			return []string{"203.0.113.9"}
		}
		return nil
	}
	f := LocalFound{Installed: true, Name: "web", Folder: `D:\UnravisBlackbox`, Port: 7788, Bind: "0.0.0.0"}
	local := []string{"Web-Host", "10.1.2.3", "fe80::1"}
	mustLoc := func(s string) Location {
		l, err := ParseLocation(s)
		if err != nil {
			t.Fatalf("%q: %v", s, err)
		}
		return l
	}
	cases := []struct {
		name          string
		loc           string
		recHost, recN string
		want          bool
	}{
		{"same folder", `D:\UnravisBlackbox`, "", "", true},
		{"folder, case, slashes and trailing slash", `d:/UNRAVISBLACKBOX//`, "", "", true},
		{"other folder", `D:\Other`, "", "", false},
		{"subfolder is not the folder", `D:\UnravisBlackbox\sub`, "", "", false},
		{"loopback", "http://127.0.0.1:7788", "", "", true},
		{"localhost", "http://LocalHost:7788", "", "", true},
		{"IPv6 loopback", "http://[::1]:7788", "", "", true},
		{"any-address", "http://0.0.0.0:7788", "", "", true},
		{"LAN address", "http://10.1.2.3:7788", "", "", true},
		{"host name, other case", "http://WEB-HOST:7788", "", "", true},
		{"host name, fully qualified", "http://web-host.corp:7788", "", "", true},
		{"same first label, other domain", "http://web-host.partner.example:7788", "", "", false},
		{"same first label, does not resolve", "http://web-host.nowhere.example:7788", "", "", false},
		{"other port", "http://127.0.0.1:7790", "", "", false},
		{"remote host", "http://other:7788", "", "", false},
		{"remote address", "http://10.9.9.9:7788", "", "", false},
		{"https is not the status page", "https://127.0.0.1:7788", "", "", false},
		{"share, recorder known to be this PC's", `\\fileserver\bb`, "web-host", "WEB", true},
		{"share, recorder unknown", `\\web-host\bb`, "", "", false},
		{"share, same name on another PC", `\\fileserver\bb`, "other-pc", "web", false},
		{"share, recorder of another domain with this PC's first label", `\\fileserver\bb`, "web-host.partner.example", "web", false},
		{"share, other recorder on this PC", `\\web-host\bb`, "web-host", "db", false},
	}
	for _, c := range cases {
		if got := f.IsEntry(mustLoc(c.loc), c.recHost, c.recN, local); got != c.want {
			t.Errorf("%s: %q = %v, want %v", c.name, c.loc, got, c.want)
		}
	}
	if (LocalFound{}).IsEntry(mustLoc(`D:\UnravisBlackbox`), "", "", local) {
		t.Error("nothing installed matched")
	}
	// Settings unreadable: no folder, port or name to compare, so nothing is claimed.
	bare := LocalFound{Installed: true, ConfigUnreadable: true}
	if bare.IsEntry(mustLoc("http://127.0.0.1:7788"), "web-host", "web", local) || bare.IsEntry(mustLoc(`D:\UnravisBlackbox`), "", "", local) {
		t.Error("unreadable settings matched")
	}
}
