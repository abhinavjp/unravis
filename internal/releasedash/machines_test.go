package releasedash

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/localci"
)

func saveFleet(t *testing.T, dir string, v localci.FleetView) {
	t.Helper()
	b, err := json.Marshal(v)
	must(t, err)
	writeFile(t, localci.FleetViewPath(dir), string(b))
}

func TestMachinesWithNothingSaved(t *testing.T) {
	f := newCIFixture(t)
	m := decode[CIMachines](t, f.get(t, "/api/machines"))
	if !m.CIFound || m.Show || m.Seen || len(m.Machines) != 0 {
		t.Fatalf("no list saved: the tab stays hidden: %+v", m)
	}
	if o := decode[CIOverview](t, f.get(t, "/api/overview")); o.Machines {
		t.Fatalf("the overview must not ask for the tab: %+v", o.Machines)
	}
}

func TestMachinesAreShownByOpaqueIDWithTheStateWords(t *testing.T) {
	f := newCIFixture(t)
	now := f.now
	saveFleet(t, f.dir, localci.FleetView{Updated: now, Repo: "o/r", Self: "m-aaaa1111", Agents: []localci.FleetAgent{
		{ID: "m-zzzz9999", Labels: []string{"gpu"}, State: "ready", LastHeard: now.Add(-20 * time.Second)},
		{ID: "m-aaaa1111", Labels: []string{"windows", fxTokenSecret}, State: "ready", Slots: 2, LastHeard: now.Add(-5 * time.Second),
			Jobs: []localci.FleetJob{{Item: "pr 12 into develop", Since: now.Add(-time.Minute)}}},
		{ID: "m-bbbb2222", State: "ready", LastHeard: now.Add(-30 * time.Minute)},
		{ID: "m-cccc3333", State: "stopped", LastHeard: now.Add(-time.Hour)},
		{ID: "m-dddd4444", State: "paused", LastHeard: now.Add(-10 * time.Second)},
	}})
	m := decode[CIMachines](t, f.get(t, "/api/machines"))
	if !m.Show || !m.Seen || len(m.Machines) != 5 || m.Stale != "" {
		t.Fatalf("%+v", m)
	}
	if m.Machines[0].ID != "m-aaaa1111" || !m.Machines[0].Self {
		t.Fatalf("this PC comes first: %+v", m.Machines[0])
	}
	state := map[string]CIMachine{}
	for _, x := range m.Machines {
		state[x.ID] = x
	}
	for id, want := range map[string]string{"m-aaaa1111": "Running", "m-zzzz9999": "Idle", "m-bbbb2222": "Quiet", "m-cccc3333": "Stopped", "m-dddd4444": "Idle"} {
		if state[id].State != want {
			t.Errorf("%s: %q, want %q (%+v)", id, state[id].State, want, state[id])
		}
	}
	if len(state["m-aaaa1111"].Jobs) != 1 || state["m-aaaa1111"].Jobs[0].Label != "pr 12 into develop" {
		t.Errorf("current run: %+v", state["m-aaaa1111"].Jobs)
	}
	if state["m-bbbb2222"].Silent == "" || state["m-dddd4444"].Note == "" {
		t.Errorf("notes: %+v %+v", state["m-bbbb2222"], state["m-dddd4444"])
	}
	body := f.get(t, "/api/machines").Body.String()
	if strings.Contains(body, fxTokenSecret) {
		t.Fatalf("a secret in a label reached the page: %s", body)
	}
	if o := decode[CIOverview](t, f.get(t, "/api/overview")); !o.Machines {
		t.Fatal("the overview must say the tab is shown")
	}
}

func TestMachinesTabNeedsTwoPCsOrTheProjectRecord(t *testing.T) {
	f := newCIFixture(t)
	one := localci.FleetView{Updated: f.now, Self: "m-aaaa1111", Agents: []localci.FleetAgent{{ID: "m-aaaa1111", State: "ready", LastHeard: f.now}}}
	saveFleet(t, f.dir, one)
	if m := decode[CIMachines](t, f.get(t, "/api/machines")); m.Show || len(m.Machines) != 1 {
		t.Fatalf("one PC is no fleet: %+v", m)
	}
	f.d.Projects[0].Machines = true
	if m := decode[CIMachines](t, f.get(t, "/api/machines")); !m.Show || !m.Enabled {
		t.Fatalf("the project record asks for it: %+v", m)
	}
}

func TestMachinesSaysWhenTheListIsOld(t *testing.T) {
	f := newCIFixture(t)
	old := f.now.Add(-2 * time.Hour)
	saveFleet(t, f.dir, localci.FleetView{Updated: old, Self: "m-aaaa1111", Agents: []localci.FleetAgent{
		{ID: "m-aaaa1111", State: "ready", LastHeard: old}, {ID: "m-bbbb2222", State: "ready", LastHeard: old}}})
	m := decode[CIMachines](t, f.get(t, "/api/machines"))
	if m.Stale == "" {
		t.Fatalf("an old list must say so: %+v", m)
	}
}

func TestMachinesIsReadOnlyAndNeedsAKnownProject(t *testing.T) {
	f := newCIFixture(t)
	if r := f.get(t, "/api/machines?project=nope"); r.Code != http.StatusNotFound {
		t.Fatalf("unknown project: %d", r.Code)
	}
	if r := f.do(t, http.MethodPost, "127.0.0.1:80", "/api/machines", "tok"); r.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST must be refused: %d", r.Code)
	}
}
