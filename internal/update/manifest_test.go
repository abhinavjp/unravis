package update

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

func h(c string) string { return strings.Repeat(c, 64) }

func goodManifest(version, channel string) Manifest {
	return Manifest{
		Schema: 1, Product: "unravis", Version: version, Channel: channel,
		Published: now.Add(-time.Hour), Expires: now.Add(24 * time.Hour), Seq: 48,
		Assets: map[string]Asset{
			"setup":    {Name: "unravis-setup-v" + version + "-windows-x64.exe", SHA256: h("a"), Size: 100},
			"app":      {Name: "unravis-v" + version + "-windows-x64.zip", SHA256: h("b"), Size: 100},
			"blackbox": {Name: "unravis-blackbox-v" + version + "-windows-x64.zip", SHA256: h("c"), Size: 100},
		},
		BlackboxExeSHA256: h("d"),
		Commit:            strings.Repeat("e", 40),
	}
}

func key(t *testing.T, role string) (Key, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return Key{Role: role, Pub: pub}, priv
}

func sign(t *testing.T, m Manifest, k Key, priv ed25519.PrivateKey) ([]byte, []byte) {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b, []byte(FormatSignature(k.Pub, ed25519.Sign(priv, b)))
}

func TestVerifyGood(t *testing.T) {
	k, priv := key(t, ChannelRC)
	mb, sb := sign(t, goodManifest("0.1.0-rc.48", ChannelRC), k, priv)
	m, err := Verify(mb, sb, Trust{Keys: []Key{k}, Channel: ChannelRC, Want: ChannelRC}, now)
	if err != nil || m.Version != "0.1.0-rc.48" {
		t.Fatalf("got %v %v", m, err)
	}
}

func TestVerifyRefusals(t *testing.T) {
	rc, rcPriv := key(t, ChannelRC)
	st, stPriv := key(t, ChannelStable)
	other, otherPriv := key(t, ChannelStable)
	trust := Trust{Keys: []Key{rc, st}, Channel: ChannelRC, Want: ChannelRC}

	rcM, rcS := sign(t, goodManifest("0.1.0-rc.48", ChannelRC), rc, rcPriv)
	stM, stS := sign(t, goodManifest("1.0.0", ChannelStable), st, stPriv)
	cases := []struct {
		name  string
		mb    []byte
		sb    []byte
		trust Trust
	}{
		{"tampered manifest", []byte(strings.Replace(string(rcM), "rc.48", "rc.49", 1)), rcS, trust},
		{"tampered signature", rcM, flipSig(t, rcS), trust},
		{"unknown key", func() []byte { b, _ := sign(t, goodManifest("1.0.0", ChannelStable), other, otherPriv); return b }(),
			[]byte(FormatSignature(other.Pub, ed25519.Sign(otherPriv, []byte("x")))), trust},
		{"empty signature", rcM, nil, trust},
		{"garbage signature", rcM, []byte("hello"), trust},
		{"stable build refuses rc key", rcM, rcS, Trust{Keys: []Key{rc, st}, Channel: ChannelStable, Want: ChannelRC}},
		{"oversize manifest", []byte(strings.Repeat("x", MaxManifestSize+1)), rcS, trust},
	}
	for _, c := range cases {
		if _, err := Verify(c.mb, c.sb, c.trust, now); err == nil {
			t.Errorf("%s: accepted", c.name)
		}
	}
	// stable manifest signed by the stable key is fine for both builds
	for _, ch := range []string{ChannelStable, ChannelRC} {
		if _, err := Verify(stM, stS, Trust{Keys: []Key{rc, st}, Channel: ch, Want: ChannelStable}, now); err != nil {
			t.Errorf("stable manifest on %s build: %v", ch, err)
		}
	}
}

func TestRoleMustMatchChannel(t *testing.T) {
	rc, rcPriv := key(t, ChannelRC)
	// An rc key signing a stable manifest must be refused even though the signature is valid.
	mb, sb := sign(t, goodManifest("1.0.0", ChannelStable), rc, rcPriv)
	if _, err := Verify(mb, sb, Trust{Keys: []Key{rc}, Channel: ChannelRC, Want: ChannelStable}, now); err == nil {
		t.Fatal("rc key signed a stable manifest")
	}
}

func TestValidateRejects(t *testing.T) {
	mut := func(f func(m *Manifest)) Manifest { m := goodManifest("0.1.0-rc.48", ChannelRC); f(&m); return m }
	bad := map[string]Manifest{
		"schema":            mut(func(m *Manifest) { m.Schema = 2 }),
		"product":           mut(func(m *Manifest) { m.Product = "other" }),
		"version":           mut(func(m *Manifest) { m.Version = "nope" }),
		"channel mismatch":  mut(func(m *Manifest) { m.Channel = ChannelStable }),
		"expired":           mut(func(m *Manifest) { m.Expires = now.Add(-time.Second) }),
		"future":            mut(func(m *Manifest) { m.Published = now.Add(time.Hour) }),
		"seq zero":          mut(func(m *Manifest) { m.Seq = 0 }),
		"seq huge":          mut(func(m *Manifest) { m.Seq = MaxSeq + 1 }),
		"no commit":         mut(func(m *Manifest) { m.Commit = "" }),
		"short commit":      mut(func(m *Manifest) { m.Commit = "abc" }),
		"path in name":      mut(func(m *Manifest) { a := m.Assets["setup"]; a.Name = "../evil.exe"; m.Assets["setup"] = a }),
		"slash in name":     mut(func(m *Manifest) { a := m.Assets["setup"]; a.Name = "a/b.exe"; m.Assets["setup"] = a }),
		"short hash":        mut(func(m *Manifest) { a := m.Assets["app"]; a.SHA256 = "abc"; m.Assets["app"] = a }),
		"upper hash":        mut(func(m *Manifest) { a := m.Assets["app"]; a.SHA256 = strings.ToUpper(h("a")); m.Assets["app"] = a }),
		"zero size":         mut(func(m *Manifest) { a := m.Assets["app"]; a.Size = 0; m.Assets["app"] = a }),
		"huge size":         mut(func(m *Manifest) { a := m.Assets["app"]; a.Size = MaxAssetSize + 1; m.Assets["app"] = a }),
		"missing setup":     mut(func(m *Manifest) { delete(m.Assets, "setup") }),
		"unknown asset":     mut(func(m *Manifest) { m.Assets["extra"] = m.Assets["app"] }),
		"duplicate name":    mut(func(m *Manifest) { m.Assets["app"] = m.Assets["setup"] }),
		"bad blackbox hash": mut(func(m *Manifest) { m.BlackboxExeSHA256 = "" }),
		"bad minUpdateFrom": mut(func(m *Manifest) { m.MinUpdateFrom = "zzz" }),
		"v prefix":          mut(func(m *Manifest) { m.Version = "v0.1.0-rc.48" }),
	}
	for name, m := range bad {
		if err := m.Validate(now); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	good := goodManifest("0.1.0-rc.48", ChannelRC)
	if err := good.Validate(now); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyRejectsUnknownFieldsAndTrailingData(t *testing.T) {
	k, priv := key(t, ChannelRC)
	m := goodManifest("0.1.0-rc.48", ChannelRC)
	b, _ := json.Marshal(m)
	for name, body := range map[string][]byte{
		"unknown field": []byte(strings.Replace(string(b), `{`, `{"url":"http://evil",`, 1)),
		"trailing":      append(append([]byte{}, b...), []byte(`{}`)...),
	} {
		sb := []byte(FormatSignature(k.Pub, ed25519.Sign(priv, body)))
		if _, err := Verify(body, sb, Trust{Keys: []Key{k}, Channel: ChannelRC, Want: ChannelRC}, now); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestJudge(t *testing.T) {
	m := goodManifest("0.1.0-rc.48", ChannelRC)
	cases := []struct {
		name    string
		current string
		lastSeq uint64
		min     string
		want    Decision
	}{
		{"newer rc installs", "0.1.0-rc.47", 47, "", DecisionInstall},
		{"rc.9 to rc.48 numeric order", "0.1.0-rc.9", 0, "", DecisionInstall},
		{"same version", "0.1.0-rc.48", 0, "", DecisionIgnore},
		{"downgrade", "0.1.0-rc.50", 0, "", DecisionIgnore},
		{"final beats rc, never the reverse", "0.1.0", 0, "", DecisionIgnore},
		{"replayed manifest", "0.1.0-rc.47", 49, "", DecisionIgnore},
		{"same seq, newer version", "0.1.0-rc.47", 48, "", DecisionInstall},
		{"older seq", "0.1.0-rc.47", 99, "", DecisionIgnore},
		{"too old for one step", "0.1.0-rc.10", 0, "0.1.0-rc.30", DecisionIgnore},
		{"dev build", "dev", 0, "", DecisionIgnore},
	}
	for _, c := range cases {
		mm := m
		mm.MinUpdateFrom = c.min
		got, _, err := Judge(&mm, c.current, c.lastSeq)
		if err != nil || got != c.want {
			t.Errorf("%s: got %v %v", c.name, got, err)
		}
	}
}

func TestSemverOrder(t *testing.T) {
	order := []string{"0.1.0-rc.2", "0.1.0-rc.10", "0.1.0-rc.10.1", "0.1.0-rcx", "0.1.0", "0.1.1-rc.1", "0.2.0", "1.0.0"}
	for i := range order {
		for j := range order {
			a, _ := ParseVersion(order[i])
			b, _ := ParseVersion(order[j])
			want := 0
			if i < j {
				want = -1
			} else if i > j {
				want = 1
			}
			if got := Compare(a, b); got != want {
				t.Errorf("Compare(%s,%s)=%d want %d", order[i], order[j], got, want)
			}
		}
	}
	for _, bad := range []string{"", "1", "1.2", "1.2.3-RC.1", "01.2.3", "1.2.3+build", "1.2.3-", "1.2.3-rc.01", "v", "1.2.3 4"} {
		if _, err := ParseVersion(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestChannelFailsClosed(t *testing.T) {
	k, priv := key(t, ChannelRC)
	mb, sb := sign(t, goodManifest("0.1.0-rc.48", ChannelRC), k, priv)
	for _, ch := range []string{"", "Stable", "STABLE", "beta"} {
		if _, err := Verify(mb, sb, Trust{Keys: []Key{k}, Channel: ch, Want: ChannelRC}, now); err == nil {
			t.Errorf("build channel %q accepted", ch)
		}
	}
	if _, err := Verify(mb, sb, Trust{Keys: []Key{k}, Channel: ChannelRC, Want: ChannelStable}, now); err == nil {
		t.Error("rc manifest accepted when stable was asked for")
	}
	if _, err := Verify(mb, sb, Trust{Keys: []Key{k}, Channel: ChannelRC}, now); err == nil {
		t.Error("an empty Want was accepted")
	}
	if _, err := Verify(mb, sb, Trust{Keys: []Key{k}, Channel: ChannelRC, Want: ChannelRC}, now); err != nil {
		t.Error(err)
	}
}

func TestStrictJSON(t *testing.T) {
	k, priv := key(t, ChannelRC)
	good, _ := json.Marshal(goodManifest("0.1.0-rc.48", ChannelRC))
	g := string(good)
	bodies := map[string]string{
		"duplicate seq":      strings.Replace(g, `"seq":48`, `"seq":48,"seq":9`, 1),
		"case duplicate":     strings.Replace(g, `"seq":48`, `"seq":48,"SEQ":99`, 1),
		"upper-case key":     strings.Replace(g, `"seq"`, `"SEQ"`, 1),
		"trailing brace":     g + "}",
		"trailing bracket":   g + "]",
		"duplicate in asset": strings.Replace(g, `"size":100`, `"size":100,"size":1`, 1),
	}
	for name, body := range bodies {
		sb := []byte(FormatSignature(k.Pub, ed25519.Sign(priv, []byte(body))))
		if _, err := Verify([]byte(body), sb, Trust{Keys: []Key{k}, Channel: ChannelRC, Want: ChannelRC}, now); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestSaferFileNamesAndLifetime(t *testing.T) {
	for name, mut := range map[string]func(m *Manifest){
		"reserved":     func(m *Manifest) { a := m.Assets["app"]; a.Name = "CON.zip"; m.Assets["app"] = a },
		"trailing dot": func(m *Manifest) { a := m.Assets["app"]; a.Name = "x.exe."; m.Assets["app"] = a },
		"case duplicate": func(m *Manifest) {
			a := m.Assets["app"]
			a.Name = strings.ToUpper(m.Assets["setup"].Name)
			m.Assets["app"] = a
		},
		"long lifetime": func(m *Manifest) { m.Expires = m.Published.Add(500 * 24 * time.Hour) },
		"floor above":   func(m *Manifest) { m.MinUpdateFrom = "9.0.0" },
		"floor spelled": func(m *Manifest) { m.MinUpdateFrom = "v0.1.0-rc.1" },
	} {
		m := goodManifest("0.1.0-rc.48", ChannelRC)
		mut(&m)
		if err := m.Validate(now); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestHugePrereleaseNumbers(t *testing.T) {
	a, _ := ParseVersion("1.0.0-rc.99999999999999999999")
	b, _ := ParseVersion("1.0.0-rc.99999999999999999998")
	if Compare(a, b) != 1 || Compare(b, a) != -1 {
		t.Fatal("huge numbers compare wrongly")
	}
}

// flipSig changes one bit of the decoded signature, so the test does not depend on which letters the base64 holds.
func flipSig(t *testing.T, sigFile []byte) []byte {
	t.Helper()
	id, sig, err := parseSignature(sigFile)
	if err != nil {
		t.Fatal(err)
	}
	sig[10] ^= 1
	return []byte(sigPrefix + " " + id + " " + base64.StdEncoding.EncodeToString(sig) + "\n")
}
