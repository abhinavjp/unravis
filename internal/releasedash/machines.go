package releasedash

import (
	"net/http"
	"sort"

	"api-audit-explorer/internal/localci"
)

// The Machines tab: which PCs check this repository, as the host's agent records say. The page reads one file the
// agent saved in the CI store (fleet/agents.json); it never asks GitHub itself and it changes nothing. Other PCs are
// shown by their opaque id only, with the labels they published.

// machineQuiet is how long a PC may be silent before it is called Quiet. A PC publishes itself about once a minute.
const machineQuiet = 3 * localci.DefaultFleetEvery

// machineSyncStale is how old the saved file may be before the page says this PC is not looking at the others.
const machineSyncStale = 3 * localci.DefaultFleetEvery

// The words of a machine's state. They are the words used for runs elsewhere on the page.
const (
	machRunning = "Running"
	machIdle    = "Idle"
	machQuiet   = "Quiet"
	machStopped = "Stopped"
)

// CIMachine is one PC.
type CIMachine struct {
	ID        string       `json:"id"`
	Self      bool         `json:"self"`
	Labels    []string     `json:"labels"`
	State     string       `json:"state"` // Running, Idle, Quiet or Stopped
	Note      string       `json:"note,omitempty"`
	Jobs      []CIAgentJob `json:"jobs"`
	Slots     int          `json:"slots,omitempty"`
	LastHeard string       `json:"lastHeard,omitempty"`
	Silent    string       `json:"silent,omitempty"`
}

// CIMachines is the answer of /api/machines.
type CIMachines struct {
	CIFound bool `json:"ciFound"`
	// Show says whether the tab is shown: more than one PC has been seen, or the project record asks for it.
	Show     bool        `json:"show"`
	Enabled  bool        `json:"enabled"` // the project record asks for the tab
	Seen     bool        `json:"seen"`    // an agent has saved the list of PCs at all
	Now      string      `json:"now"`
	Updated  string      `json:"updated,omitempty"`
	Stale    string      `json:"stale,omitempty"` // how long ago the list was saved, when that is too long
	Machines []CIMachine `json:"machines"`
}

// Machines reads the saved list of PCs.
func (v *ciView) Machines() CIMachines {
	m := CIMachines{CIFound: v.found, Enabled: v.p.Machines, Now: tstr(v.now), Machines: []CIMachine{}}
	if !v.found {
		m.Show = m.Enabled
		return m
	}
	fv, err := localci.ReadFleetView(v.dir)
	if err != nil || fv == nil {
		m.Show = m.Enabled
		return m
	}
	m.Seen = true
	m.Updated = tstr(fv.Updated)
	if age := v.now.Sub(fv.Updated); age > machineSyncStale {
		m.Stale = ago(age)
	}
	for _, a := range fv.Agents {
		m.Machines = append(m.Machines, v.machineOf(a, fv.Self))
	}
	sort.SliceStable(m.Machines, func(i, j int) bool {
		if m.Machines[i].Self != m.Machines[j].Self {
			return m.Machines[i].Self
		}
		return m.Machines[i].ID < m.Machines[j].ID
	})
	m.Show = m.Enabled || len(m.Machines) > 1
	return m
}

func (v *ciView) machineOf(a localci.FleetAgent, self string) CIMachine {
	m := CIMachine{ID: v.rd(a.ID), Self: a.ID == self, Labels: []string{}, Jobs: []CIAgentJob{}, Slots: a.Slots, LastHeard: tstr(a.LastHeard)}
	for _, l := range a.Labels {
		m.Labels = append(m.Labels, v.rd(l))
	}
	for _, j := range a.Jobs {
		m.Jobs = append(m.Jobs, CIAgentJob{Label: v.rd(j.Item), Since: tstr(j.Since)})
	}
	silent := v.now.Sub(a.LastHeard)
	switch {
	case a.State == "stopped":
		m.State, m.Note = machStopped, "It said it was stopping. It takes no work and holds no claims."
	case silent > machineQuiet:
		m.State, m.Silent = machQuiet, ago(silent)
		m.Note = "Not heard from for " + m.Silent + ". It may be asleep, offline or switched off. If it held a check, another PC takes it over."
	case len(m.Jobs) > 0:
		m.State = machRunning
		if a.State == "draining" {
			m.Note = "Finishing these checks, then stopping. It takes no new work."
		}
	default:
		m.State = machIdle
		switch a.State {
		case "paused":
			m.Note = "Paused: it takes no new work until it is resumed."
		case "draining":
			m.Note = "Stopping: it takes no new work."
		}
	}
	return m
}

func (d *Dash) registerMachines(mux *http.ServeMux, view func(http.ResponseWriter, *http.Request) (*ciView, bool)) {
	mux.HandleFunc("/api/machines", func(w http.ResponseWriter, r *http.Request) {
		if v, ok := view(w, r); ok {
			writeJSON(w, v.Machines())
		}
	})
}
