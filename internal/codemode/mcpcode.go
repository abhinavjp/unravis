package codemode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"time"

	"api-audit-explorer/internal/codemode/bindings"
	"api-audit-explorer/internal/codemode/tsgen"
	"api-audit-explorer/internal/mcp"
	"api-audit-explorer/internal/mcpsrv"
)

// BL-574 slice 2b: the two MCP tools of code mode (unravis_types, unravis_execute) over a Broker. MCPCode is the mcpsrv.CodeBackend the
// dispatcher calls for a host with a grant. It lives here because this package already imports mcpsrv (the other direction would be a cycle).
//
// What a script may reach is decided here, and only from the stored profile the dispatcher read (Scope): the tier, the tool list and the
// source scope. The arguments of the call carry a script, a time limit, a reason and a case, and nothing else: a field that is not one of
// those four is refused, so a caller cannot name its own sources, tier or tools, and the broker's own refusals (a scope with no known tier,
// the surface mcp without a scope) stay in force under it.

// MCPCode serves unravis_types and unravis_execute for the grants of the host.
type MCPCode struct {
	Broker *Broker
	// Leases opens the scope in which the case resolver may take case handles, and gives them back (codeapi.WithLeases). Nil: the resolver
	// needs none (a test's).
	Leases func(ctx context.Context) (context.Context, func())
	// ManyOpen says, after a run was refused with no_case, that the reason was more than one open case (codeapi.ManyOpen). Nil: never.
	ManyOpen func(ctx context.Context) bool
}

var _ mcpsrv.CodeBackend = (*MCPCode)(nil)

// executeArgs are the only arguments of unravis_execute.
type executeArgs struct {
	Code      string `json:"code"`
	TimeoutMS int    `json:"timeout_ms"`
	Reason    string `json:"reason"`
	CaseID    string `json:"case_id"`
}

// maxExecuteAnswer is the largest answer body sent to a host: the envelope goes out twice (structured and text) inside mcp.FrameBudget.
const maxExecuteAnswer = mcp.FrameBudget / 2

const noToolsMessage = "This access allows no tools a script could call."

// PlainMessage is the fixed text of a code-mode error code, or "" for a code it does not know.
func PlainMessage(code string) string { return plain[code] }

func refusalBody(code, message string) []byte { return mcpsrv.CodeRefusalBody(code, message) }

// CallCode runs tool for the grant. See mcpsrv.CodeBackend.
func (m *MCPCode) CallCode(ctx context.Context, tool string, pr mcpsrv.Principal, p mcpsrv.Profile, args []byte, now time.Time) ([]byte, bool, bool) {
	if m == nil || m.Broker == nil {
		return refusalBody(CodeModeOff, plain[CodeModeOff]), true, false
	}
	switch tool {
	case mcp.ToolTypes:
		body, isErr := m.types(p, args)
		return body, isErr, false
	case mcp.ToolExecute:
		return m.execute(ctx, pr, p, args, now)
	}
	return refusalBody(CodeBadRequest, plain[CodeBadRequest]), true, false
}

// settingsOn is the owner's switch (code-mode.json), read now.
func (m *MCPCode) settingsOn() bool {
	b := m.Broker
	return b.cfg.Settings != nil && b.cfg.Settings().Enabled
}

// types answers unravis_types: the declarations of the tools of the grant (its tier and tool list) and nothing of the others.
func (m *MCPCode) types(p mcpsrv.Profile, args []byte) ([]byte, bool) {
	if !m.settingsOn() {
		return refusalBody(CodeModeOff, plain[CodeModeOff]), true
	}
	if trimmed := bytes.TrimSpace(args); len(trimmed) > 0 && string(trimmed) != "null" {
		var none map[string]json.RawMessage
		if err := decodeStrict(args, &none); err != nil || len(none) != 0 {
			return refusalBody(CodeBadRequest, plain[CodeBadRequest]), true
		}
	}
	allowed := mcpsrv.GrantTools(p.Tier, p.Tools)
	if len(allowed) == 0 || len(p.Sources) == 0 {
		return refusalBody(CodeDenied, noToolsMessage), true
	}
	spec := bindings.Spec()
	spec.Tools = slices.DeleteFunc(slices.Clone(spec.Tools), func(t tsgen.Tool) bool { return !slices.Contains(allowed, t.Name) })
	if len(spec.Tools) == 0 {
		return refusalBody(CodeInternal, plain[CodeInternal]), true
	}
	res, err := tsgen.Generate(spec)
	if err != nil {
		return refusalBody(CodeInternal, plain[CodeInternal]), true
	}
	names := make([]string, 0, len(spec.Tools))
	for _, t := range spec.Tools {
		names = append(names, t.Name)
	}
	body, err := json.Marshal(map[string]any{
		"namespace": bindings.Namespace, "dts": res.DTS, "schemaSha256": res.SchemaSHA256,
		"tier": p.Tier, "tools": names, "sources": slices.Clone(p.Sources),
		"limits": map[string]any{
			"maxInFlight": MaxInFlight, "maxCalls": MaxCalls, "maxResultBytes": MaxResultBytes, "maxLogBytes": MaxLogBytes,
			"runCapSeconds": int(RunCap.Seconds()), "maxSourceBytes": MaxSource,
			"defaultTimeoutMs": DefaultTimeout.Milliseconds(), "minTimeoutMs": MinTimeout.Milliseconds(), "maxTimeoutMs": MaxTimeout.Milliseconds(),
		},
	})
	if err != nil {
		return refusalBody(CodeInternal, plain[CodeInternal]), true
	}
	return body, false
}

// execute answers unravis_execute: it runs the script through the broker as the grant, with the grant's scope taken from the profile.
func (m *MCPCode) execute(ctx context.Context, pr mcpsrv.Principal, p mcpsrv.Profile, raw []byte, now time.Time) ([]byte, bool, bool) {
	var in executeArgs
	if err := decodeStrict(raw, &in); err != nil || in.TimeoutMS < 0 || len(in.CaseID) > mcp.ExecuteMaxCaseID {
		return refusalBody(CodeBadRequest, plain[CodeBadRequest]), true, false
	}
	// A grant that names no tool a script could call, or no source, runs nothing (the same refusal as unravis_types; the approval card
	// does not allow either, so this is the stored profile having been damaged).
	if len(mcpsrv.GrantTools(p.Tier, p.Tools)) == 0 || len(p.Sources) == 0 {
		return refusalBody(CodeDenied, noToolsMessage), true, false
	}
	// Identifiers need the grant's own identifiers permission now AND the operator's switch; the broker refuses a request that asks for
	// them without the second, so the flag is only ever set when both hold.
	withIdentifiers := m.Broker.cfg.AllowIdentifiers && mcpsrv.IdentifiersAllowed(p, now)
	req := RunRequest{
		Code: in.Code, CaseID: in.CaseID, TimeoutMS: in.TimeoutMS, Reason: in.Reason,
		Surface: "mcp", CallerID: p.ID, CallerDeclared: false, AllowIdentifiers: withIdentifiers,
		Scoped: true, Sources: slices.Clone(p.Sources), Tier: p.Tier, Tools: slices.Clone(p.Tools),
		ProfileID: pr.ProfileID, ProfileRev: pr.Rev,
	}
	if m.Leases != nil {
		var release func()
		ctx, release = m.Leases(ctx)
		defer release()
	}
	res, err := m.Broker.Run(ctx, req)
	if err != nil {
		var ref *Refusal
		if !errors.As(err, &ref) {
			return refusalBody(CodeInternal, plain[CodeInternal]), true, false
		}
		msg := ref.Message
		if ref.Code == CodeNoCase && m.ManyOpen != nil && m.ManyOpen(ctx) {
			msg = "More than one case is open. Name the case with case_id."
		}
		return refusalBody(ref.Code, msg), true, false
	}
	body, err := json.Marshal(res)
	if err != nil {
		return refusalBody(CodeInternal, plain[CodeInternal]), true, withIdentifiers
	}
	if len(body) > maxExecuteAnswer {
		// The run happened and is recorded; its answer is too big for one message. Say so, with the run id, and send no part of it.
		big, _ := json.Marshal(map[string]any{"runId": res.RunID, "status": res.Status, "error": map[string]string{
			"code": CodeOutputTooLarge, "message": "The answer of the run is too big to send here (the run is recorded in the case). Return a smaller summary.",
		}})
		return big, true, withIdentifiers
	}
	return body, res.Error != nil, withIdentifiers
}

// decodeStrict decodes one JSON value into v: no unknown field and nothing after it. Empty input is an empty object.
func decodeStrict(raw []byte, v any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("trailing data")
	}
	return nil
}
