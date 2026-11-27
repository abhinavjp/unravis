package releasedash

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"api-audit-explorer/internal/activitylog"
	"api-audit-explorer/internal/localci"
	"api-audit-explorer/internal/releasehold"
)

// The Homestretch read API (BL-704): four GET endpoints that show the queue, the machine policy, the holds and the
// activity log of the CI state folder. They read the same files the unravis-ci commands read and write nothing: no
// folder, no lock file, no marker. A state folder that is not there answers empty (ciFound false). A file that
// cannot be read answers 200 with an "error" in plain words and whatever could still be read, so a page can show
// the reason instead of a blank. The field names follow the commands' --json output where the same fact is shown.

// hsAgentStale is how long a machine may be silent before it counts as gone. It is the limit unravis-ci uses, so the
// words match what `unravis-ci queue list` and `machines list` say.
const hsAgentStale = 3 * localci.DefaultBeatEvery

// Limits of the activity and finished-request lists.
const (
	hsDefaultActivity = 50
	hsMaxActivity     = 1000
	hsDefaultDone     = 20
	hsMaxDone         = 200
)

// Priority words. A request shows First when somebody moved it to the top of the queue (its priority is below the
// priority its kind starts with); every other request shows Normal.
const (
	hsPriorityNormal = "Normal"
	hsPriorityFirst  = "First"
)

// HSRequest is one request of the queue, as the page shows it.
type HSRequest struct {
	ID            string              `json:"id"`
	Kind          string              `json:"kind"`
	Repo          string              `json:"repo"`
	Label         string              `json:"label"`
	Ref           string              `json:"ref,omitempty"`
	Base          string              `json:"base,omitempty"`
	Number        int                 `json:"number,omitempty"`
	SHA           string              `json:"sha,omitempty"`
	Workflow      string              `json:"workflow"`
	State         string              `json:"state"` // queued, running, done, cancelled or superseded
	Priority      int                 `json:"priority"`
	PriorityLabel string              `json:"priorityLabel"` // Normal or First
	EnqueuedAt    string              `json:"enqueuedAt"`
	ClaimedBy     string              `json:"claimedBy,omitempty"`
	ClaimedAt     string              `json:"claimedAt,omitempty"`
	FinishedAt    string              `json:"finishedAt,omitempty"`
	Attempts      int                 `json:"attempts,omitempty"`
	Conclusion    string              `json:"conclusion,omitempty"`
	Reason        string              `json:"reason,omitempty"`
	SupersededBy  string              `json:"supersededBy,omitempty"`
	Display       string              `json:"display,omitempty"` // a plain line for a request that was replaced
	Waiting       bool                `json:"waiting,omitempty"` // queued and no machine takes it at its next look
	Why           *localci.WaitReason `json:"why,omitempty"`     // queued requests: why it has not started
}

// HSQueueCounts are the sizes of the lists.
type HSQueueCounts struct {
	Queued  int `json:"queued"`
	Running int `json:"running"`
	Waiting int `json:"waiting"` // queued requests that no machine takes at its next look
	Done    int `json:"done"`    // finished requests in the folder, shown or not
}

// HSQueue is the answer of /api/queue.
type HSQueue struct {
	CIFound      bool          `json:"ciFound"`
	Now          string        `json:"now"`
	Paused       bool          `json:"paused"` // the PAUSE file exists: no machine takes a new request
	PausedReason string        `json:"pausedReason,omitempty"`
	Counts       HSQueueCounts `json:"counts"`
	Queued       []HSRequest   `json:"queued"`  // in the order machines take them
	Running      []HSRequest   `json:"running"` // oldest claim first
	Done         []HSRequest   `json:"done"`    // newest first, at most limit
	Error        string        `json:"error,omitempty"`
}

// Queue reads the queue folders.
func (v *ciView) Queue(doneLimit int) HSQueue {
	q := HSQueue{CIFound: v.found, Now: tstr(v.now), Queued: []HSRequest{}, Running: []HSRequest{}, Done: []HSRequest{}}
	if !v.found {
		return q
	}
	var errs []string
	rs, err := localci.ListRequests(v.dir)
	if err != nil {
		errs = append(errs, "The queue could not be read completely: "+err.Error())
	}
	in, werr := localci.WaitInputOf(v.dir, v.now, hsAgentStale)
	if werr != nil {
		errs = append(errs, "Why a request waits could not be worked out: "+werr.Error())
	}
	q.Paused, q.PausedReason = in.Paused, v.rd(in.PausedReason)
	if werr != nil {
		q.Paused = localci.IsPaused(v.dir)
	}
	var queued, running, done []localci.Request
	for _, r := range rs {
		switch r.State {
		case localci.StateQueued:
			queued = append(queued, r)
		case localci.StateRunning:
			running = append(running, r)
		default:
			done = append(done, r)
		}
	}
	localci.SortClaimOrder(queued)
	sort.SliceStable(running, func(i, j int) bool { return running[i].ClaimedAt.Before(running[j].ClaimedAt) })
	sort.SliceStable(done, func(i, j int) bool { return done[i].FinishedAt.After(done[j].FinishedAt) })
	q.Counts = HSQueueCounts{Queued: len(queued), Running: len(running), Done: len(done)}
	for _, r := range queued {
		h := v.requestOf(r)
		if werr == nil {
			w := localci.WhyWaiting(r, in)
			w.Text = v.rd(w.Text)
			for i := range w.Machines {
				w.Machines[i] = v.rd(w.Machines[i])
			}
			h.Why = &w
			h.Waiting = w.Code != localci.WaitReady
			if h.Waiting {
				q.Counts.Waiting++
			}
		}
		q.Queued = append(q.Queued, h)
	}
	for _, r := range running {
		q.Running = append(q.Running, v.requestOf(r))
	}
	if doneLimit > 0 && len(done) > doneLimit {
		done = done[:doneLimit]
	}
	for _, r := range done {
		q.Done = append(q.Done, v.requestOf(r))
	}
	q.Error = strings.Join(errs, " ")
	return q
}

func (v *ciView) requestOf(r localci.Request) HSRequest {
	h := HSRequest{ID: v.rd(r.ID), Kind: v.rd(r.Kind), Repo: v.rd(r.Repo), Label: v.rd(r.Label()), Ref: v.rd(r.Ref), Base: v.rd(r.Base), Number: r.Number,
		SHA: shaOnly(r.SHA), Workflow: v.rd(r.Workflow), State: r.State, Priority: r.Priority, PriorityLabel: hsPriorityNormal,
		EnqueuedAt: tstr(r.EnqueuedAt), ClaimedBy: v.rd(r.ClaimedBy), ClaimedAt: tstr(r.ClaimedAt), FinishedAt: tstr(r.FinishedAt),
		Attempts: r.Attempts, Conclusion: v.rd(r.Conclusion), Reason: v.rd(r.Reason), SupersededBy: v.rd(r.SupersededBy)}
	if r.Priority < localci.PriorityFor(r.Kind) {
		h.PriorityLabel = hsPriorityFirst
	}
	if r.State == localci.StateSuperseded || r.SupersededBy != "" {
		h.Display = "Replaced by a newer request"
		if r.SupersededBy != "" {
			h.Display += " (" + h.SupersededBy + ")"
		}
	}
	return h
}

// ---------------------------------------------------------------- policy

// HSProjectPolicy is the machine rule of one project and what it means for the queue today.
type HSProjectPolicy struct {
	Project  string   `json:"project"` // owner/name in lower case
	Mode     string   `json:"mode"`    // any, only or except
	Machines []string `json:"machines"`
	Text     string   `json:"text"`     // the rule in plain words
	Explicit bool     `json:"explicit"` // the policy file has a rule for it; false means the default (any machine)
	// Impact is what the rule does to the queued requests of the project right now: how many there are and how many
	// have no known machine that may run them (the Stranded list is always empty here: nothing is being edited).
	Impact     localci.PolicyImpact `json:"impact"`
	ImpactLine string               `json:"impactLine"`
	Error      string               `json:"error,omitempty"`
}

// HSMachine is one machine this state folder knows, by its heartbeat.
type HSMachine struct {
	Name       string   `json:"name"`
	State      string   `json:"state"` // ready, paused, draining, low-disk, or gone when the heartbeat is stale
	Labels     []string `json:"labels"`
	Slots      int      `json:"slots"`
	Running    int      `json:"running"`
	LastSeen   string   `json:"lastSeen"`
	SeenAgo    string   `json:"seenAgo"`
	DrainAsked bool     `json:"drainAsked"` // a drain mark waits for it
	Denied     []string `json:"denied"`     // the projects whose policy does not allow this machine
}

// HSPolicy is the answer of /api/policy.
type HSPolicy struct {
	CIFound  bool              `json:"ciFound"`
	Now      string            `json:"now"`
	Updated  string            `json:"updated,omitempty"` // when the policy file was last written
	Projects []HSProjectPolicy `json:"projects"`
	Machines []HSMachine       `json:"machines"`
	Error    string            `json:"error,omitempty"`
}

// Policy reads the policy file, the heartbeats and the queue.
func (v *ciView) Policy() HSPolicy {
	out := HSPolicy{CIFound: v.found, Now: tstr(v.now), Projects: []HSProjectPolicy{}, Machines: []HSMachine{}}
	if !v.found {
		return out
	}
	var errs []string
	pol, err := localci.LoadPolicy(v.dir)
	if err != nil {
		errs = append(errs, "The machine policy could not be read, so no machine takes anything until it is fixed: "+err.Error())
		pol = nil
	}
	hbs, err := localci.ReadAgents(v.dir)
	if err != nil {
		errs = append(errs, "The machines could not be listed: "+err.Error())
	}
	rs, err := localci.ListRequests(v.dir)
	if err != nil {
		errs = append(errs, "The queue could not be read completely: "+err.Error())
	}

	// The projects shown: every project the file has a rule for, the project this page is about, and every project
	// that has a queued request.
	keys := map[string]bool{}
	if pol != nil {
		out.Updated = tstr(pol.Updated)
		for k := range pol.Projects {
			keys[k] = true
		}
	}
	if k, ok := localci.ProjectKey(v.p.Repo); ok {
		keys[k] = true
	}
	for _, r := range rs {
		if r.State != localci.StateQueued {
			continue
		}
		if k, ok := localci.ProjectKey(r.Repo); ok {
			keys[k] = true
		}
	}
	names := make([]string, 0, len(keys))
	for k := range keys {
		names = append(names, k)
	}
	sort.Strings(names)

	for _, k := range names {
		rule := pol.For(k)
		explicit := false
		if pol != nil {
			_, explicit = pol.Projects[k]
		}
		p := HSProjectPolicy{Project: v.rd(k), Mode: rule.Mode, Machines: []string{}, Text: v.rd(rule.Describe()), Explicit: explicit, Impact: localci.PolicyImpact{Stranded: []string{}, Machines: []string{}}}
		if p.Mode == "" {
			p.Mode = localci.PolicyAny
		}
		for _, m := range rule.Machines {
			p.Machines = append(p.Machines, v.rd(m))
		}
		if pol != nil {
			imp, ierr := localci.CurrentImpact(v.dir, k)
			if ierr != nil {
				p.Error = v.rd("The effect on the queue could not be worked out: " + ierr.Error())
			} else {
				imp.Machines = v.rdAll(imp.Machines)
				imp.Stranded = v.rdAll(imp.Stranded)
				p.Impact = imp
			}
		}
		p.ImpactLine = impactLine(p.Impact, p.Error != "")
		out.Projects = append(out.Projects, p)
	}

	for _, h := range hbs {
		m := HSMachine{Name: v.rd(h.Name), State: h.State, Labels: v.rdAll(h.Labels), Slots: h.Slots, Running: len(h.Jobs),
			LastSeen: tstr(h.LastSeen), SeenAgo: ago(v.now.Sub(h.LastSeen)), DrainAsked: localci.DrainRequested(v.dir, h.Name), Denied: []string{}}
		if !h.Fresh(v.now, hsAgentStale) {
			m.State = "gone"
		}
		for _, k := range names {
			if !pol.Allows(k, h.Name) {
				m.Denied = append(m.Denied, v.rd(k))
			}
		}
		out.Machines = append(out.Machines, m)
	}
	out.Error = strings.Join(errs, " ")
	return out
}

func (v *ciView) rdAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, v.rd(s))
	}
	return out
}

// impactLine says in plain words what the current rule does to the queued requests of a project.
func impactLine(i localci.PolicyImpact, unknown bool) string {
	switch {
	case unknown:
		return "The effect on the queue is not known."
	case i.Queued == 0:
		return "No run of this project is waiting."
	case i.NoMachine == 1:
		return "1 waiting run has no machine that may run it."
	case i.NoMachine > 1:
		return fmt.Sprintf("%d waiting runs have no machine that may run them.", i.NoMachine)
	}
	return fmt.Sprintf("Every waiting run of this project (%d) has a machine that may run it.", i.Queued)
}

// ---------------------------------------------------------------- holds

// HSHold is one hold (freeze window).
type HSHold struct {
	ID         string `json:"id"`
	Project    string `json:"project"` // a project id, or "all"
	Reason     string `json:"reason"`
	Owner      string `json:"owner"`
	State      string `json:"state"` // upcoming, active, ended or lifted
	Start      string `json:"start"`
	End        string `json:"end"`
	Created    string `json:"created,omitempty"`
	LiftedBy   string `json:"liftedBy,omitempty"`
	LiftedAt   string `json:"liftedAt,omitempty"`
	LiftReason string `json:"liftReason,omitempty"`
}

// HSHolds is the answer of /api/holds.
type HSHolds struct {
	CIFound  bool     `json:"ciFound"`
	Now      string   `json:"now"`
	Active   []HSHold `json:"active"`   // Promote is refused while one of these covers the project
	Upcoming []HSHold `json:"upcoming"` // soonest start first
	Lifted   []HSHold `json:"lifted"`   // somebody ended them early, latest first
	Ended    []HSHold `json:"ended"`    // the window is over, latest first
	Error    string   `json:"error,omitempty"`
}

// Holds reads the hold store.
func (v *ciView) Holds() HSHolds {
	out := HSHolds{CIFound: v.found, Now: tstr(v.now), Active: []HSHold{}, Upcoming: []HSHold{}, Lifted: []HSHold{}, Ended: []HSHold{}}
	if !v.found {
		return out
	}
	all, err := (releasehold.Store{Dir: v.dir}).List()
	if err != nil {
		out.Error = "The holds could not be read, and Promote refuses until they can be: " + v.rd(err.Error())
		return out
	}
	type item struct {
		h  releasehold.Hold
		hs HSHold
	}
	var act, up, lift, end []item
	for _, h := range all {
		st := h.StateAt(v.now)
		it := item{h, HSHold{ID: v.rd(h.ID), Project: v.rd(h.Project), Reason: v.rd(h.Reason), Owner: v.rdPerson(h.Owner), State: st, Start: tstr(h.Start), End: tstr(h.End),
			Created: tstr(h.Created), LiftedBy: v.rdPerson(h.LiftedBy), LiftedAt: tstr(h.LiftedAt), LiftReason: v.rd(h.LiftReason)}}
		switch st {
		case releasehold.StateActive:
			act = append(act, it)
		case releasehold.StateUpcoming:
			up = append(up, it)
		case releasehold.StateLifted:
			lift = append(lift, it)
		default:
			end = append(end, it)
		}
	}
	sort.SliceStable(act, func(i, j int) bool { return act[i].h.End.Before(act[j].h.End) })
	sort.SliceStable(up, func(i, j int) bool { return up[i].h.Start.Before(up[j].h.Start) })
	sort.SliceStable(lift, func(i, j int) bool { return lift[i].h.LiftedAt.After(lift[j].h.LiftedAt) })
	sort.SliceStable(end, func(i, j int) bool { return end[i].h.End.After(end[j].h.End) })
	for _, it := range act {
		out.Active = append(out.Active, it.hs)
	}
	for _, it := range up {
		out.Upcoming = append(out.Upcoming, it.hs)
	}
	for _, it := range lift {
		out.Lifted = append(out.Lifted, it.hs)
	}
	for _, it := range end {
		out.Ended = append(out.Ended, it.hs)
	}
	return out
}

// ---------------------------------------------------------------- activity

// HSActivityEntry is one line of the activity log.
type HSActivityEntry struct {
	Time   string `json:"time"`
	Actor  string `json:"actor"` // ui, cli or claude
	Person string `json:"person,omitempty"`
	Action string `json:"action"`
	Target string `json:"target,omitempty"`
	Reason string `json:"reason,omitempty"`
	// Outcome is done, refused, failed, invalid or error; absent on entries written before actions logged it (done).
	Outcome string `json:"outcome,omitempty"`
	Detail  string `json:"detail,omitempty"` // why an attempt did not end done
}

// HSActivity is the answer of /api/activity.
type HSActivity struct {
	CIFound bool              `json:"ciFound"`
	Now     string            `json:"now"`
	Skipped int               `json:"skipped"` // lines that were not a valid entry (a torn last line after a crash)
	Order   string            `json:"order"`   // oldest-first, as unravis-ci activity --json
	Entries []HSActivityEntry `json:"entries"`
	Error   string            `json:"error,omitempty"`
}

// Activity reads the newest limit entries of the log, of one actor when actor is set.
func (v *ciView) Activity(actor string, limit int) HSActivity {
	out := HSActivity{CIFound: v.found, Now: tstr(v.now), Order: "oldest-first", Entries: []HSActivityEntry{}}
	if !v.found {
		return out
	}
	es, skipped, err := (activitylog.Log{Dir: v.dir}).Read(activitylog.Filter{Actor: actor, Limit: limit})
	out.Skipped = skipped
	if err != nil {
		out.Error = "The activity log could not be read: " + v.rd(err.Error())
		return out
	}
	for _, e := range es {
		out.Entries = append(out.Entries, HSActivityEntry{Time: tstr(e.Time), Actor: e.Actor, Person: v.rdPerson(e.Person), Action: v.rd(e.Action), Target: v.rd(e.Target), Reason: v.rd(e.Reason),
			Outcome: e.Outcome, Detail: v.rd(e.Detail)})
	}
	return out
}

// ---------------------------------------------------------------- routes

// errHSParam marks a query value the endpoints refuse.
var errHSParam = errors.New("bad query value")

// intParam reads an optional whole number in [lo, hi] from the query; the default stands when it is absent.
func intParam(r *http.Request, name string, def, lo, hi int) (int, error) {
	s := r.URL.Query().Get(name)
	if s == "" {
		return def, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < lo || n > hi {
		return 0, fmt.Errorf("%w: %s must be a whole number from %d to %d", errHSParam, name, lo, hi)
	}
	return n, nil
}

// registerHomestretch adds the four read-only endpoints. The Handler already refuses every method but GET and HEAD
// (405), a Host that is not loopback (404) and a request without the session (401).
func (d *Dash) registerHomestretch(mux *http.ServeMux, view func(http.ResponseWriter, *http.Request) (*ciView, bool)) {
	mux.HandleFunc("/api/queue", func(w http.ResponseWriter, r *http.Request) {
		v, ok := view(w, r)
		if !ok {
			return
		}
		n, err := intParam(r, "limit", hsDefaultDone, 1, hsMaxDone)
		if err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, v.Queue(n))
	})
	mux.HandleFunc("/api/policy", func(w http.ResponseWriter, r *http.Request) {
		if v, ok := view(w, r); ok {
			writeJSON(w, v.Policy())
		}
	})
	mux.HandleFunc("/api/holds", func(w http.ResponseWriter, r *http.Request) {
		if v, ok := view(w, r); ok {
			writeJSON(w, v.Holds())
		}
	})
	mux.HandleFunc("/api/activity", func(w http.ResponseWriter, r *http.Request) {
		v, ok := view(w, r)
		if !ok {
			return
		}
		actor := r.URL.Query().Get("actor")
		if actor != "" && !activitylog.ValidActor(actor) {
			fail(w, http.StatusBadRequest, "actor must be one of "+strings.Join(activitylog.Actors, ", ")+".")
			return
		}
		n, err := intParam(r, "limit", hsDefaultActivity, 1, hsMaxActivity)
		if err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, v.Activity(actor, n))
	})
}

// rdPerson hides secrets in a person's name (a hold's owner, who lifted it, who did an action) but keeps the name itself. The
// design shows who owns a hold and who acted; that is the point of the record, and the CLI prints it too. The machine and user
// names the dashboard hides elsewhere are for log text, where they leak by accident; here they are the data.
func (v *ciView) rdPerson(s string) string {
	if s == "" {
		return ""
	}
	return NewRedactor().Line(s)
}
