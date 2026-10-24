package server

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
)

// L7: an erase cannot be overtaken by a read that opened the case first.
// Every id here is synthetic.

func gateID(c string) casestore.CaseID { return casestore.CaseID(strings.Repeat(c, 26)) }

func TestEraseWaitsForReadsInFlight(t *testing.T) {
	var g caseGates
	id := gateID("a")
	read, ok := g.acquireRead(id)
	if !ok {
		t.Fatal("read refused with no erase pending")
	}
	got := make(chan error, 1)
	var done func()
	go func() {
		d, err := g.acquireErase(context.Background(), id)
		done = d
		got <- err
	}()
	select {
	case err := <-got:
		t.Fatalf("erase finished while a read was open: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	read()
	select {
	case err := <-got:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("erase did not start after the read finished")
	}
	done()
	g.mu.Lock()
	left := len(g.m)
	g.mu.Unlock()
	if left != 0 {
		t.Fatalf("%d gates leaked", left)
	}
}

func TestReadStartedDuringEraseIsRefused(t *testing.T) {
	var g caseGates
	id := gateID("b")
	first, _ := g.acquireRead(id)
	started := make(chan struct{})
	got := make(chan func(), 1)
	go func() {
		close(started)
		d, err := g.acquireErase(context.Background(), id)
		if err != nil {
			got <- nil
			return
		}
		got <- d
	}()
	<-started
	// Wait until the erase has marked the gate.
	deadline := time.Now().Add(2 * time.Second)
	for {
		g.mu.Lock()
		erasing := g.m[id] != nil && g.m[id].erasing
		g.mu.Unlock()
		if erasing {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("erase never marked the gate")
		}
		time.Sleep(time.Millisecond)
	}
	if _, ok := g.acquireRead(id); ok {
		t.Fatal("a read started while an erase was pending")
	}
	// Other cases are not affected.
	other, ok := g.acquireRead(gateID("c"))
	if !ok {
		t.Fatal("a read of another case was refused")
	}
	other()
	first()
	done := <-got
	if done == nil {
		t.Fatal("erase failed")
	}
	done()
	// After the erase the case reads again (it may simply be gone).
	again, ok := g.acquireRead(id)
	if !ok {
		t.Fatal("read refused after the erase ended")
	}
	again()
}

func TestEraseGivesUpWhenContextEnds(t *testing.T) {
	var g caseGates
	id := gateID("d")
	read, _ := g.acquireRead(id)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := g.acquireErase(ctx, id); err == nil {
		t.Fatal("erase returned while a read was open")
	}
	// The gate is usable again.
	read()
	again, err := g.acquireErase(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	again()
	g.mu.Lock()
	left := len(g.m)
	g.mu.Unlock()
	if left != 0 {
		t.Fatalf("%d gates leaked", left)
	}
}

func TestReadsOfAnErasingCaseAnswer404(t *testing.T) {
	e := newCaseEnv(t, envConfig{})
	id := e.newCase("Case A")
	if err := id2gate(e, id); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/api/cases/" + id + "/imports",
		"/api/cases/" + id + "/events",
	} {
		res := e.get(path)
		if res.Code != http.StatusNotFound {
			t.Errorf("GET %s during an erase = %d, want 404", path, res.Code)
		}
	}
}

// id2gate leaves the case's gate marked as erasing, as an erase waiting on a
// reader would, so the next read must be refused.
func id2gate(e *caseEnv, id string) error {
	h := e.h.(*handler)
	read, _ := h.gates.acquireRead(casestore.CaseID(id))
	go func() { _, _ = h.gates.acquireErase(context.Background(), casestore.CaseID(id)) }()
	for i := 0; i < 2000; i++ {
		h.gates.mu.Lock()
		g := h.gates.m[casestore.CaseID(id)]
		erasing := g != nil && g.erasing
		h.gates.mu.Unlock()
		if erasing {
			_ = read // held on purpose: the erase stays pending
			return nil
		}
		time.Sleep(time.Millisecond)
	}
	return context.DeadlineExceeded
}
