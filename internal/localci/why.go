package localci

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Why a queued request waits, computed in one place so that the command line, the read API and the screens say
// the same thing. The reasons are checked in this order and the first that holds is the answer:
//
//	(hold on the project: a later round adds it here, before paused)
//	paused     claiming is paused for the state folder (the PAUSE file)
//	deferred   another PC runs it; this PC looks again later
//	no-machine no machine is known in this state folder
//	no-labels  no known machine has the labels the request needs
//	denied     machines with the labels exist, but the project's policy allows none of them
//	busy       allowed machines with the labels are all running as much as they may
//	draining   allowed machines with the labels are draining, paused on their own, or low on disk
//	offline    allowed machines with the labels have not been heard from
//	ready      an allowed machine with the labels is free: it takes the request at its next look

// Why-waiting codes.
const (
	WaitPaused    = "paused"
	WaitDeferred  = "deferred"
	WaitNoMachine = "no-machine"
	WaitNoLabels  = "no-labels"
	WaitDenied    = "denied"
	WaitBusy      = "busy"
	WaitDraining  = "draining"
	WaitOffline   = "offline"
	WaitReady     = "ready"
	WaitNotQueued = "not-queued"
)

// WaitReason is why one queued request has not started.
type WaitReason struct {
	Code string `json:"code"`
	Text string `json:"text"` // one plain line
	// Machines are the machines the reason is about (the denied, busy, draining or offline ones), by name.
	Machines []string `json:"machines,omitempty"`
}

// WaitInput is what WhyWaiting looks at. It holds no file access, so it can be filled from the state folder
// (WaitInputOf) or by a test.
type WaitInput struct {
	Now          time.Time
	Stale        time.Duration // a heartbeat older than this is an offline machine
	Paused       bool
	PausedReason string
	Agents       []Heartbeat
	DrainMarks   map[string]bool // agents with a pending drain mark
	Policy       *Policy
}

// WaitInputOf reads the state folder for WhyWaiting: the heartbeats, the drain marks, the pause file and the
// policy. A policy that cannot be read is an error: the agents take nothing then, and saying anything else
// would be wrong.
func WaitInputOf(stateDir string, now time.Time, stale time.Duration) (WaitInput, error) {
	hbs, err := ReadAgents(stateDir)
	if err != nil {
		return WaitInput{}, err
	}
	pol, err := LoadPolicy(stateDir)
	if err != nil {
		return WaitInput{}, err
	}
	in := WaitInput{Now: now, Stale: stale, Agents: hbs, Policy: pol, DrainMarks: map[string]bool{}}
	if IsPaused(stateDir) {
		in.Paused, in.PausedReason = true, "the pause file "+PausePath(stateDir)+" exists"
	}
	for _, h := range hbs {
		if DrainRequested(stateDir, h.Name) {
			in.DrainMarks[h.Name] = true
		}
	}
	return in, nil
}

// WhyWaiting says why a queued request has not started yet.
func WhyWaiting(r Request, in WaitInput) WaitReason {
	if r.State != StateQueued && r.State != "" {
		return WaitReason{Code: WaitNotQueued, Text: "it is not waiting (" + r.State + ")"}
	}
	if in.Paused {
		t := "claiming is paused"
		if in.PausedReason != "" {
			t += ": " + in.PausedReason
		}
		return WaitReason{Code: WaitPaused, Text: t + "; resume the queue to let machines take it"}
	}
	if r.DeferUntil.After(in.Now) {
		t := "another PC is running it; this PC looks again in " + r.DeferUntil.Sub(in.Now).Round(time.Second).String()
		if r.DeferNote != "" {
			t = r.DeferNote + "; looking again in " + r.DeferUntil.Sub(in.Now).Round(time.Second).String()
		}
		return WaitReason{Code: WaitDeferred, Text: t}
	}
	if len(in.Agents) == 0 {
		return WaitReason{Code: WaitNoMachine, Text: "no machine is known here; start an agent (the serve command)"}
	}
	var fit []Heartbeat
	for _, h := range in.Agents {
		if hasLabels(h.Labels, r.Labels) {
			fit = append(fit, h)
		}
	}
	if len(fit) == 0 {
		return WaitReason{Code: WaitNoLabels, Text: "no machine has the labels it needs (" + strings.Join(r.Labels, ", ") + ")"}
	}
	rule := in.Policy.For(r.Repo)
	var allowed []Heartbeat
	var denied []string
	for _, h := range fit {
		if rule.Allows(h.Name) {
			allowed = append(allowed, h)
		} else {
			denied = append(denied, h.Name)
		}
	}
	if len(allowed) == 0 {
		return WaitReason{Code: WaitDenied, Machines: sorted(denied),
			Text: fmt.Sprintf("the machines that fit (%s) are not allowed to run %s; its policy is %s", strings.Join(sorted(denied), ", "), r.Repo, rule.Describe())}
	}
	var busy, draining, drainWhy, offline []string
	for _, h := range allowed {
		switch {
		case !h.Fresh(in.Now, in.Stale):
			offline = append(offline, h.Name)
		case h.State == "draining" || h.State == "paused" || h.State == "low-disk" || in.DrainMarks[h.Name]:
			draining = append(draining, h.Name)
			drainWhy = append(drainWhy, h.Name+" ("+notTakingWhy(h, in.DrainMarks[h.Name])+")")
		case h.Slots > 0 && len(h.Jobs) >= h.Slots:
			busy = append(busy, h.Name)
		default:
			return WaitReason{Code: WaitReady, Machines: []string{h.Name}, Text: "next in line: " + h.Name + " is free and takes it at its next look"}
		}
	}
	switch {
	case len(busy) > 0:
		return WaitReason{Code: WaitBusy, Machines: sorted(busy), Text: "the machines that may run it are busy (" + strings.Join(sorted(busy), ", ") + "); it starts when one is free"}
	case len(draining) > 0:
		return WaitReason{Code: WaitDraining, Machines: sorted(draining), Text: "the machines that may run it take nothing new: " + strings.Join(sorted(drainWhy), ", ")}
	}
	return WaitReason{Code: WaitOffline, Machines: sorted(offline), Text: "the machines that may run it have not been heard from (" + strings.Join(sorted(offline), ", ") + ")"}
}

func notTakingWhy(h Heartbeat, marked bool) string {
	switch {
	case marked || h.State == "draining":
		return "draining"
	case h.State == "low-disk":
		return "low on disk space"
	}
	return "paused"
}

func sorted(s []string) []string {
	out := append([]string{}, s...)
	sort.Strings(out)
	return out
}
