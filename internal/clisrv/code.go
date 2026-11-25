package clisrv

import (
	"context"
	"encoding/json"
	"errors"
	"unicode/utf8"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/clicore"
)

// code.types and code.run (BL-371): the command line reaches the code-mode broker through the owner, the way the app's window reaches it
// through /api/code/*. Only the person at the command line may call them (internal/clicap). The broker and its audit record are the
// same; the run is recorded with the surface "cli", it is not scoped and it carries no grant.

// Code is the code-mode broker as the owner pipe needs it. nil in Deps: the two operations answer ENDPOINT_UNAVAILABLE.
type Code interface {
	// Types answers the same document as GET /api/code/types (the typings, their hash and the limits). The value is encoded as it is.
	Types(ctx context.Context) (any, error)
	// Run runs one script. A request the broker turns away before it records anything is a *CodeRefusal. A run that was accepted comes back
	// as its result (the broker's RunResult, also when the script failed: its status says how it ended). Any other error is internal.
	Run(ctx context.Context, in CodeRunInput) (any, error)
}

// CodeRunInput is what the person asked for. The surface and the caller are not here: the owner sets them ("cli"), nothing in a request
// can name another.
type CodeRunInput struct {
	Code             string
	CaseID           string
	TimeoutMS        int
	Reason           string
	AllowIdentifiers bool
}

// CodeRefusal is a refusal of the broker (code_mode_off, no_case, busy, ...). Code is the broker's own code, or clicore.CodeModeManyCases.
type CodeRefusal struct{ Code string }

func (r *CodeRefusal) Error() string { return "code mode refused the run: " + r.Code }

// maxReasonRunes is the longest reason the command line sends; the broker cuts it again (casestore.MaxRunReason).
const maxReasonRunes = 500

func (s *server) codeTypes(ctx context.Context, args json.RawMessage) (any, *clicore.Error) {
	var none struct{}
	if e := decodeStrict(args, &none); e != nil {
		return nil, e
	}
	if s.d.Code == nil {
		return nil, codeUnavailable()
	}
	data, err := s.d.Code.Types(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctxError(ctx)
		}
		return nil, clicore.NewErrorAfterWork(clicore.CodeInternal, "The code-mode typings could not be read.", "")
	}
	return data, nil
}

func codeUnavailable() *clicore.Error {
	return clicore.NewError(clicore.CodeEndpointUnavailable, "Code mode is not available in this app.", "Update Unravis.")
}

func (s *server) codeRun(ctx context.Context, args json.RawMessage) (any, *clicore.Error) {
	var in struct {
		Code             string `json:"code"`
		CaseID           string `json:"caseId"`
		TimeoutMS        int    `json:"timeoutMs"`
		Reason           string `json:"reason"`
		AllowIdentifiers bool   `json:"allowIdentifiers"`
	}
	if e := decodeStrict(args, &in); e != nil {
		return nil, e
	}
	switch {
	case in.Code == "":
		return nil, clicore.NewError(clicore.CodeInvalidArgument, "There is no script to run.", "Give the script as a file, as - to read it from the input, or with --code TEXT.")
	case len(in.Code) > clicore.MaxCodeSource:
		return nil, clicore.CodeModeError("source_too_large")
	case in.CaseID != "" && !validCaseID(in.CaseID):
		return nil, clicore.NewError(clicore.CodeInvalidArgument, "That is not a case id (26 characters).", "Run 'unravis cases list' to see the ids.")
	case in.TimeoutMS < 0 || in.TimeoutMS > clicore.MaxCodeTimeoutMS:
		return nil, clicore.NewError(clicore.CodeInvalidArgument, "timeoutMs must be between 0 and 120000.", "")
	case utf8.RuneCountInString(in.Reason) > maxReasonRunes:
		return nil, clicore.NewError(clicore.CodeInvalidArgument, "The reason is longer than 500 characters.", "Make it shorter.")
	}
	if s.d.Code == nil {
		return nil, codeUnavailable()
	}
	res, err := s.d.Code.Run(ctx, CodeRunInput{Code: in.Code, CaseID: in.CaseID, TimeoutMS: in.TimeoutMS, Reason: in.Reason, AllowIdentifiers: in.AllowIdentifiers})
	var ref *CodeRefusal
	switch {
	case errors.As(err, &ref):
		return nil, clicore.CodeModeError(ref.Code)
	case ctx.Err() != nil:
		// The call's own deadline or a cancel ended the run, and the broker has already ended the script. The record in the case says
		// it was cancelled; the command line says why.
		return nil, ctxError(ctx)
	case err != nil:
		return nil, clicore.NewErrorAfterWork(clicore.CodeInternal, "The app hit an internal problem and stopped the run. The app is unaffected.", "Run the script again.")
	}
	return res, nil
}

// codeMeta is the meta of an accepted run. The run says itself whether identifiers were hidden; a result or a log cut at its limit
// makes the answer partial.
func codeMeta(raw []byte, m clicore.Meta) clicore.Meta {
	var probe struct {
		Identifiers string `json:"identifiers"`
		Stats       struct {
			Truncated     bool `json:"truncated"`
			LogsTruncated bool `json:"logsTruncated"`
		} `json:"stats"`
	}
	if json.Unmarshal(raw, &probe) != nil {
		return m
	}
	if probe.Identifiers == "hidden" || probe.Identifiers == "shown" {
		m.Identifiers = probe.Identifiers
	}
	if probe.Stats.Truncated {
		m.Coverage = "partial"
		m.Truncated = "result"
		m.Warnings = append(m.Warnings, "The script's result was cut at its limit.")
	}
	if probe.Stats.LogsTruncated {
		m.Coverage = "partial"
		if m.Truncated == nil {
			m.Truncated = "logs"
		}
		m.Warnings = append(m.Warnings, "The script's log lines were cut at their limit.")
	}
	return m
}

func validCaseID(id string) bool {
	_, err := casestore.ParseID(id)
	return err == nil
}
