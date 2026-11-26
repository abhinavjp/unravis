package localci

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"

	"go.yaml.in/yaml/v3"
)

// LocalConfig is the content of .github/local-ci.yml: how this PC runs GitHub runner labels and
// per-job overrides.
type LocalConfig struct {
	RunsOn map[string]string
	Jobs   map[string]map[string]JobOverride // workflow file base name -> job id -> override
}

// JobOverride is a per-job local override.
type JobOverride struct {
	Skip     string                       // reason; non-empty means the job is skipped locally
	Accepted bool                         // a skipped job counts as passed for the gate
	StepEnv  map[string]map[string]string // step name -> local-only environment for that step (wins over the workflow's)
	// Exclusive names a resource of this PC that only one job instance may use at a time, across the jobs of every run
	// this program has going (for example a per-user named object that two parallel copies would fight over).
	Exclusive string
}

type rawLocalConfig struct {
	RunsOn map[string]string                `yaml:"runs-on"`
	Jobs   map[string]map[string]rawJobOver `yaml:"jobs"`
}

type rawJobOver struct {
	Skip      string                       `yaml:"skip"`
	Accepted  bool                         `yaml:"accepted"`
	StepEnv   map[string]map[string]string `yaml:"step-env"`
	Exclusive string                       `yaml:"exclusive"`
}

// ParseLocalConfig parses the overrides file strictly: unknown keys are an error, and runs-on
// values must be host, host-bash or skip. Empty data returns the defaults.
func ParseLocalConfig(data []byte) (*LocalConfig, error) {
	c := &LocalConfig{RunsOn: map[string]string{}, Jobs: map[string]map[string]JobOverride{}}
	if len(bytes.TrimSpace(data)) == 0 {
		return c, nil
	}
	var raw rawLocalConfig
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&raw); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("local-ci.yml: %w", err)
	}
	for label, mode := range raw.RunsOn {
		switch mode {
		case "host", "host-bash", "skip":
			c.RunsOn[label] = mode
		default:
			return nil, fmt.Errorf("local-ci.yml: runs-on %q: invalid value %q (want host, host-bash or skip)", label, mode)
		}
	}
	for file, jobs := range raw.Jobs {
		c.Jobs[file] = map[string]JobOverride{}
		for id, o := range jobs {
			c.Jobs[file][id] = JobOverride(o)
		}
	}
	return c, nil
}

// Resolve maps a runner label to a local mode (host, host-bash or skip). A configured label wins;
// otherwise ubuntu-* is host-bash and windows-* is host. ok is false when the label is neither
// configured nor covered by those defaults; the mode is then "skip".
func (c *LocalConfig) Resolve(label string) (mode string, ok bool) {
	if c != nil {
		if m, found := c.RunsOn[label]; found {
			return m, true
		}
	}
	switch {
	case strings.HasPrefix(label, "ubuntu-"):
		return "host-bash", true
	case strings.HasPrefix(label, "windows-"):
		return "host", true
	}
	return "skip", false
}

// Override returns the override for a job of a workflow file (base name), or the zero value.
func (c *LocalConfig) Override(file, job string) JobOverride {
	if c == nil {
		return JobOverride{}
	}
	return c.Jobs[file][job]
}
