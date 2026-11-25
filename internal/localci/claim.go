package localci

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Many PCs, one queue. Each PC keeps its own queue folder, so two PCs that watch the same repository both queue
// the same (commit, workflow). A claim, kept on the host as a git ref, makes sure only one of them runs it.
//
// The only atomic operation the host gives is "create a ref that does not exist yet": the second create fails.
// (Updating a ref with force=false is documented to refuse a non fast-forward move, but the host does not enforce
// it for a ref outside refs/heads, so it cannot serve as a compare-and-swap.) Everything here is therefore built
// on create:
//
//   - A claim is the ref <ns>/<commit>-<workflow>. A takeover creates the next generation, <name>+2, <name>+3 and
//     so on. Exactly one taker can create a given generation, so exactly one wins. The highest generation is the
//     owner; the lower ones are deleted by whoever wins and are never trusted.
//   - The ref points to a commit with no files whose message is a small JSON record (FleetRecord): who holds it,
//     what for, and the state (held or done). The holder refreshes it by writing a new commit to its own ref; the
//     host stamps the commit with ITS clock, and the age of a lease is that stamp against the host's own Date
//     header, so a PC with a wrong clock cannot make a lease look stale or fresh.
//   - A lease is stale when it was not refreshed for TTL. Anyone may then create the next generation.
//
// The safety property is "a duplicate run at worst, never a lost run". A holder that was only slow (asleep, offline)
// notices at its next refresh that a higher generation exists and cancels its own run; until then both may run.
// Nothing here deletes a claim that another agent holds: a ref is only deleted by its own generation's holder
// (release), by the taker of the next generation (cleanup of the generation it replaced), or by the clean-up of
// finished records after a week.

// Namespaces of the refs the host keeps, below refs/.
const (
	RefNSClaim = "ci-claim"
	RefNSAgent = "ci-agent"
)

// Defaults of the fleet.
const (
	DefaultLeaseTTL   = 5 * time.Minute  // a lease not refreshed for this long can be taken over
	DefaultFleetEvery = time.Minute      // between two refreshes, agent records and syncs (keeps within the REST limits)
	DoneClaimKeep     = 7 * 24 * time.Hour
	StoppedAgentKeep  = 24 * time.Hour
	AgentRecordKeep   = 7 * 24 * time.Hour
)

// Claim and agent record states.
const (
	ClaimHeld = "held"
	ClaimDone = "done"
)

// Errors of a ClaimHost.
var (
	ErrRefExists = errors.New("the ref already exists")
	ErrRefGone   = errors.New("the ref does not exist")
	// ErrClaimDenied: the host refused to take records from this PC (the token cannot write the repository).
	ErrClaimDenied = errors.New("the host does not let this PC write claim records")
)

// FleetRecord is what a claim ref or an agent ref says. It is public (the repository's refs and commits are
// readable by anyone who can read the repository), so it holds the opaque id and the labels, never a PC or a
// user name or a path.
type FleetRecord struct {
	V     int    `json:"v"`
	Agent string `json:"agent"`
	State string `json:"state"` // claims: held or done; agents: ready, paused, draining or stopped
	// Claims.
	Conclusion string `json:"conclusion,omitempty"` // of a done claim: success or failure
	Item       string `json:"item,omitempty"`       // "pr 284 into develop"
	Workflow   string `json:"workflow,omitempty"`
	SHA        string `json:"sha,omitempty"`
	// Agents.
	Labels  []string   `json:"labels,omitempty"`
	Slots   int        `json:"slots,omitempty"`
	Jobs    []FleetJob `json:"jobs,omitempty"`
	Started time.Time  `json:"started,omitzero"`
	// Beat is the writer's clock, for people. Staleness never uses it.
	Beat time.Time `json:"beat,omitzero"`
}

// FleetJob is a request an agent is running.
type FleetJob struct {
	Item  string    `json:"item"`
	Since time.Time `json:"since,omitzero"`
}

// RefRecord is one ref of a namespace as the host reports it.
type RefRecord struct {
	Name string
	Rec  FleetRecord // zero when the commit did not hold a record
	// Age is how long ago the record was written, by the host's clock (its commit stamp against its Date header).
	Age time.Duration
}

// ClaimHost is the part of a code host the fleet needs: a small store of named records with an atomic create.
// The names are checked by the implementation (ValidRefName); a ClaimHost never touches a ref outside RefNSClaim
// and RefNSAgent.
type ClaimHost interface {
	// ListRefs returns the refs of ns whose name starts with prefix, with their records.
	ListRefs(ctx context.Context, repo, ns, prefix string) ([]RefRecord, error)
	// CreateRef makes the ref; ErrRefExists when it is there already. It is atomic: of many racers one wins.
	CreateRef(ctx context.Context, repo, ns, name string, rec FleetRecord) error
	// UpdateRef replaces the record of the ref; ErrRefGone when it is not there.
	UpdateRef(ctx context.Context, repo, ns, name string, rec FleetRecord) error
	// DeleteRef removes the ref; a ref that is already gone is not an error.
	DeleteRef(ctx context.Context, repo, ns, name string) error
}

var (
	refNameRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,200}$`)
	workflowRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,80}$`)
	fleetIDRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{3,31}$`)
	slotRe     = regexp.MustCompile(`^[0-9T]{1,16}$`)
)

// ValidRefName says whether name is one this package would make: letters, digits and . _ + - only, so no path,
// no "..", no ".lock" ending and no way out of the namespace. Every ref delete goes through it.
func ValidRefName(name string) bool {
	return refNameRe.MatchString(name) && !strings.Contains(name, "..") && !strings.HasSuffix(name, ".lock") && !strings.HasSuffix(name, ".")
}

// ValidRefNS says whether ns is one of the two namespaces of the fleet.
func ValidRefNS(ns string) bool { return ns == RefNSClaim || ns == RefNSAgent }

// ClaimName is the base name of the claim of a request: <commit>-<workflow>. A scheduled run carries its time
// slot too, because the same commit can be asked for again the next day.
func ClaimName(r Request) (string, error) {
	if !shaRe.MatchString(r.SHA) {
		return "", fmt.Errorf("claim: %q is not a commit id", r.SHA)
	}
	if !workflowRe.MatchString(r.Workflow) || strings.Contains(r.Workflow, "..") || strings.HasSuffix(r.Workflow, ".") || strings.HasSuffix(r.Workflow, ".lock") {
		return "", fmt.Errorf("claim: %q is not a workflow file name", r.Workflow)
	}
	n := r.SHA + "-" + r.Workflow
	if r.Kind == KindSchedule && r.Slot != "" {
		if !slotRe.MatchString(r.Slot) {
			return "", fmt.Errorf("claim: %q is not a time slot", r.Slot)
		}
		n += "-s" + r.Slot
	}
	return n, nil
}

// genName is the ref of generation gen of a claim: the base name for the first, base+N after.
func genName(base string, gen int) string {
	if gen <= 1 {
		return base
	}
	return base + "+" + strconv.Itoa(gen)
}

// genOf reads the generation out of a ref name of base, or 0 when name is not of this claim.
func genOf(base, name string) int {
	if name == base {
		return 1
	}
	rest, ok := strings.CutPrefix(name, base+"+")
	if !ok {
		return 0
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n < 2 || strconv.Itoa(n) != rest {
		return 0
	}
	return n
}

// NewFleetID makes a random opaque id: "m-" and 8 hex digits.
func NewFleetID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return "m-" + hex.EncodeToString(b[:])
}

// FleetIDPath is where the id of a state folder is kept.
func FleetIDPath(stateDir string) string { return filepath.Join(stateDir, "fleet", "id") }

// LoadFleetID returns the opaque id of this state folder, making and saving one the first time. The id belongs to
// the state folder, not to the PC: two agents that run from two folders of one PC count as two machines, which is
// what keeps them from treating each other's claims as their own.
func LoadFleetID(stateDir string) (string, error) {
	p := FleetIDPath(stateDir)
	if b, err := os.ReadFile(p); err == nil { //nolint:gosec // path inside the state folder
		if id := strings.TrimSpace(string(b)); fleetIDRe.MatchString(id) {
			return id, nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return "", err
	}
	id := NewFleetID()
	if err := os.WriteFile(p, []byte(id+"\n"), 0o600); err != nil {
		return "", err
	}
	return id, nil
}

// ClaimOutcome is the answer of Acquire.
type ClaimOutcome int

// The three answers of Acquire.
const (
	ClaimWon  ClaimOutcome = iota // this agent holds the claim: run the request
	ClaimOver                     // the request already ran on another PC: do not run it
	ClaimBusy                     // another PC is running it now: look again later
)

// ClaimDecision is what Acquire found.
type ClaimDecision struct {
	Outcome    ClaimOutcome
	Holder     string        // the other agent's id, for ClaimOver and ClaimBusy
	Conclusion string        // how the other PC's run ended, for ClaimOver
	RetryAfter time.Duration // for ClaimBusy
}

// Fleet takes part in the claims and the agent list of one repository for one agent.
type Fleet struct {
	Host   ClaimHost
	Repo   string
	ID     string // the opaque id, see LoadFleetID
	Labels []string
	// TTL is how long a lease may go without a refresh before another agent can take it over; default 5 minutes.
	TTL time.Duration
	// RetryEvery is how long a request another PC runs waits before this PC looks again; zero picks 30 s to a
	// minute from the lease's age.
	RetryEvery time.Duration
	Log        func(format string, a ...any)

	mu   sync.Mutex
	held map[string]*heldClaim // by request id
}

type heldClaim struct {
	base string
	name string // the ref of the generation this agent holds
	rec  FleetRecord
}

func (f *Fleet) ttl() time.Duration {
	if f.TTL > 0 {
		return f.TTL
	}
	return DefaultLeaseTTL
}

func (f *Fleet) logf(format string, a ...any) {
	if f.Log != nil {
		f.Log(format, a...)
	}
}

func (f *Fleet) claimRecord(r Request, now time.Time) FleetRecord {
	return FleetRecord{V: 1, Agent: f.ID, State: ClaimHeld, Item: r.Label(), Workflow: r.Workflow, SHA: r.SHA, Started: now, Beat: now}
}

func (f *Fleet) remember(r Request, base, name string, rec FleetRecord) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.held == nil {
		f.held = map[string]*heldClaim{}
	}
	f.held[r.ID] = &heldClaim{base: base, name: name, rec: rec}
}

// topOf finds the highest generation of base among refs.
func topOf(base string, refs []RefRecord) (top RefRecord, gen int, ok bool) {
	for _, r := range refs {
		if g := genOf(base, r.Name); g > gen {
			top, gen, ok = r, g, true
		}
	}
	return top, gen, ok
}

// Acquire tries to take the claim of a request. A returned error means the host could not be asked: the caller
// must not run the request (fail closed) but try again later.
func (f *Fleet) Acquire(ctx context.Context, r Request) (ClaimDecision, error) {
	base, err := ClaimName(r)
	if err != nil {
		return ClaimDecision{}, err
	}
	now := time.Now()
	for attempt := 0; attempt < 4; attempt++ {
		refs, err := f.Host.ListRefs(ctx, f.Repo, RefNSClaim, base)
		if err != nil {
			return ClaimDecision{}, err
		}
		top, gen, found := topOf(base, refs)
		if !found {
			rec := f.claimRecord(r, now)
			switch err := f.Host.CreateRef(ctx, f.Repo, RefNSClaim, genName(base, 1), rec); {
			case err == nil:
				f.remember(r, base, genName(base, 1), rec)
				return ClaimDecision{Outcome: ClaimWon}, nil
			case errors.Is(err, ErrRefExists):
				continue // someone created it between our look and our create: look again
			default:
				return ClaimDecision{}, err
			}
		}
		switch {
		case top.Rec.Agent == f.ID && top.Rec.State == ClaimHeld && f.holdsBase(base, r.ID):
			// Another request of this PC (a push and a pull request for the same commit) is running it now.
			return ClaimDecision{Outcome: ClaimBusy, Holder: f.ID, RetryAfter: 30 * time.Second}, nil
		case top.Rec.Agent == f.ID && top.Rec.State == ClaimHeld:
			// This agent's own earlier process died with the claim (one process runs per state folder, so it is not
			// running). Take it up again.
			rec := top.Rec
			rec.Beat = now
			if err := f.Host.UpdateRef(ctx, f.Repo, RefNSClaim, top.Name, rec); err != nil {
				if errors.Is(err, ErrRefGone) {
					continue
				}
				return ClaimDecision{}, err
			}
			f.remember(r, base, top.Name, rec)
			return ClaimDecision{Outcome: ClaimWon}, nil
		case top.Rec.State == ClaimDone && !r.Rerun:
			return ClaimDecision{Outcome: ClaimOver, Holder: top.Rec.Agent, Conclusion: top.Rec.Conclusion}, nil
		case top.Rec.State == ClaimDone || top.Age > f.ttl() || top.Rec.State != ClaimHeld:
			// a deliberate rerun of a finished claim, a lease nobody refreshed, or a record we cannot read: next generation
			rec := f.claimRecord(r, now)
			next := genName(base, gen+1)
			switch err := f.Host.CreateRef(ctx, f.Repo, RefNSClaim, next, rec); {
			case err == nil:
				f.remember(r, base, next, rec)
				for _, old := range refs {
					if g := genOf(base, old.Name); g > 0 && g <= gen {
						if derr := f.Host.DeleteRef(ctx, f.Repo, RefNSClaim, old.Name); derr != nil {
							f.logf("claim: could not remove the replaced %s: %v", old.Name, derr)
						}
					}
				}
				if top.Rec.State == ClaimHeld {
					f.logf("claim %s: took over from %s, its lease was not refreshed for %s", shortSHA(r.SHA), shortOpaque(top.Rec.Agent), top.Age.Round(time.Second))
				}
				return ClaimDecision{Outcome: ClaimWon}, nil
			case errors.Is(err, ErrRefExists):
				continue // another taker was first; look again, it now holds the claim
			default:
				return ClaimDecision{}, err
			}
		default:
			return ClaimDecision{Outcome: ClaimBusy, Holder: top.Rec.Agent, RetryAfter: f.retryAfter(top.Age)}, nil
		}
	}
	return ClaimDecision{Outcome: ClaimBusy, RetryAfter: f.retryAfter(0)}, nil
}

// retryAfter is when a request that another PC is running should be looked at again: when its lease could have
// run out, but not sooner than 30 seconds and not later than a minute.
func (f *Fleet) retryAfter(age time.Duration) time.Duration {
	if f.RetryEvery > 0 {
		return f.RetryEvery
	}
	d := f.ttl() - age
	return min(max(d, 30*time.Second), time.Minute)
}

func shortOpaque(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// holdsBase says whether a request other than id holds the claim of base.
func (f *Fleet) holdsBase(base, id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for hid, h := range f.held {
		if h.base == base && hid != id {
			return true
		}
	}
	return false
}

// Held tells whether this agent holds a claim for the request.
func (f *Fleet) Held(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.held[id]
	return ok
}

// Refresh renews the lease of every claim this agent holds and returns the ids of the requests whose claim it
// no longer holds (another agent took it over, or the ref is gone). Those runs must stop.
func (f *Fleet) Refresh(ctx context.Context) (lost []string) {
	f.mu.Lock()
	hs := make(map[string]heldClaim, len(f.held))
	for id, h := range f.held {
		hs[id] = *h
	}
	f.mu.Unlock()
	for id, h := range hs {
		switch f.refreshOne(ctx, h) {
		case refreshLost:
			f.mu.Lock()
			delete(f.held, id)
			f.mu.Unlock()
			lost = append(lost, id)
		}
	}
	sort.Strings(lost)
	return lost
}

type refreshResult int

const (
	refreshOK refreshResult = iota
	refreshUnknown
	refreshLost
)

func (f *Fleet) refreshOne(ctx context.Context, h heldClaim) refreshResult {
	refs, err := f.Host.ListRefs(ctx, f.Repo, RefNSClaim, h.base)
	if err != nil {
		f.logf("claim %s: lease not refreshed: %v", shortSHA(h.rec.SHA), err)
		return refreshUnknown
	}
	top, _, found := topOf(h.base, refs)
	if !found || top.Name != h.name || top.Rec.Agent != f.ID {
		f.logf("claim %s: lost, %s", shortSHA(h.rec.SHA), noteFor(found, top))
		return refreshLost
	}
	rec := h.rec
	rec.Beat = time.Now()
	if err := f.Host.UpdateRef(ctx, f.Repo, RefNSClaim, h.name, rec); err != nil {
		if errors.Is(err, ErrRefGone) {
			f.logf("claim %s: lost, its ref is gone", shortSHA(h.rec.SHA))
			return refreshLost
		}
		f.logf("claim %s: lease not refreshed: %v", shortSHA(h.rec.SHA), err)
		return refreshUnknown
	}
	return refreshOK
}

func noteFor(found bool, top RefRecord) string {
	if !found {
		return "its ref is gone"
	}
	return "another agent (" + shortOpaque(top.Rec.Agent) + ") holds it now"
}

// Finish ends the claim of a request after it ran. A success or a failure is kept as a done claim, so the other
// PCs, which still have the request in their own queues, do not run it again. Anything else (cancelled,
// superseded, an error, a stop) removes the claim, so that another PC may run the request.
func (f *Fleet) Finish(ctx context.Context, r Request, conclusion string) {
	f.mu.Lock()
	h := f.held[r.ID]
	delete(f.held, r.ID)
	f.mu.Unlock()
	if h == nil {
		return
	}
	if conclusion == "success" || conclusion == "failure" {
		rec := h.rec
		rec.State, rec.Conclusion, rec.Beat = ClaimDone, conclusion, time.Now()
		if err := f.Host.UpdateRef(ctx, f.Repo, RefNSClaim, h.name, rec); err != nil {
			f.logf("claim %s: could not mark it done: %v", shortSHA(r.SHA), err)
		}
		return
	}
	f.drop(ctx, h)
}

// Release removes the claim of a request without a result (cancelled, stopped, lost).
func (f *Fleet) Release(ctx context.Context, r Request) {
	f.mu.Lock()
	h := f.held[r.ID]
	delete(f.held, r.ID)
	f.mu.Unlock()
	if h != nil {
		f.drop(ctx, h)
	}
}

func (f *Fleet) drop(ctx context.Context, h *heldClaim) {
	if err := f.Host.DeleteRef(ctx, f.Repo, RefNSClaim, h.name); err != nil {
		f.logf("claim %s: could not release it (another PC takes it over after %s): %v", shortSHA(h.rec.SHA), f.ttl(), err)
	}
}

// ReleaseAll removes every claim still held, for an agent that ends.
func (f *Fleet) ReleaseAll(ctx context.Context) {
	f.mu.Lock()
	hs := f.held
	f.held = nil
	f.mu.Unlock()
	for _, h := range hs {
		f.drop(ctx, h)
	}
}

// Publish writes this agent's record: who it is, its labels and what it runs. state "stopped" marks a clean end.
func (f *Fleet) Publish(ctx context.Context, hb Heartbeat, state string) error {
	rec := FleetRecord{V: 1, Agent: f.ID, State: state, Labels: cleanLabels(f.Labels), Slots: hb.Slots, Started: hb.Started, Beat: time.Now()}
	for _, j := range hb.Jobs {
		rec.Jobs = append(rec.Jobs, FleetJob{Item: cleanText(j.Label, 80), Since: j.Since})
	}
	return f.putAgent(ctx, rec)
}

// Probe writes a first agent record (state "ready", nothing running). It is the check at start that the host
// takes records from this PC: a token that cannot write the repository's contents fails here with ErrClaimDenied.
func (f *Fleet) Probe(ctx context.Context) error {
	return f.putAgent(ctx, FleetRecord{V: 1, Agent: f.ID, State: "ready", Labels: cleanLabels(f.Labels), Started: time.Now(), Beat: time.Now()})
}

func (f *Fleet) putAgent(ctx context.Context, rec FleetRecord) error {
	err := f.Host.UpdateRef(ctx, f.Repo, RefNSAgent, f.ID, rec)
	if errors.Is(err, ErrRefGone) {
		err = f.Host.CreateRef(ctx, f.Repo, RefNSAgent, f.ID, rec)
		if errors.Is(err, ErrRefExists) {
			err = f.Host.UpdateRef(ctx, f.Repo, RefNSAgent, f.ID, rec)
		}
	}
	return err
}

// FleetView is what Sync saves for the Releases app: the agents that published a record, by their opaque ids.
type FleetView struct {
	Updated time.Time    `json:"updated"`
	Repo    string       `json:"repo"`
	Self    string       `json:"self"`
	Agents  []FleetAgent `json:"agents"`
}

// FleetAgent is one agent of the view.
type FleetAgent struct {
	ID        string     `json:"id"`
	Labels    []string   `json:"labels,omitempty"`
	State     string     `json:"state"` // ready, paused, draining or stopped, as the agent said
	Slots     int        `json:"slots,omitempty"`
	Jobs      []FleetJob `json:"jobs,omitempty"`
	Started   time.Time  `json:"started,omitzero"`
	LastHeard time.Time  `json:"lastHeard"`
}

// FleetViewPath is the file the Releases app reads.
func FleetViewPath(stateDir string) string { return filepath.Join(stateDir, "fleet", "agents.json") }

// Sync reads the agent records of the host, writes them to the state folder for the Releases app and cleans up
// what is old: finished claims after a week, agents that stopped after a day.
func (f *Fleet) Sync(ctx context.Context, stateDir string) error {
	refs, err := f.Host.ListRefs(ctx, f.Repo, RefNSAgent, "")
	if err != nil {
		return err
	}
	now := time.Now()
	view := FleetView{Updated: now, Repo: f.Repo, Self: f.ID, Agents: []FleetAgent{}}
	for _, r := range refs {
		if !fleetIDRe.MatchString(r.Name) {
			continue
		}
		old := r.Age > AgentRecordKeep || (r.Rec.State == "stopped" && r.Age > StoppedAgentKeep)
		if old && r.Name != f.ID {
			if err := f.Host.DeleteRef(ctx, f.Repo, RefNSAgent, r.Name); err != nil {
				f.logf("fleet: could not remove the old record %s: %v", r.Name, err)
			}
			continue
		}
		view.Agents = append(view.Agents, agentOf(r, now))
	}
	sort.Slice(view.Agents, func(i, j int) bool { return view.Agents[i].ID < view.Agents[j].ID })
	if err := os.MkdirAll(filepath.Dir(FleetViewPath(stateDir)), 0o750); err != nil {
		return err
	}
	return writeAtomic(FleetViewPath(stateDir), view)
}

// CleanClaims deletes the done claims older than DoneClaimKeep.
func (f *Fleet) CleanClaims(ctx context.Context) {
	refs, err := f.Host.ListRefs(ctx, f.Repo, RefNSClaim, "")
	if err != nil {
		return
	}
	for _, r := range refs {
		if r.Age > DoneClaimKeep && r.Rec.State != ClaimHeld {
			if err := f.Host.DeleteRef(ctx, f.Repo, RefNSClaim, r.Name); err != nil {
				f.logf("fleet: could not remove the old claim %s: %v", r.Name, err)
			}
		}
	}
}

// agentOf turns an agent ref into a view entry. The record came from the host and may have been written by anyone
// with write access, so every text is cut to size and stripped of control characters.
func agentOf(r RefRecord, now time.Time) FleetAgent {
	a := FleetAgent{ID: r.Name, Labels: cleanLabels(r.Rec.Labels), State: cleanState(r.Rec.State), Slots: min(max(r.Rec.Slots, 0), 64), Started: r.Rec.Started, LastHeard: now.Add(-r.Age)}
	for i, j := range r.Rec.Jobs {
		if i >= 16 {
			break
		}
		a.Jobs = append(a.Jobs, FleetJob{Item: cleanText(j.Item, 80), Since: j.Since})
	}
	return a
}

func cleanState(s string) string {
	switch s {
	case "ready", "paused", "draining", "stopped":
		return s
	}
	return "ready"
}

// cleanText keeps printable characters only and cuts the text to max runes.
func cleanText(s string, max int) string {
	var b strings.Builder
	n := 0
	for _, c := range s {
		if n >= max {
			break
		}
		if c == utf8Replacement || !unicode.IsPrint(c) {
			c = '?'
		}
		b.WriteRune(c)
		n++
	}
	return strings.TrimSpace(b.String())
}

const utf8Replacement = '�'

var labelRe = regexp.MustCompile(`[^A-Za-z0-9._:-]`)

// cleanLabels keeps up to 8 labels of up to 32 safe characters.
func cleanLabels(in []string) []string {
	var out []string
	for _, l := range in {
		l = labelRe.ReplaceAllString(l, "")
		if l == "" {
			continue
		}
		if len(l) > 32 {
			l = l[:32]
		}
		out = append(out, l)
		if len(out) == 8 {
			break
		}
	}
	return out
}

// ReadFleetView reads what Sync saved; a missing file is no view. The agents are cleaned again on the way in,
// the file is only as safe as the folder it sits in.
func ReadFleetView(stateDir string) (*FleetView, error) {
	b, err := os.ReadFile(FleetViewPath(stateDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var v FleetView
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, fmt.Errorf("fleet view: %w", err)
	}
	for i := range v.Agents {
		a := &v.Agents[i]
		a.Labels, a.State = cleanLabels(a.Labels), cleanState(a.State)
		for j := range a.Jobs {
			a.Jobs[j].Item = cleanText(a.Jobs[j].Item, 80)
		}
	}
	return &v, nil
}
