package hosts

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"api-audit-explorer/internal/importer"
)

const sentinel = "SENTINEL-7f3a"

func loadFile(t *testing.T, name string) *Map {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	m, err := Load(f)
	if err != nil {
		t.Fatalf("Load(%s): %v", name, err)
	}
	return m
}

func TestLoadGoodAndLookup(t *testing.T) {
	m := loadFile(t, "good.json")
	if m.Len() != 3 {
		t.Fatalf("Len = %d", m.Len())
	}
	if got := strings.Join(m.Labels(), ","); got != "mongo-a,sql-a,web-a" {
		t.Fatalf("Labels = %s", got)
	}

	for _, name := range []string{"web-a", "WEB-A", "Web-A", "web-a.example.test", "WEB-A.EXAMPLE.TEST", "web-a.example.test.", "  web-a  "} {
		h, ok := m.Lookup(name)
		if !ok {
			t.Fatalf("Lookup(%q) missed", name)
		}
		if h.Label != "web-a" || h.Role != "iis" || h.LogicalCores != 8 || h.Runtime != "iis-10" {
			t.Fatalf("hint = %+v", h)
		}
		if h.TZ == nil || h.TZ.String() != "Pacific/Auckland" {
			t.Fatalf("TZ = %v", h.TZ)
		}
	}

	// A Windows zone ID maps through clock.WindowsZone.
	h, ok := m.Lookup("sql-a.example.test")
	if !ok || h.TZ == nil || h.TZ.String() != "Pacific/Auckland" || h.LogicalCores != 16 {
		t.Fatalf("sql-a = %+v ok=%v", h, ok)
	}

	// Optional fields stay at their zero values: no TZ means host-local fields are refused.
	h, ok = m.Lookup("mongo-a")
	if !ok || h.TZ != nil || h.LogicalCores != 0 || h.Role != "mongo" {
		t.Fatalf("mongo-a = %+v ok=%v", h, ok)
	}
}

func TestUnknownHost(t *testing.T) {
	m := loadFile(t, "good.json")
	for _, name := range []string{"", "web-b", "web-a.example.test.evil", "web", sentinel + ".example.test"} {
		if _, ok := m.Lookup(name); ok {
			t.Fatalf("Lookup(%q) hit", name)
		}
		_, err := m.Resolve(name)
		if !errors.Is(err, ErrUnknownHost) || !errors.Is(err, importer.ErrUnknownHost) {
			t.Fatalf("Resolve(%q) err = %v", name, err)
		}
		if err.Error() != "unknown host" {
			t.Fatalf("error text = %q", err.Error())
		}
	}
	if _, err := m.Resolve("WEB-A"); err != nil {
		t.Fatal(err)
	}
}

func TestEmptyMapShipsEmpty(t *testing.T) {
	m := loadFile(t, "empty.json")
	if m.Len() != 0 || len(m.Labels()) != 0 {
		t.Fatalf("empty map has hosts: %v", m.Labels())
	}
	if _, err := m.Resolve("web-a"); !errors.Is(err, ErrUnknownHost) {
		t.Fatalf("err = %v", err)
	}
	var nilMap *Map
	if _, ok := nilMap.Lookup("web-a"); ok || nilMap.Len() != 0 || nilMap.Labels() == nil {
		t.Fatal("nil map must behave as empty")
	}
}

func TestMarshalRoundTrip(t *testing.T) {
	m := loadFile(t, "good.json")
	data, err := m.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	again, err := Load(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	again2, _ := again.Marshal()
	if !bytes.Equal(data, again2) {
		t.Fatal("Marshal is not stable across Load")
	}
	if h, ok := again.Lookup("sql-a"); !ok || h.LogicalCores != 16 || h.TZ == nil {
		t.Fatalf("round trip lost fields: %+v", h)
	}
	emptyJSON, _ := (*Map)(nil).Marshal()
	if string(emptyJSON) != `{"version":1,"hosts":[]}` {
		t.Fatalf("empty marshal = %s", emptyJSON)
	}
}

func host(extra string) string {
	return `{"version":1,"hosts":[{"label":"web-a"` + extra + `}]}`
}

func TestLoadRefusals(t *testing.T) {
	manyHosts := func(n int) string {
		var b strings.Builder
		b.WriteString(`{"version":1,"hosts":[`)
		for i := 0; i < n; i++ {
			if i > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, `{"label":"h%d"}`, i)
		}
		b.WriteString(`]}`)
		return b.String()
	}
	cases := []struct {
		name, doc string
	}{
		{"empty input", ``},
		{"not json", `hello`},
		{"truncated", `{"version":1,"hosts":[`},
		{"version missing", `{"hosts":[]}`},
		{"version 0", `{"version":0,"hosts":[]}`},
		{"version 2", `{"version":2,"hosts":[]}`},
		{"unknown top field", `{"version":1,"hosts":[],"` + sentinel + `":1}`},
		{"unknown host field", host(`,"` + sentinel + `":"x"`)},
		{"trailing data", `{"version":1,"hosts":[]} {}`},
		{"trailing garbage", `{"version":1,"hosts":[]}x`},
		{"hosts wrong type", `{"version":1,"hosts":{}}`},
		{"empty label", `{"version":1,"hosts":[{"label":""}]}`},
		{"missing label", `{"version":1,"hosts":[{}]}`},
		{"bad label chars", `{"version":1,"hosts":[{"label":"web a"}]}`},
		{"label with slash", `{"version":1,"hosts":[{"label":"../web"}]}`},
		{"label too long", `{"version":1,"hosts":[{"label":"` + strings.Repeat("a", 254) + `"}]}`},
		{"empty alias", host(`,"aliases":[""]`)},
		{"bad alias chars", host(`,"aliases":["a b"]`)},
		{"too many aliases", host(`,"aliases":[` + strings.TrimSuffix(strings.Repeat(`"x",`, 40), ",") + `]`)},
		{"duplicate label", `{"version":1,"hosts":[{"label":"web-a"},{"label":"web-a"}]}`},
		{"duplicate label other case", `{"version":1,"hosts":[{"label":"web-a"},{"label":"WEB-A"}]}`},
		{"alias equals other label", `{"version":1,"hosts":[{"label":"web-a"},{"label":"web-b","aliases":["WEB-A"]}]}`},
		{"alias shared across hosts", `{"version":1,"hosts":[{"label":"web-a","aliases":["x.example.test"]},{"label":"web-b","aliases":["X.EXAMPLE.TEST"]}]}`},
		{"duplicate alias same host", host(`,"aliases":["x.example.test","X.example.test"]`)},
		{"bad role", host(`,"role":"db"`)},
		{"bad tz", host(`,"tz":"Mars/Olympus"`)},
		{"tz Local", host(`,"tz":"Local"`)},
		{"tz traversal", host(`,"tz":"../../etc/passwd"`)},
		{"tz unknown windows id", host(`,"tz":"Synthetic Standard Time"`)},
		{"tz too long", host(`,"tz":"` + strings.Repeat("a", 65) + `"`)},
		{"tz wrong type", host(`,"tz":5`)},
		{"cores 0", host(`,"logicalCores":0`)},
		{"cores negative", host(`,"logicalCores":-1`)},
		{"cores 1025", host(`,"logicalCores":1025`)},
		{"cores fractional", host(`,"logicalCores":1.5`)},
		{"cores string", host(`,"logicalCores":"8"`)},
		{"bad runtime", host(`,"runtime":"IIS 10"`)},
		{"runtime too long", host(`,"runtime":"` + strings.Repeat("a", 33) + `"`)},
		{"201 hosts", manyHosts(201)},
		{"file over 64 KiB", `{"version":1,"hosts":[],"pad":"` + strings.Repeat("a", MaxFileBytes) + `"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, err := Load(strings.NewReader(c.doc))
			if err == nil {
				t.Fatalf("accepted: %d hosts", m.Len())
			}
			if m != nil {
				t.Fatal("a refused file must return a nil map")
			}
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("error does not wrap ErrInvalid: %v", err)
			}
			if strings.Contains(err.Error(), sentinel) {
				t.Fatalf("error echoes input: %v", err)
			}
		})
	}
}

func TestLoadBoundaries(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"version":1,"hosts":[`)
	for i := 0; i < MaxHosts; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"label":"h%d"}`, i)
	}
	b.WriteString(`]}`)
	m, err := Load(strings.NewReader(b.String()))
	if err != nil || m.Len() != MaxHosts {
		t.Fatalf("200 hosts: %v", err)
	}

	for _, cores := range []int{1, 1024} {
		if _, err := Load(strings.NewReader(host(fmt.Sprintf(`,"logicalCores":%d`, cores)))); err != nil {
			t.Fatalf("cores %d: %v", cores, err)
		}
	}

	// Exactly at the byte cap is accepted; one byte over is refused.
	doc := `{"version":1,"hosts":[]}`
	pad := MaxFileBytes - len(doc)
	atCap := doc + strings.Repeat(" ", pad)
	if _, err := Load(strings.NewReader(atCap)); err != nil {
		t.Fatalf("at cap: %v", err)
	}
	if _, err := Load(strings.NewReader(atCap + " ")); err == nil {
		t.Fatal("one byte over the cap accepted")
	}
}

func TestOversizeInputIsNotFullyRead(t *testing.T) {
	r := &countingReader{}
	if _, err := Load(r); err == nil {
		t.Fatal("endless input accepted")
	}
	if r.n > MaxFileBytes+4096 {
		t.Fatalf("read %d bytes of an endless stream", r.n)
	}
}

type countingReader struct{ n int }

func (c *countingReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = ' '
	}
	c.n += len(p)
	return len(p), nil
}

func TestLoadNilReader(t *testing.T) {
	if _, err := Load(nil); err == nil {
		t.Fatal("nil reader accepted")
	}
}

func TestSharedLabelAndOwnAliasAllowed(t *testing.T) {
	// The documented example lists WEB-A as an alias of web-a.
	m, err := Load(strings.NewReader(`{"version":1,"hosts":[{"label":"web-a","aliases":["WEB-A"]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if h, ok := m.Lookup("Web-A"); !ok || h.Label != "web-a" {
		t.Fatalf("hint = %+v", h)
	}
}

func TestTestdataIsSynthetic(t *testing.T) {
	ip := regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`)
	dotted := regexp.MustCompile(`"([A-Za-z0-9_-]+(?:\.[A-Za-z0-9_-]+)+)"`)
	entries, err := os.ReadDir("testdata")
	if err != nil || len(entries) == 0 {
		t.Fatal("no testdata")
	}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join("testdata", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if ip.Match(data) {
			t.Fatalf("%s holds an IP address", e.Name())
		}
		for _, m := range dotted.FindAllSubmatch(data, -1) {
			name := strings.ToLower(string(m[1]))
			if strings.HasSuffix(name, ".example.test") || regexp.MustCompile(`^[a-z]+-[0-9.]+$`).MatchString(name) {
				continue
			}
			t.Fatalf("%s holds a host outside .example.test: %s", e.Name(), name)
		}
	}
}
