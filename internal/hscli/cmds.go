package hscli

import (
	"encoding/json"
	"fmt"
	"strings"

	"api-audit-explorer/internal/cicli"
)

// defaultProject is the one project whose CI store this PC knows without being told where it is.
const defaultProject = "unravis"

// cmdStatus is `status`: the engine's status (the queue, the machines, what ran last). --project says which project
// to look at; only the default project has a store the command can find by itself, so another one needs --state-dir.
func cmdStatus(c *run, args []string) int {
	project, rest, err := takeFlag(args, "project")
	if err != nil {
		return c.usageErr("status: %v", err)
	}
	if project != "" && project != defaultProject && !hasStateDir(rest) {
		return c.usageErr("status: this PC knows the CI store of the project %q only; for %q give its store folder with --state-dir", defaultProject, project)
	}
	return c.engine(append([]string{"status"}, stateFlag(rest)...)...)
}

func hasStateDir(args []string) bool {
	for _, a := range args {
		if a == "--state-dir" || a == "-state-dir" || a == "--state" || a == "-state" ||
			strings.HasPrefix(a, "--state-dir=") || strings.HasPrefix(a, "--state=") {
			return true
		}
	}
	return false
}

// cmdMachinesDrain is `machines drain <name>`: the engine's drain, which asks for one machine by --name.
func cmdMachinesDrain(c *run, args []string) int {
	pos, flags := positionals(args, "state-dir", "state")
	if len(pos) != 1 {
		return c.usageErr("machines drain: give one machine name (see: %s machines list)", c.prog)
	}
	return c.engine(append([]string{"drain", "--name", pos[0]}, stateFlag(flags)...)...)
}

// cmdCI is `ci ...`: the engine's own commands, unchanged.
func cmdCI(c *run, args []string) int {
	return c.engine(args...)
}

// versionJSON is `version --json`. Add fields, do not rename or drop them.
type versionJSON struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Engine  string `json:"engine"`
}

func cmdVersion(c *run, args []string) int {
	asJSON := false
	for _, a := range args {
		if a != "--json" {
			return c.usageErr("version: takes no arguments except --json")
		}
		asJSON = true
	}
	if asJSON {
		b, _ := json.MarshalIndent(versionJSON{Name: "homestretch", Version: Version, Engine: cicli.Version()}, "", "  ")
		_, _ = fmt.Fprintln(c.out, string(b))
		return ExitOK
	}
	_, _ = fmt.Fprintf(c.out, "%s %s (engine %s)\n", c.prog, Version, cicli.Version())
	return ExitOK
}
