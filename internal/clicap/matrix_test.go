package clicap

import (
	"strings"
	"testing"
)

func TestEveryRowHasAllFiveColumns(t *testing.T) {
	if len(Rows()) < 20 {
		t.Fatalf("only %d rows", len(Rows()))
	}
	seen := map[string]bool{}
	for _, r := range Rows() {
		if !r.Complete() {
			t.Errorf("%q: a column is empty or unknown: %+v", r.Op, r)
		}
		if seen[r.Op] {
			t.Errorf("%q is listed twice", r.Op)
		}
		seen[r.Op] = true
	}
	if len(Surfaces()) != 5 {
		t.Errorf("surfaces: %v", Surfaces())
	}
}

func TestUnlistedOpDenied(t *testing.T) {
	for _, op := range []string{"", "nosuch", "cases.delete", "audit.compare ", "AUDIT.COMPARE", "audit.*", "code", "app"} {
		for _, s := range Surfaces() {
			if Check(op, s) != Deny || permits(op, s) {
				t.Errorf("%q on %s must be denied", op, s)
			}
		}
	}
	if Check("audit.compare", Surface("root")) != Deny || Check("audit.compare", "") != Deny {
		t.Error("an unknown surface must be denied")
	}
}

// code.replay is not built (BL-370): it stays closed everywhere.
func TestCodeReplayIsDeniedEverywhere(t *testing.T) {
	for _, s := range Surfaces() {
		if Check("code.replay", s) != Deny {
			t.Errorf("code.replay on %s: %s", s, Check("code.replay", s))
		}
	}
}

// code.run and code.types (BL-371) are for the person at the command line and nobody else: not the app window (it has its own routes),
// not an automation profile, not an MCP host, not a script in the sandbox. No grant opens them either.
func TestCodeRunAndTypesAreHumanCLIOnly(t *testing.T) {
	for _, op := range []string{"code.run", "code.types"} {
		if ok, grant := Gate(op, CLIHuman); !ok || grant {
			t.Errorf("%s for a person: %v %v", op, ok, grant)
		}
		for _, s := range []Surface{GUI, CLIAuto, MCP, Sandbox} {
			if Check(op, s) != Deny {
				t.Errorf("%s on %s: %s", op, s, Check(op, s))
			}
			if ok, _ := Gate(op, s); ok {
				t.Errorf("%s: Gate lets %s through", op, s)
			}
		}
	}
}

func TestCasesAndAdminAreHumanOnly(t *testing.T) {
	for _, op := range []string{"cases.list", "cases.show", "doctor", "app.start", "app.stop", "grant.list", "grant.approve", "grant.revoke"} {
		if !permits(op, CLIHuman) {
			t.Errorf("%s should be allowed for a person", op)
		}
		for _, s := range []Surface{CLIAuto, MCP, Sandbox} {
			if permits(op, s) {
				t.Errorf("%s must be denied for %s", op, s)
			}
		}
	}
}

func TestAutomationMayOnlyRunAuditToolsSourcesAndStatus(t *testing.T) {
	want := map[string]Access{"sources.list": Scoped, "app.status": Allow}
	for _, r := range Rows() {
		if strings.HasPrefix(r.Op, "audit.") {
			want[r.Op] = Scoped
		}
	}
	auditOps := 0
	for op := range want {
		if strings.HasPrefix(op, "audit.") {
			auditOps++
		}
	}
	if auditOps != 9 {
		t.Errorf("%d audit.* operations, want 9 (with sources.list that is the ten tools)", auditOps)
	}
	for _, r := range Rows() {
		got := Check(r.Op, CLIAuto)
		if w, ok := want[r.Op]; ok {
			if got != w {
				t.Errorf("%s for cli_auto: %s, want %s", r.Op, got, w)
			}
		} else if permits(r.Op, CLIAuto) {
			t.Errorf("%s must not be permitted for cli_auto", r.Op)
		}
	}
}

func TestMCPAndSandboxNeverRunAdminOps(t *testing.T) {
	for _, r := range Rows() {
		tool := r.Tool != ""
		if r.MCP == Scoped && !tool {
			t.Errorf("%s: scoped for mcp but runs no MCP tool", r.Op)
		}
		if r.Sandbox == Allow || r.Sandbox == Scoped {
			t.Errorf("%s: the sandbox gets nothing until area 04 flips its cells", r.Op)
		}
	}
}

func TestToolNamesAreTheTenOfTheMCPCatalog(t *testing.T) {
	want := map[string]bool{}
	for _, n := range []string{"audit_compare", "audit_compare_timelines", "audit_get_activity", "audit_get_breakdown", "audit_get_groups", "audit_get_guide", "audit_get_requests", "audit_get_slowest", "audit_get_timeline", "audit_list_sources"} {
		want[n] = true
	}
	for _, r := range Rows() {
		if r.Tool == "" {
			continue
		}
		if !want[r.Tool] {
			t.Errorf("%s runs unknown tool %s", r.Op, r.Tool)
		}
		delete(want, r.Tool)
	}
	for n := range want {
		t.Errorf("tool %s has no row", n)
	}
}

func TestOfflineOpsAreNotGatedForAutomation(t *testing.T) {
	for _, op := range []string{"version", "schema", "blackbox.inspect"} {
		if Check(op, CLIAuto) != NA || permits(op, CLIAuto) {
			t.Errorf("%s: n/a for cli_auto, and a call that reaches the owner for it is refused", op)
		}
		if !permits(op, CLIHuman) {
			t.Errorf("%s: a person may run it", op)
		}
	}
}

func TestRegister(t *testing.T) {
	Register("audit.compare") // no panic
	defer func() {
		if recover() == nil {
			t.Error("an operation without a row must not register")
		}
	}()
	Register("cases.delete")
}

func TestRowsIsACopy(t *testing.T) {
	rs := Rows()
	rs[0].Human = Deny
	if Check("version", CLIHuman) != Allow {
		t.Error("changing the copy changed the matrix")
	}
	if (Row{Op: "x", Gui: Allow, Human: Allow, Auto: Allow, MCP: Allow}).Complete() {
		t.Error("a row with an empty column is not complete")
	}
}

func permits(op string, s Surface) bool { ok, _ := Gate(op, s); return ok }

func TestGateTellsWhenAGrantIsNeeded(t *testing.T) {
	if ok, grant := Gate("audit.compare", MCP); !ok || !grant {
		t.Errorf("a scoped call needs a grant: %v %v", ok, grant)
	}
	if ok, grant := Gate("audit.compare", CLIHuman); !ok || grant {
		t.Errorf("a person needs no grant: %v %v", ok, grant)
	}
	if ok, grant := Gate("cases.list", CLIAuto); ok || grant {
		t.Errorf("denied: %v %v", ok, grant)
	}
	if ok, _ := Gate("nosuch", GUI); ok {
		t.Error("unlisted")
	}
}

// TestMatrixIsTheSpecTable pins every cell to the table of spec 05-cli section 3.6 (written out again here, on purpose: a changed cell
// must be changed in two places). Columns: gui, cli_human, cli_auto, mcp, sandbox.
func TestMatrixIsTheSpecTable(t *testing.T) {
	const (
		a, s, d, n = Allow, Scoped, Deny, NA
	)
	golden := map[string][5]Access{
		"version":                 {n, a, n, n, n},
		"schema":                  {n, a, n, n, n},
		"sources.list":            {a, a, s, s, d},
		"audit.guide":             {a, a, s, s, d},
		"audit.compare":           {a, a, s, s, d},
		"audit.groups":            {a, a, s, s, d},
		"audit.timeline":          {a, a, s, s, d},
		"audit.timelines.compare": {a, a, s, s, d},
		"audit.requests":          {a, a, s, s, d},
		"audit.slowest":           {a, a, s, s, d},
		"audit.breakdown":         {a, a, s, s, d},
		"audit.activity":          {a, a, s, s, d},
		"cases.list":              {a, a, d, d, d},
		"cases.show":              {a, a, d, d, d},
		"doctor":                  {n, a, d, d, d},
		"app.status":              {n, a, a, d, d},
		"app.start":               {n, a, d, d, d},
		"app.stop":                {n, a, d, d, d},
		"grant.list":              {a, a, d, d, d},
		"grant.approve":           {a, a, d, d, d},
		"grant.revoke":            {a, a, d, d, d},
		"blackbox.inspect":        {n, a, n, n, n},
		"code.run":                {d, a, d, d, d},
		"code.types":              {d, a, d, d, d},
		"code.replay":             {d, d, d, d, d},
	}
	if len(Rows()) != len(golden) {
		t.Errorf("%d rows, %d in the spec table", len(Rows()), len(golden))
	}
	for op, want := range golden {
		for i, sf := range Surfaces() {
			if got := Check(op, sf); got != want[i] {
				t.Errorf("%s on %s: %s, want %s", op, sf, got, want[i])
			}
		}
	}
}
