package localci

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Machine policy: per project (repository), which machines may run its requests. Labels still decide whether a
// machine fits a request; the policy decides whether it is allowed to take it. A request runs where both say yes.
//
// The policy is one file beside the queue, <state>/policy.json, so it governs the agents that share this state
// folder. Other PCs keep their own queue folder and their own policy.
//
// Reading is tolerant and fails closed: a missing file means every project allows any machine; unknown fields
// are ignored; a project entry with a mode this version does not know allows no machine; a file that is not
// JSON, or one written by a newer version, is an error and Claim takes nothing until it is fixed. Deny wins.

// Policy modes.
const (
	PolicyAny    = "any"    // any machine (the default)
	PolicyOnly   = "only"   // only the machines listed
	PolicyExcept = "except" // any machine except the ones listed
)

// PolicyVersion is the version of policy.json this code writes and understands.
const PolicyVersion = 1

// PolicyFileName is the policy file in the state folder.
const PolicyFileName = "policy.json"

// PolicyPath is the policy file of a state folder.
func PolicyPath(stateDir string) string { return filepath.Join(stateDir, PolicyFileName) }

// ProjectPolicy is the rule of one project.
type ProjectPolicy struct {
	Mode     string   `json:"mode"`
	Machines []string `json:"machines,omitempty"` // agent names, for only and except
}

// Policy is the whole file.
type Policy struct {
	V        int                      `json:"v"`
	Updated  time.Time                `json:"updated,omitzero"`
	Projects map[string]ProjectPolicy `json:"projects"`
}

var projectRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}/[A-Za-z0-9._-]{1,100}$`)

// ProjectKey is the key a repository has in the policy: owner/name in lower case (the host treats names
// case-insensitively). ok is false for something that is not owner/name.
func ProjectKey(repo string) (key string, ok bool) {
	k := strings.ToLower(strings.TrimSpace(repo))
	return k, projectRe.MatchString(k) && !strings.Contains(k, "..")
}

// For returns the rule of a project; a project without one allows any machine.
func (p *Policy) For(repo string) ProjectPolicy {
	if p == nil {
		return ProjectPolicy{Mode: PolicyAny}
	}
	k, _ := ProjectKey(repo)
	if pp, ok := p.Projects[k]; ok {
		return pp
	}
	return ProjectPolicy{Mode: PolicyAny}
}

// Allows says whether the machine may take requests of the project.
func (p *Policy) Allows(repo, machine string) bool { return p.For(repo).Allows(machine) }

// Allows says whether the rule lets the machine run. An unknown mode allows nobody (fail closed).
func (pp ProjectPolicy) Allows(machine string) bool {
	switch pp.Mode {
	case PolicyAny, "":
		return true
	case PolicyOnly:
		return pp.lists(machine)
	case PolicyExcept:
		return !pp.lists(machine)
	}
	return false
}

func (pp ProjectPolicy) lists(machine string) bool {
	for _, m := range pp.Machines {
		if strings.EqualFold(m, machine) {
			return true
		}
	}
	return false
}

// Describe is the rule in plain words.
func (pp ProjectPolicy) Describe() string {
	switch pp.Mode {
	case PolicyAny, "":
		return "any machine"
	case PolicyOnly:
		if len(pp.Machines) == 0 {
			return "only these: none (no machine may run it)"
		}
		return "only these: " + strings.Join(pp.Machines, ", ")
	case PolicyExcept:
		return "any except these: " + strings.Join(pp.Machines, ", ")
	}
	return fmt.Sprintf("unreadable rule %q (no machine may run it until it is set again)", pp.Mode)
}

// NewProjectPolicy checks and normalises a rule: a known mode, valid agent names, no duplicates (sorted). Any
// takes no machines; Only and Except take at least one.
func NewProjectPolicy(mode string, machines []string) (ProjectPolicy, error) {
	switch mode {
	case PolicyAny:
		if len(machines) > 0 {
			return ProjectPolicy{}, errors.New("any machine takes no machine names")
		}
		return ProjectPolicy{Mode: PolicyAny}, nil
	case PolicyOnly, PolicyExcept:
	default:
		return ProjectPolicy{}, fmt.Errorf("%q is not a policy (any, only or except)", mode)
	}
	pp := ProjectPolicy{Mode: mode}
	for _, m := range machines {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		if !ValidAgentName(m) {
			return ProjectPolicy{}, fmt.Errorf("%q is not a machine name (letters, digits, dot, dash, underscore; at most 64)", m)
		}
		if !pp.lists(m) {
			pp.Machines = append(pp.Machines, m)
		}
	}
	if len(pp.Machines) == 0 {
		return ProjectPolicy{}, fmt.Errorf("%s needs at least one machine name", mode)
	}
	sort.Strings(pp.Machines)
	return pp, nil
}

// AllowMachine is the rule after allowing one machine: added to Only, removed from Except, no change for Any.
func (pp ProjectPolicy) AllowMachine(machine string) ProjectPolicy {
	switch pp.Mode {
	case PolicyOnly:
		if !pp.lists(machine) {
			pp.Machines = sortedWith(pp.Machines, machine)
		}
	case PolicyExcept:
		pp.Machines = without(pp.Machines, machine)
		if len(pp.Machines) == 0 {
			return ProjectPolicy{Mode: PolicyAny}
		}
	case PolicyAny, "":
		return ProjectPolicy{Mode: PolicyAny}
	default:
		return ProjectPolicy{Mode: PolicyOnly, Machines: []string{machine}} // an unreadable rule becomes the safe one
	}
	return pp
}

// DenyMachine is the rule after denying one machine: Any becomes Except it, added to Except, removed from Only
// (an Only list can become empty: then no machine may run the project).
func (pp ProjectPolicy) DenyMachine(machine string) ProjectPolicy {
	switch pp.Mode {
	case PolicyAny, "":
		return ProjectPolicy{Mode: PolicyExcept, Machines: []string{machine}}
	case PolicyExcept:
		if !pp.lists(machine) {
			pp.Machines = sortedWith(pp.Machines, machine)
		}
	case PolicyOnly:
		pp.Machines = without(pp.Machines, machine)
	}
	return pp
}

func sortedWith(list []string, m string) []string {
	out := append(append([]string{}, list...), m)
	sort.Strings(out)
	return out
}

func without(list []string, m string) []string {
	var out []string
	for _, x := range list {
		if !strings.EqualFold(x, m) {
			out = append(out, x)
		}
	}
	return out
}

// ErrPolicyBroken is matched (errors.Is) by the error of a policy.json that is not JSON, has the wrong types or was
// written by a newer version: the store is broken, the request is not wrong.
var ErrPolicyBroken = errors.New("the machine policy file is broken")

// ErrPolicyMoved is the error of a confirmed edit whose base (the policy stamp or the stranded runs the person saw in
// the dry run) is no longer the current one. Nothing was written.
var ErrPolicyMoved = errors.New("the policy or the queued runs changed since the dry run; nothing changed: look again and confirm again")

type brokenPolicy struct{ err error }

func (e brokenPolicy) Error() string        { return e.err.Error() }
func (e brokenPolicy) Unwrap() error        { return e.err }
func (e brokenPolicy) Is(target error) bool { return target == ErrPolicyBroken }

// LoadPolicy reads the policy of a state folder. A missing file is an empty policy (any machine everywhere).
func LoadPolicy(stateDir string) (*Policy, error) {
	var b []byte
	err := retryFS(func() (e error) { b, e = os.ReadFile(PolicyPath(stateDir)); return e })
	if errors.Is(err, fs.ErrNotExist) {
		return &Policy{V: PolicyVersion, Projects: map[string]ProjectPolicy{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("machine policy: %w", err)
	}
	var p Policy
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, brokenPolicy{fmt.Errorf("machine policy %s is not readable (%v); no request is taken until it is fixed or removed", PolicyPath(stateDir), err)}
	}
	if p.V > PolicyVersion {
		return nil, brokenPolicy{fmt.Errorf("machine policy %s was written by a newer version (v%d); no request is taken until this program is updated", PolicyPath(stateDir), p.V)}
	}
	clean := map[string]ProjectPolicy{}
	for k, pp := range p.Projects {
		key, ok := ProjectKey(k)
		if !ok {
			continue // a key that is not owner/name never matches a request
		}
		var ms []string
		for _, m := range pp.Machines {
			if ValidAgentName(m) {
				ms = append(ms, m)
			}
		}
		pp.Machines = ms
		if old, dup := clean[key]; dup && old.Mode != pp.Mode {
			pp = ProjectPolicy{Mode: "conflict"} // two spellings of one project disagree: allow nobody
		}
		clean[key] = pp
	}
	p.V, p.Projects = PolicyVersion, clean
	return &p, nil
}

// savePolicy writes the policy atomically. Call it with the policy lock held.
func savePolicy(stateDir string, p *Policy) error {
	if err := os.MkdirAll(stateDir, 0o750); err != nil {
		return err
	}
	p.V = PolicyVersion
	return writeAtomic(PolicyPath(stateDir), p)
}

// lockPolicy takes the lock of the policy file (a file made with O_EXCL), so that two edits at once do not lose
// one of them. A lock older than policyLockStale is left from a process that died and is taken over.
func lockPolicy(stateDir string) (unlock func(), err error) {
	if err := os.MkdirAll(stateDir, 0o750); err != nil {
		return nil, err
	}
	p := PolicyPath(stateDir) + ".lock"
	deadline := time.Now().Add(policyLockWait)
	for {
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // the lock of the policy file
		if err == nil {
			_ = f.Close()
			return func() { _ = retryFS(func() error { return os.Remove(p) }) }, nil
		}
		if !errors.Is(err, fs.ErrExist) && !transientFS(err) {
			return nil, err
		}
		if st, serr := os.Stat(p); serr == nil && time.Since(st.ModTime()) > policyLockStale {
			_ = os.Remove(p)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("machine policy: another edit holds %s; try again", p)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

const (
	policyLockWait  = 5 * time.Second
	policyLockStale = 30 * time.Second
)

// PolicyImpact is what a policy edit does to the queued requests of its project, measured against the machines
// this state folder knows (their heartbeat files). It counts what the edit changes: a request that no known
// machine could take before the edit is not stranded by it.
type PolicyImpact struct {
	Queued   int      `json:"queued"`   // queued requests of the project
	Stranded []string `json:"stranded"` // ids of queued requests a known machine could take before, and none after
	// NoMachine counts the queued requests no known machine could take after the edit (stranded ones included).
	NoMachine int      `json:"noMachine"`
	Machines  []string `json:"machines"` // the known machines, by name
}

// Line is the impact in plain words, for the line shown before an edit.
func (i PolicyImpact) Line() string {
	switch {
	case len(i.Stranded) == 1:
		return "1 queued run would have no machine"
	case len(i.Stranded) > 1:
		return fmt.Sprintf("%d queued runs would have no machine", len(i.Stranded))
	case i.Queued == 0:
		return "no queued run of this project is affected"
	}
	return fmt.Sprintf("every queued run of this project (%d) still has a machine it may run on", i.Queued)
}

// PolicyChange is the result of a policy edit, applied or not.
type PolicyChange struct {
	Project string        `json:"project"`
	Before  ProjectPolicy `json:"before"`
	After   ProjectPolicy `json:"after"`
	Impact  PolicyImpact  `json:"impact"`
	Applied bool          `json:"applied"`
	// Refused is set when the edit would strand queued runs and was not forced; nothing was written.
	Refused bool `json:"refused"`
	// Unchanged is set when the rule after the edit is the rule before; nothing was written.
	Unchanged bool `json:"unchanged"`
	// Base is the stamp of the whole policy the edit was measured against (see PolicyStamp). A confirm sends it back
	// with the stranded ids it was shown (PolicyExpect), so it applies only to what the person saw.
	Base string `json:"base"`
}

// PolicyExpect is what a confirmed edit was shown in its dry run: the policy stamp and the stranded run ids.
type PolicyExpect struct {
	Base     string
	Stranded []string
}

// PolicyStamp is a short hash of the whole policy (its rules and its Updated time).
func PolicyStamp(p *Policy) string {
	b, _ := json.Marshal(p) // map keys are sorted, so the same policy gives the same bytes
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:12])
}

func sameIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x, y := append([]string{}, a...), append([]string{}, b...)
	sort.Strings(x)
	sort.Strings(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

// ChangePolicy is the one way the policy of a project is changed. edit gets the current rule and returns the new
// one. The impact is computed first; an edit that would strand queued runs is refused (Refused, nothing written,
// no error) unless force is set. With dryRun nothing is written either way: the caller only wants the impact
// (the screen shows it before the person confirms).
func ChangePolicy(stateDir, repo string, edit func(ProjectPolicy) (ProjectPolicy, error), force, dryRun bool) (PolicyChange, error) {
	return ChangePolicyExpect(stateDir, repo, edit, force, dryRun, nil)
}

// ChangePolicyExpect is ChangePolicy with the base of a confirm: with expect set and force, the edit is applied only
// when, inside the lock, the policy stamp and the stranded ids are the ones of the dry run; otherwise it returns
// ErrPolicyMoved and writes nothing.
func ChangePolicyExpect(stateDir, repo string, edit func(ProjectPolicy) (ProjectPolicy, error), force, dryRun bool, expect *PolicyExpect) (PolicyChange, error) {
	key, ok := ProjectKey(repo)
	if !ok {
		return PolicyChange{}, fmt.Errorf("%q is not a project (owner/name)", repo)
	}
	unlock, err := lockPolicy(stateDir)
	if err != nil {
		return PolicyChange{}, err
	}
	defer unlock()
	pol, err := LoadPolicy(stateDir)
	if err != nil {
		return PolicyChange{}, err
	}
	before := pol.For(key)
	after, err := edit(before)
	if err != nil {
		return PolicyChange{}, err
	}
	if after.Mode == "" {
		after.Mode = PolicyAny
	}
	ch := PolicyChange{Project: key, Before: before, After: after, Base: PolicyStamp(pol)}
	ch.Impact, err = impactOf(stateDir, key, before, after)
	if err != nil {
		return PolicyChange{}, err
	}
	if expect != nil && force && (expect.Base != ch.Base || !sameIDs(expect.Stranded, ch.Impact.Stranded)) {
		return ch, ErrPolicyMoved
	}
	if samePolicy(before, after) {
		ch.Unchanged = true
		return ch, nil
	}
	if len(ch.Impact.Stranded) > 0 && !force {
		ch.Refused = true
		return ch, nil
	}
	if dryRun {
		return ch, nil
	}
	if after.Mode == PolicyAny {
		delete(pol.Projects, key)
	} else {
		pol.Projects[key] = after
	}
	pol.Updated = time.Now().UTC()
	if err := savePolicy(stateDir, pol); err != nil {
		return PolicyChange{}, err
	}
	ch.Applied = true
	return ch, nil
}

func samePolicy(a, b ProjectPolicy) bool {
	if a.Mode == "" {
		a.Mode = PolicyAny
	}
	if b.Mode == "" {
		b.Mode = PolicyAny
	}
	if a.Mode != b.Mode || len(a.Machines) != len(b.Machines) {
		return false
	}
	for _, m := range a.Machines {
		if !b.lists(m) {
			return false
		}
	}
	return true
}

// impactOf measures an edit against the queued requests of the project and the known machines.
func impactOf(stateDir, key string, before, after ProjectPolicy) (PolicyImpact, error) {
	hbs, err := ReadAgents(stateDir)
	if err != nil {
		return PolicyImpact{}, err
	}
	rs, err := (&Queue{Dir: QueueDir(stateDir)}).folderOf("queued")
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return PolicyImpact{}, err
	}
	sortClaimOrder(rs)
	imp := PolicyImpact{Stranded: []string{}, Machines: []string{}}
	for _, h := range hbs {
		imp.Machines = append(imp.Machines, h.Name)
	}
	for _, r := range rs {
		if k, _ := ProjectKey(r.Repo); k != key {
			continue
		}
		imp.Queued++
		could, can := false, false
		for _, h := range hbs {
			if !hasLabels(h.Labels, r.Labels) {
				continue
			}
			could = could || before.Allows(h.Name)
			can = can || after.Allows(h.Name)
		}
		if !can {
			imp.NoMachine++
			if could {
				imp.Stranded = append(imp.Stranded, r.ID)
			}
		}
	}
	return imp, nil
}

// hasLabels says whether have holds every label of need.
func hasLabels(have, need []string) bool {
	for _, n := range need {
		found := false
		for _, h := range have {
			if h == n {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
