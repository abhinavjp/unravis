package localci

import (
	"strings"
	"testing"
)

func TestLocalConfigDefaults(t *testing.T) {
	for _, data := range [][]byte{nil, {}, []byte("  \n# only a comment\n")} {
		c, err := ParseLocalConfig(data)
		if err != nil {
			t.Fatal(err)
		}
		cases := map[string]string{
			"ubuntu-24.04":  "host-bash",
			"ubuntu-latest": "host-bash",
			"windows-2022":  "host",
			"windows-2025":  "host",
			"macos-14":      "skip",
			"self-hosted":   "skip",
		}
		for label, want := range cases {
			got, ok := c.Resolve(label)
			if got != want {
				t.Errorf("Resolve(%q) = %q, want %q", label, got, want)
			}
			if wantOK := want != "skip"; ok != wantOK {
				t.Errorf("Resolve(%q) ok = %v, want %v", label, ok, wantOK)
			}
		}
		if o := c.Override("ci.yml", "x"); o.Skip != "" || o.Accepted || len(o.StepEnv) > 0 {
			t.Errorf("override = %+v", o)
		}
	}
}

func TestLocalConfigParse(t *testing.T) {
	src := `
runs-on:
  ubuntu-24.04: host
  windows-2022: host
  macos-14: skip
  custom-label: host-bash
jobs:
  ci.yml:
    desktop-sidecar:
      skip: "macOS entries cannot run on a Windows PC"
      accepted: true
    other:
      skip: why
`
	c, err := ParseLocalConfig([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	for label, want := range map[string]string{"ubuntu-24.04": "host", "macos-14": "skip", "custom-label": "host-bash", "windows-2025": "host"} {
		if got, _ := c.Resolve(label); got != want {
			t.Errorf("Resolve(%q) = %q, want %q", label, got, want)
		}
	}
	if got, ok := c.Resolve("custom-label"); !ok || got != "host-bash" {
		t.Errorf("custom-label: %q %v", got, ok)
	}
	o := c.Override("ci.yml", "desktop-sidecar")
	if o.Skip != "macOS entries cannot run on a Windows PC" || !o.Accepted {
		t.Errorf("override = %+v", o)
	}
	if o := c.Override("ci.yml", "other"); o.Skip != "why" || o.Accepted {
		t.Errorf("other = %+v", o)
	}
	if o := c.Override("ci.yml", "missing"); o.Skip != "" || o.Accepted || len(o.StepEnv) > 0 {
		t.Errorf("missing = %+v", o)
	}
	if o := c.Override("nope.yml", "other"); o.Skip != "" || o.Accepted || len(o.StepEnv) > 0 {
		t.Errorf("nope = %+v", o)
	}
}

func TestLocalConfigStrict(t *testing.T) {
	cases := map[string]struct{ src, want string }{
		"unknown top-level key": {"runs_on:\n  a: host\n", "runs_on"},
		"bad mode":              {"runs-on:\n  ubuntu-24.04: docker\n", "docker"},
		"unknown job key":       {"jobs:\n  ci.yml:\n    j:\n      skipp: x\n", "skipp"},
		"wrong type":            {"jobs:\n  ci.yml:\n    j:\n      accepted: maybe\n", ""},
		"broken yaml":           {"runs-on: [\n", ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseLocalConfig([]byte(tc.src))
			if err == nil {
				t.Fatal("expected an error")
			}
			if tc.want != "" && !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestLocalConfigNilReceiver(t *testing.T) {
	var c *LocalConfig
	if m, ok := c.Resolve("ubuntu-24.04"); m != "host-bash" || !ok {
		t.Errorf("got %q %v", m, ok)
	}
	if o := c.Override("a", "b"); o.Skip != "" || o.Accepted || len(o.StepEnv) > 0 {
		t.Errorf("got %+v", o)
	}
}

func TestLocalConfigStepEnv(t *testing.T) {
	src := `jobs:
  fast-gate.yml:
    lint:
      step-env:
        "Go - lint":
          GOOS: linux
`
	c, err := ParseLocalConfig([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Override("fast-gate.yml", "lint").StepEnv["Go - lint"]["GOOS"]; got != "linux" {
		t.Fatalf("step env override lost: %q", got)
	}
}
