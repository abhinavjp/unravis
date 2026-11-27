package localci

import (
	"strings"
	"testing"
	"time"
)

func whyInput(agents ...Heartbeat) WaitInput {
	now := time.Now()
	for i := range agents {
		if agents[i].LastSeen.IsZero() {
			agents[i].LastSeen = now
		}
		if agents[i].Slots == 0 {
			agents[i].Slots = 1
		}
		if agents[i].State == "" {
			agents[i].State = "ready"
		}
	}
	return WaitInput{Now: now, Stale: time.Minute, Agents: agents, Policy: &Policy{}, DrainMarks: map[string]bool{}}
}

func queuedReq() Request {
	r := prReq(1, strings.Repeat("a", 40))
	r.State = StateQueued
	return r
}

func wantWhy(t *testing.T, got WaitReason, code string, machines ...string) {
	t.Helper()
	if got.Code != code {
		t.Fatalf("code %q (%s), want %q", got.Code, got.Text, code)
	}
	if strings.Join(got.Machines, ",") != strings.Join(machines, ",") {
		t.Fatalf("machines %v, want %v", got.Machines, machines)
	}
	if got.Text == "" {
		t.Fatal("every reason has a plain line")
	}
}

func TestWhyWaitingNotQueued(t *testing.T) {
	r := queuedReq()
	r.State = StateRunning
	wantWhy(t, WhyWaiting(r, whyInput(Heartbeat{Name: "m1"})), WaitNotQueued)
}

func TestWhyWaitingPaused(t *testing.T) {
	in := whyInput(Heartbeat{Name: "m1"})
	in.Paused, in.PausedReason = true, "the pause file exists"
	got := WhyWaiting(queuedReq(), in)
	wantWhy(t, got, WaitPaused)
	if !strings.Contains(got.Text, "pause file") {
		t.Fatalf("the reason is shown: %q", got.Text)
	}
}

func TestWhyWaitingDeferred(t *testing.T) {
	r := queuedReq()
	in := whyInput(Heartbeat{Name: "m1"})
	r.DeferUntil, r.DeferNote = in.Now.Add(30*time.Second), "another PC holds it"
	got := WhyWaiting(r, in)
	wantWhy(t, got, WaitDeferred)
	if !strings.Contains(got.Text, "another PC holds it") {
		t.Fatalf("the note is shown: %q", got.Text)
	}
	// A defer time in the past no longer counts.
	r.DeferUntil = in.Now.Add(-time.Second)
	wantWhy(t, WhyWaiting(r, in), WaitReady, "m1")
}

func TestWhyWaitingNoMachine(t *testing.T) {
	wantWhy(t, WhyWaiting(queuedReq(), whyInput()), WaitNoMachine)
}

func TestWhyWaitingNoLabels(t *testing.T) {
	r := queuedReq()
	r.Labels = []string{"gpu"}
	got := WhyWaiting(r, whyInput(Heartbeat{Name: "m1", Labels: []string{"win"}}))
	wantWhy(t, got, WaitNoLabels)
	if !strings.Contains(got.Text, "gpu") {
		t.Fatalf("the labels are named: %q", got.Text)
	}
}

func TestWhyWaitingDenied(t *testing.T) {
	in := whyInput(Heartbeat{Name: "m2"}, Heartbeat{Name: "m1"}, Heartbeat{Name: "m3", Labels: nil})
	r := queuedReq()
	r.Labels = nil
	in.Policy = &Policy{Projects: map[string]ProjectPolicy{"o/r": {Mode: PolicyOnly, Machines: []string{"m9"}}}}
	wantWhy(t, WhyWaiting(r, in), WaitDenied, "m1", "m2", "m3")
	// A machine that lacks the labels is not counted as denied.
	r.Labels = []string{"gpu"}
	in = whyInput(Heartbeat{Name: "m1", Labels: []string{"gpu"}}, Heartbeat{Name: "m2"})
	in.Policy = &Policy{Projects: map[string]ProjectPolicy{"o/r": {Mode: PolicyExcept, Machines: []string{"m1"}}}}
	wantWhy(t, WhyWaiting(r, in), WaitDenied, "m1")
}

func TestWhyWaitingBusy(t *testing.T) {
	in := whyInput(Heartbeat{Name: "m1", Slots: 1, Jobs: []AgentJob{{}}}, Heartbeat{Name: "m2", State: "draining"})
	wantWhy(t, WhyWaiting(queuedReq(), in), WaitBusy, "m1")
}

func TestWhyWaitingDraining(t *testing.T) {
	in := whyInput(Heartbeat{Name: "m1", State: "draining"}, Heartbeat{Name: "m2", State: "low-disk"}, Heartbeat{Name: "m3"})
	in.DrainMarks["m3"] = true // asked to drain; its next heartbeat says so
	got := WhyWaiting(queuedReq(), in)
	wantWhy(t, got, WaitDraining, "m1", "m2", "m3")
	if !strings.Contains(got.Text, "low on disk") {
		t.Fatalf("each machine says why: %q", got.Text)
	}
}

func TestWhyWaitingOffline(t *testing.T) {
	in := whyInput(Heartbeat{Name: "m1"})
	in.Agents[0].LastSeen = in.Now.Add(-time.Hour)
	wantWhy(t, WhyWaiting(queuedReq(), in), WaitOffline, "m1")
}

func TestWhyWaitingReady(t *testing.T) {
	in := whyInput(Heartbeat{Name: "m1", Slots: 1, Jobs: []AgentJob{{}}}, Heartbeat{Name: "m2", Slots: 2, Jobs: []AgentJob{{}}})
	wantWhy(t, WhyWaiting(queuedReq(), in), WaitReady, "m2")
}

func TestWhyWaitingOrder(t *testing.T) {
	// Paused wins over everything else; denied wins over busy machines that the rule does not allow.
	in := whyInput(Heartbeat{Name: "m1", Slots: 1, Jobs: []AgentJob{{}}})
	in.Policy = &Policy{Projects: map[string]ProjectPolicy{"o/r": {Mode: PolicyExcept, Machines: []string{"m1"}}}}
	wantWhy(t, WhyWaiting(queuedReq(), in), WaitDenied, "m1")
	in.Paused = true
	wantWhy(t, WhyWaiting(queuedReq(), in), WaitPaused)
}

func TestWaitInputOfReadsTheStateFolder(t *testing.T) {
	dir, q := policyRig(t)
	writeBeat(t, dir, Heartbeat{Name: "m1"})
	r := mustEnqueue(t, q, prReq(1, strings.Repeat("a", 40)))
	in, err := WaitInputOf(dir, time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	wantWhy(t, WhyWaiting(r, in), WaitReady, "m1")

	setPolicy(t, dir, "o/r", PolicyExcept, "m1")
	if err := RequestDrain(dir, "m1"); err != nil {
		t.Fatal(err)
	}
	in, err = WaitInputOf(dir, time.Now(), time.Minute)
	if err != nil || !in.DrainMarks["m1"] {
		t.Fatalf("drain marks are read: %+v %v", in.DrainMarks, err)
	}
	wantWhy(t, WhyWaiting(r, in), WaitDenied, "m1")

	if err := SetPaused(dir, true); err != nil {
		t.Fatal(err)
	}
	in, _ = WaitInputOf(dir, time.Now(), time.Minute)
	wantWhy(t, WhyWaiting(r, in), WaitPaused)
}
