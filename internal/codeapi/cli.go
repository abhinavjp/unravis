package codeapi

import (
	"context"
	"errors"
	"sync"

	"api-audit-explorer/internal/clicore"
	"api-audit-explorer/internal/clisrv"
	"api-audit-explorer/internal/codemode"
)

// The command line's way into code mode (BL-371): the owner pipe's code.types and code.run (internal/clisrv) over the same Broker the
// /api/code routes use, so a run from the console goes through the same checks, the same sandbox and the same audit record as one from the
// app's window. Who runs: the person at the console. The run is unscoped, has no grant and no tier, and is recorded with the surface "cli".

// cliCaller is the caller id of a run from the command line (the same text as the surface, as the routes do for a request that says it is
// the command line).
const cliCaller = "cli"

type cliEntrance struct {
	b         *codemode.Broker
	typesOnce sync.Once
	types     typesDTO
	typesErr  error
}

// CLI returns the code-mode entrance for clisrv.Deps.Code. A nil broker (code mode is not built into this app) gives nil, so the operations
// answer ENDPOINT_UNAVAILABLE.
func CLI(b *codemode.Broker) clisrv.Code {
	if b == nil {
		return nil
	}
	return &cliEntrance{b: b}
}

// Types answers the document of GET /api/code/types.
func (c *cliEntrance) Types(context.Context) (any, error) {
	c.typesOnce.Do(func() { c.types, c.typesErr = buildTypes() })
	return c.types, c.typesErr
}

// Run runs one script. A refusal of the broker comes back as a *clisrv.CodeRefusal; an accepted run comes back as its RunResult.
func (c *cliEntrance) Run(ctx context.Context, in clisrv.CodeRunInput) (any, error) {
	// Every case handle the resolver takes lives until the run is over, and is let go here, whatever way Run ended.
	ctx, release := WithLeases(ctx)
	defer release()
	res, err := c.b.Run(ctx, codemode.RunRequest{
		Code: in.Code, CaseID: in.CaseID, TimeoutMS: in.TimeoutMS, Reason: in.Reason, Surface: "cli", CallerID: cliCaller, AllowIdentifiers: in.AllowIdentifiers,
	})
	if err != nil {
		var ref *codemode.Refusal
		if !errors.As(err, &ref) {
			return nil, err
		}
		code := ref.Code
		if code == codemode.CodeNoCase && ManyOpen(ctx) {
			code = clicore.CodeModeManyCases // the person can fix this one by naming the case
		}
		return nil, &clisrv.CodeRefusal{Code: code}
	}
	return res, nil
}
