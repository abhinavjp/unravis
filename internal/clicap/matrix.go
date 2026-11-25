// Package clicap is the capability matrix of the command line (spec 05-cli D8): for each operation and each surface, whether it is
// allowed. It is data, and the answer for anything not listed is no (deny by default). The owner side of a CLI call asks Check before
// it runs an operation, and an operation that has no row with every column set does not register.
package clicap

import "strings"

// Surface is a place an operation can be called from.
type Surface string

const (
	GUI      Surface = "gui"       // the app window
	CLIHuman Surface = "cli_human" // the command line used by a person, with the locator token
	CLIAuto  Surface = "cli_auto"  // the command line with --profile (an automation grant)
	MCP      Surface = "mcp"       // an AI agent over MCP
	Sandbox  Surface = "sandbox"   // code mode (area 04)
)

// Surfaces lists every surface, in the column order of the matrix.
func Surfaces() []Surface { return []Surface{GUI, CLIHuman, CLIAuto, MCP, Sandbox} }

// Access is the value of one cell.
type Access string

const (
	Allow  Access = "allow"  // yes
	Scoped Access = "scoped" // yes, within a grant (area 03): the caller must also hold one
	Deny   Access = "deny"   // no
	NA     Access = "n/a"    // the operation does not go through this surface (it has no backend there), so there is no gate
)

// Row is one operation. Tool is the MCP tool the operation runs, when it runs one.
type Row struct {
	Op   string
	Tool string
	Gui  Access
	// Human, Auto, MCP and Sandbox are the other four columns, in the order of Surfaces.
	Human, Auto, MCP, Sandbox Access
}

// Cell returns the value of the row for a surface. An unknown surface is Deny.
func (r Row) Cell(s Surface) Access {
	switch s {
	case GUI:
		return r.Gui
	case CLIHuman:
		return r.Human
	case CLIAuto:
		return r.Auto
	case MCP:
		return r.MCP
	case Sandbox:
		return r.Sandbox
	}
	return Deny
}

// The v1 matrix (spec 05-cli, section 3.6). Add a row before the operation exists. The code.* rows of area 04 stay deny except the cells that
// area has flipped: code.run and code.types are open to the person at the command line (BL-371) and to nobody else; code.replay stays
// closed until it is built (BL-370).
var matrix = []Row{
	{Op: "version", Gui: NA, Human: Allow, Auto: NA, MCP: NA, Sandbox: NA},
	{Op: "schema", Gui: NA, Human: Allow, Auto: NA, MCP: NA, Sandbox: NA},
	{Op: "sources.list", Tool: "audit_list_sources", Gui: Allow, Human: Allow, Auto: Scoped, MCP: Scoped, Sandbox: Deny},
	{Op: "audit.guide", Tool: "audit_get_guide", Gui: Allow, Human: Allow, Auto: Scoped, MCP: Scoped, Sandbox: Deny},
	{Op: "audit.compare", Tool: "audit_compare", Gui: Allow, Human: Allow, Auto: Scoped, MCP: Scoped, Sandbox: Deny},
	{Op: "audit.groups", Tool: "audit_get_groups", Gui: Allow, Human: Allow, Auto: Scoped, MCP: Scoped, Sandbox: Deny},
	{Op: "audit.timeline", Tool: "audit_get_timeline", Gui: Allow, Human: Allow, Auto: Scoped, MCP: Scoped, Sandbox: Deny},
	{Op: "audit.timelines.compare", Tool: "audit_compare_timelines", Gui: Allow, Human: Allow, Auto: Scoped, MCP: Scoped, Sandbox: Deny},
	{Op: "audit.requests", Tool: "audit_get_requests", Gui: Allow, Human: Allow, Auto: Scoped, MCP: Scoped, Sandbox: Deny},
	{Op: "audit.slowest", Tool: "audit_get_slowest", Gui: Allow, Human: Allow, Auto: Scoped, MCP: Scoped, Sandbox: Deny},
	{Op: "audit.breakdown", Tool: "audit_get_breakdown", Gui: Allow, Human: Allow, Auto: Scoped, MCP: Scoped, Sandbox: Deny},
	{Op: "audit.activity", Tool: "audit_get_activity", Gui: Allow, Human: Allow, Auto: Scoped, MCP: Scoped, Sandbox: Deny},
	{Op: "cases.list", Gui: Allow, Human: Allow, Auto: Deny, MCP: Deny, Sandbox: Deny},
	{Op: "cases.show", Gui: Allow, Human: Allow, Auto: Deny, MCP: Deny, Sandbox: Deny},
	{Op: "doctor", Gui: NA, Human: Allow, Auto: Deny, MCP: Deny, Sandbox: Deny},
	{Op: "app.status", Gui: NA, Human: Allow, Auto: Allow, MCP: Deny, Sandbox: Deny},
	{Op: "app.start", Gui: NA, Human: Allow, Auto: Deny, MCP: Deny, Sandbox: Deny},
	{Op: "app.stop", Gui: NA, Human: Allow, Auto: Deny, MCP: Deny, Sandbox: Deny},
	{Op: "grant.list", Gui: Allow, Human: Allow, Auto: Deny, MCP: Deny, Sandbox: Deny},
	{Op: "grant.approve", Gui: Allow, Human: Allow, Auto: Deny, MCP: Deny, Sandbox: Deny},
	{Op: "grant.revoke", Gui: Allow, Human: Allow, Auto: Deny, MCP: Deny, Sandbox: Deny},
	{Op: "blackbox.inspect", Gui: NA, Human: Allow, Auto: NA, MCP: NA, Sandbox: NA},
	{Op: "code.run", Gui: Deny, Human: Allow, Auto: Deny, MCP: Deny, Sandbox: Deny},
	{Op: "code.types", Gui: Deny, Human: Allow, Auto: Deny, MCP: Deny, Sandbox: Deny},
	{Op: "code.replay", Gui: Deny, Human: Deny, Auto: Deny, MCP: Deny, Sandbox: Deny},
}

var byOp = func() map[string]Row {
	m := make(map[string]Row, len(matrix))
	for _, r := range matrix {
		if _, dup := m[r.Op]; dup {
			panic("clicap: operation listed twice: " + r.Op)
		}
		m[r.Op] = r
	}
	return m
}()

// Rows returns a copy of the matrix, in its order.
func Rows() []Row { return append([]Row(nil), matrix...) }

// Lookup returns the row of an operation.
func Lookup(op string) (Row, bool) { r, ok := byOp[op]; return r, ok }

// Complete says whether every column of the row holds a known value.
func (r Row) Complete() bool {
	if r.Op == "" || strings.TrimSpace(r.Op) != r.Op {
		return false
	}
	for _, s := range Surfaces() {
		switch r.Cell(s) {
		case Allow, Scoped, Deny, NA:
		default:
			return false
		}
	}
	return true
}

// Check says what a surface may do with an operation. An operation that is not listed, a surface that is not known, or a row that is
// not complete, is Deny.
func Check(op string, s Surface) Access {
	r, ok := byOp[op]
	if !ok || !r.Complete() {
		return Deny
	}
	switch v := r.Cell(s); v {
	case Allow, Scoped, Deny, NA:
		return v
	}
	return Deny
}

// Gate says whether a call may go on at the gate, and whether it needs a grant first. An Allow goes on with no grant. A Scoped call goes
// on only with a grant (area 03), so the caller must check it: needsGrant is true then. Deny stops the call. NA means the surface has
// no gate for this operation (an offline operation), the owner never sees such a call, and a call that does arrive for it is refused.
func Gate(op string, s Surface) (ok, needsGrant bool) {
	switch Check(op, s) {
	case Allow:
		return true, false
	case Scoped:
		return true, true
	}
	return false, false
}

// Register is for an operation handler: it panics if the operation has no complete row. Call it at start-up, so an operation
// without a row never runs.
func Register(op string) {
	if r, ok := byOp[op]; !ok || !r.Complete() {
		panic("clicap: operation has no complete matrix row: " + op)
	}
}
