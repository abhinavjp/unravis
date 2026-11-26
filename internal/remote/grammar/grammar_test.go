package grammar

import (
	"encoding/json"
	"os"
	"testing"
)

type vec struct {
	Fn      string   `json:"fn"`
	In      string   `json:"in"`
	Allowed []string `json:"allowed"`
	Ok      bool     `json:"ok"`
}

func TestVectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct{ Cases []vec }
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Cases) < 40 {
		t.Fatalf("vectors file looks truncated: %d cases", len(f.Cases))
	}
	for _, c := range f.Cases {
		var reason string
		switch c.Fn {
		case "Host":
			reason = Host(c.In)
		case "User":
			reason = User(c.In)
		case "Fingerprint":
			reason = Fingerprint(c.In)
		case "MinionID":
			reason = MinionID(c.In)
		case "SafeWord":
			reason = SafeWord(c.In)
		case "SafePattern":
			reason = SafePattern(c.In)
		case "YamlSafe":
			reason = YamlSafe(c.In)
		case "PathCharset":
			reason = PathCharset(c.In)
		case "PathAllowed":
			reason = PathAllowed(c.In, c.Allowed)
		case "ReadOnlyCommand":
			reason = ReadOnlyCommand(c.In, c.Allowed)
		default:
			t.Fatalf("unknown fn %q", c.Fn)
		}
		if (reason == "") != c.Ok {
			t.Errorf("%s(%q, %v): allowed=%v, want %v (%s)", c.Fn, c.In, c.Allowed, reason == "", c.Ok, reason)
		}
	}
}

func TestPort(t *testing.T) {
	for n, ok := range map[int]bool{0: false, 1: true, 22: true, 65535: true, 65536: false, -1: false} {
		if (Port(n) == "") != ok {
			t.Errorf("Port(%d)", n)
		}
	}
}
