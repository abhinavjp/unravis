package cmdtree

import (
	"context"
	"net"

	"api-audit-explorer/internal/clicore"
	"os"
	"strings"
	"testing"

	"api-audit-explorer/internal/locator"
)

func TestDoctorWithTheAppRunning(t *testing.T) {
	o := newOwner(t, nil)
	d := data(t, o.run("doctor", "-o", "json"))
	loc, be := d["locator"].(map[string]any), d["backend"].(map[string]any)
	if loc["state"] != "running" || loc["owner"] != "app" || loc["pid"] != float64(4242) || loc["appVersion"] != "1.2.3" {
		t.Errorf("locator: %v", loc)
	}
	if be["reachable"] != true || be["state"] != "ready" || be["proto"].(map[string]any)["major"] != float64(1) {
		t.Errorf("backend: %v", be)
	}
	if d["cli"].(map[string]any)["role"] != "cli" || d["installDir"] != "/opt/unravis" || d["mcpCommand"] != "/opt/unravis/unravis.exe --mcp" {
		t.Errorf("paths: %v", d)
	}
	if d["appExe"].(map[string]any)["path"] != "/opt/unravis/unravis.exe" || d["appExe"].(map[string]any)["exists"] != false {
		t.Errorf("appExe: %v", d["appExe"])
	}
	if len(o.opsSeen()) != 0 {
		t.Error("doctor sends no operation")
	}
	tb := o.run("doctor")
	if tb.code != 0 || !strings.Contains(tb.out, "app answered") || !strings.Contains(tb.out, "ready") {
		t.Errorf("table: %+v", tb)
	}
}

func TestDoctorStatesWithoutABackend(t *testing.T) {
	// locked and updating are answers of hello
	for _, state := range []string{"locked", "updating"} {
		o := newOwner(t, nil)
		o.set(func(o *owner) { o.state = state })
		d := data(t, o.run("doctor", "--json"))
		if d["backend"].(map[string]any)["state"] != state || d["backend"].(map[string]any)["reachable"] != true {
			t.Errorf("%s: %v", state, d["backend"])
		}
	}
	// not running, stale, a newer locator, an owner that does not prove who it is
	o := newOwner(t, nil)
	o.set(func(o *owner) { o.alive = false })
	if d := data(t, o.run("doctor", "--json")); d["locator"].(map[string]any)["state"] != "stale" || d["backend"].(map[string]any)["reachable"] != false {
		t.Errorf("stale: %v", d)
	}
	_ = os.Remove(locator.FilePath(o.runDir, false))
	if d := data(t, o.run("doctor", "--json")); d["locator"].(map[string]any)["state"] != "not_running" {
		t.Errorf("not running: %v", d)
	}
	o = newOwner(t, nil)
	p := locator.FilePath(o.runDir, false)
	b, _ := os.ReadFile(p)                                                                          //nolint:gosec // a file of this test
	_ = os.WriteFile(p, []byte(strings.Replace(string(b), `"version":2`, `"version":9`, 1)), 0o600) //nolint:gosec // a file of this test
	d := data(t, o.run("doctor", "--json"))
	if d["locator"].(map[string]any)["state"] != "unusable_version" || d["backend"].(map[string]any)["error"] != "VERSION_INCOMPATIBLE" {
		t.Errorf("newer locator: %v", d)
	}
	o = newOwner(t, nil)
	o.set(func(o *owner) { o.helloPID = 7 })
	d = data(t, o.run("doctor", "--json"))
	if d["locator"].(map[string]any)["state"] != "unknown" || d["backend"].(map[string]any)["error"] != "ENDPOINT_UNTRUSTED" || d["backend"].(map[string]any)["reachable"] != false {
		t.Errorf("untrusted: %v", d)
	}
}

// Doctor shows no token, no pipe name and no case title, in data, in the table or in errors.
func TestDoctorNoSecrets(t *testing.T) {
	o := newOwner(t, nil)
	for _, mode := range [][]string{{"doctor", "--json"}, {"doctor"}} {
		r := o.run(mode...)
		for _, bad := range []string{ownerToken, "unravis.ag", "unravis.ui", "pipe", "0123456789abcdef0123456789abcdef", `\\.\`} {
			if strings.Contains(r.out, bad) || strings.Contains(r.err, bad) {
				t.Errorf("%v shows %q: %s", mode, bad, r.out)
			}
		}
	}
	if r := o.run("doctor", "--request", "-"); r.code != 2 {
		t.Errorf("doctor takes no request: %+v", r)
	}
}

func TestDoctorStopsWhenTheCommandIsStopped(t *testing.T) {
	o := newOwner(t, nil)
	r := runOn(t, withDialBlock(o.attacher()), "", nil, "doctor", "--timeout", "100ms", "--json")
	if r.code != 6 || errCode(t, r) != "TIMEOUT" {
		t.Errorf("%+v", r)
	}
}

// withDialBlock makes every dial wait for the end of the context.
func withDialBlock(a clicore.Attacher) clicore.Attacher {
	a.Dial = func(ctx context.Context, _ locator.Record) (net.Conn, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return a
}
