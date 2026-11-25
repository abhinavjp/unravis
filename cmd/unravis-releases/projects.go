package main

import (
	"os"
	"path/filepath"

	"api-audit-explorer/internal/releasedash"
)

// defaultCIStore is where unravis-ci keeps its store when it is not told otherwise (the same place as its own default).
func defaultCIStore(getenv func(string) string) string {
	base := getenv("LOCALAPPDATA")
	if base == "" {
		base = filepath.Join(os.TempDir(), "Unravis-local")
	}
	return filepath.Join(base, "Unravis", "ci", "store")
}

// defaultProject is the one project this program has always shown: Unravis, with the rc and stable channels, the seven steps
// of Release.ps1 and the two branches people merge into. The screens read these labels from the project record, so a second
// project can bring its own through --projects without a code change.
func defaultProject(ciStore string) releasedash.Project {
	return releasedash.Project{
		ID:       "unravis",
		Name:     "Unravis",
		Repo:     "abhinavjp/unravis",
		CIStore:  ciStore,
		Channels: append([]string(nil), releasedash.DefaultChannels...),
		Steps:    append([]string(nil), releasedash.DefaultSteps...),
		Branches: []string{"develop", "master"},
	}
}

// projectList is the project record the dashboard runs with. A --projects file wins; without one the default project is
// used, with --ci-store (when given) as its store folder.
func projectList(file, ciStore string, getenv func(string) string) ([]releasedash.Project, error) {
	if file != "" {
		list, err := releasedash.LoadProjects(file)
		if err != nil {
			return nil, err
		}
		if ciStore != "" {
			list[0].CIStore = ciStore
		}
		return list, nil
	}
	if ciStore == "" {
		ciStore = defaultCIStore(getenv)
	}
	p := defaultProject(ciStore)
	if err := p.Normalize(); err != nil {
		return nil, err
	}
	return []releasedash.Project{p}, nil
}
