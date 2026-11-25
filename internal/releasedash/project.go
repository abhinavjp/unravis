package releasedash

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Project is one entry of the per-project config record: everything the screens used to hard-code (the channel names, the
// release steps) and where this project's runs live. The dashboard reads it from a JSON file (--projects) or uses the
// default of the program that started it. The release code paths still read one release data folder; the record only
// names and labels what they show.
type Project struct {
	ID   string `json:"id"`   // short, used in links ("unravis")
	Name string `json:"name"` // shown in the project switcher
	Repo string `json:"repo,omitempty"`
	// CIStore is the folder of the local CI store (the one unravis-ci writes). Empty: this project has no runs here.
	CIStore string `json:"ciStore,omitempty"`
	// Releases is false for a project that makes no releases: its Releases tab and channel boxes are hidden. Missing means true.
	Releases *bool `json:"releases,omitempty"`
	// Channels are the release channels, the preview one first ("rc", "stable"). One or two.
	Channels []string `json:"channels,omitempty"`
	// Steps are the labels of the release steps, in order.
	Steps []string `json:"steps,omitempty"`
	// Branches are the branches the Overview shows the latest run of, first the ones people merge into.
	Branches []string `json:"branches,omitempty"`
	// ShadowGate is how many pull requests in a row must agree with GitHub before the local result may replace it (0: 20).
	ShadowGate int `json:"shadowGate,omitempty"`
}

// HasReleases says whether the project makes releases.
func (p Project) HasReleases() bool { return p.Releases == nil || *p.Releases }

// DefaultSteps are the steps Release.ps1 works through, in order. They label the same seven events as StageNames.
var DefaultSteps = []string{"Preflight", "Tag", "Build", "Sign", "Publish", "Feed", "Verify"}

// DefaultChannels are the release channels the dashboard has always shown.
var DefaultChannels = []string{"rc", "stable"}

var projectIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)

// Normalize fills the blanks with the defaults and checks the record.
func (p *Project) Normalize() error {
	p.ID = strings.TrimSpace(p.ID)
	if !projectIDRe.MatchString(p.ID) {
		return fmt.Errorf("project id %q must be 1 to 40 characters: lower case letters, digits and dashes", p.ID)
	}
	if strings.TrimSpace(p.Name) == "" {
		p.Name = p.ID
	}
	if len(p.Channels) == 0 {
		p.Channels = append([]string(nil), DefaultChannels...)
	}
	if len(p.Channels) > 2 {
		return fmt.Errorf("project %s: at most two channels are shown", p.ID)
	}
	for _, c := range p.Channels {
		if c == "" || len(c) > 20 {
			return fmt.Errorf("project %s: a channel name must be 1 to 20 characters", p.ID)
		}
	}
	if len(p.Steps) == 0 {
		p.Steps = append([]string(nil), DefaultSteps...)
	}
	if p.ShadowGate <= 0 {
		p.ShadowGate = 20
	}
	return nil
}

// LoadProjects reads a JSON file: either {"projects":[...]} or a bare list. The first project is the default one.
func LoadProjects(path string) ([]Project, error) {
	b, err := os.ReadFile(path) //nolint:gosec // the path is a flag the owner typed
	if err != nil {
		return nil, err
	}
	var wrapped struct {
		Projects []Project `json:"projects"`
	}
	list := wrapped.Projects
	if err := json.Unmarshal(b, &wrapped); err == nil && wrapped.Projects != nil {
		list = wrapped.Projects
	} else if err := json.Unmarshal(b, &list); err != nil {
		return nil, fmt.Errorf("%s is not a project list: %w", path, err)
	}
	return NormalizeProjects(list)
}

// NormalizeProjects checks a list: at least one project, unique ids.
func NormalizeProjects(list []Project) ([]Project, error) {
	if len(list) == 0 {
		return nil, errors.New("the project list is empty")
	}
	seen := map[string]bool{}
	for i := range list {
		if err := list[i].Normalize(); err != nil {
			return nil, err
		}
		if seen[list[i].ID] {
			return nil, fmt.Errorf("project id %q is used twice", list[i].ID)
		}
		seen[list[i].ID] = true
	}
	return list, nil
}

// projects is the configured list, or one project with today's defaults when none was given (so a Dash made without a
// record shows what it always did).
func (d *Dash) projects() []Project {
	if len(d.Projects) > 0 {
		return d.Projects
	}
	p := Project{ID: "default", Name: "Releases"}
	_ = p.Normalize()
	return []Project{p}
}

// project finds one by id; an empty id means the first.
func (d *Dash) project(id string) (Project, bool) {
	ps := d.projects()
	if id == "" {
		return ps[0], true
	}
	for _, p := range ps {
		if p.ID == id {
			return p, true
		}
	}
	return Project{}, false
}
