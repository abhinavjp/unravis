package mcpagent_test

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"api-audit-explorer/internal/mcp"
	"api-audit-explorer/internal/mcpsrv"
)

// BL-574 slice 2b: the two code-mode tools go through the agent to the dispatcher like any tool, and what they run with is the stored
// grant, never the arguments.

// echoCode is a code backend that says what it was given.
type echoCode struct{}

func (echoCode) CallCode(_ context.Context, tool string, pr mcpsrv.Principal, p mcpsrv.Profile, args []byte, _ time.Time) ([]byte, bool, bool) {
	body, _ := json.Marshal(map[string]any{"tool": tool, "profile": pr.ProfileID, "tier": p.Tier, "tools": p.Tools, "sources": p.Sources, "args": json.RawMessage(args)})
	return body, false, false
}

func TestCodeToolsReachTheDispatcherThroughTheAgent(t *testing.T) {
	r := newRigWith(t, echoCode{})
	a := r.agent("code-1")
	// Before approval the code tools are refused like every other tool: the grant is the gate.
	if e := call(t, a, mcp.ToolTypes); !e.isError || code(e) != "permission_required" {
		t.Fatalf("before approval: %.300s", e.body)
	}
	r.approveWith("code-1", map[string]any{"code": true})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// A caller that names its own scope in the arguments still gets the grant's: the backend is given the stored profile.
	b, isErr := a.CallEnvelope(ctx, mcp.ToolExecute, json.RawMessage(`{"code":"1","tier":"evidence","tools":["audit_get_requests"]}`))
	var got struct {
		Tool    string
		Profile string
		Tier    string
		Tools   []string
		Sources []string
		Args    map[string]any
	}
	if isErr || json.Unmarshal(b, &got) != nil || got.Tool != mcp.ToolExecute || got.Profile != "code-1" {
		t.Fatalf("execute: %v %.300s", isErr, b)
	}
	if got.Tier != mcpsrv.TierAggregate || slices.Contains(got.Tools, "audit_get_requests") || len(got.Sources) == 0 {
		t.Errorf("the backend was given %q %v %v", got.Tier, got.Tools, got.Sources)
	}
	if got.Args["tier"] != "evidence" {
		t.Errorf("the arguments were changed on the way: %v", got.Args)
	}
	if e := call(t, a, mcp.ToolTypes); e.isError || e.m["tool"] != mcp.ToolTypes {
		t.Errorf("types: %.300s", e.body)
	}
	// The gate is the two names and nothing else.
	if e := call(t, a, "unravis_delete_everything"); !e.isError || code(e) != mcp.WireInvalidArguments {
		t.Errorf("unknown tool: %.300s", e.body)
	}
}

// A build with no code-mode broker answers both tools as "code mode is off", and nothing else.
func TestCodeToolsAreOffWhenThereIsNoBackend(t *testing.T) {
	r := newRig(t)
	a := r.agent("code-2")
	_ = call(t, a, mcp.ToolTypes)
	r.approve("code-2")
	for _, tool := range []string{mcp.ToolTypes, mcp.ToolExecute} {
		e := call(t, a, tool)
		if !e.isError || code(e) != "code_mode_off" {
			t.Errorf("%s: %.300s", tool, e.body)
		}
	}
}

// A grant approved with the card's defaults (no code mode) is refused both tools, though a broker is mounted (BL-574 M1).
func TestCodeToolsNeedTheGrantsOwnCodePermission(t *testing.T) {
	r := newRigWith(t, echoCode{})
	a := r.agent("code-3")
	_ = call(t, a, mcp.ToolTypes)
	r.approve("code-3")
	for _, tool := range []string{mcp.ToolTypes, mcp.ToolExecute} {
		e := call(t, a, tool)
		if !e.isError || code(e) != "denied" {
			t.Errorf("%s: %.300s", tool, e.body)
		}
	}
}
