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
			if Check(op, s) != Deny || Permits(op, s) {
				t.Errorf("%q on %s must be denied", op, s)
			}
		}
	}
	if Check("audit.compare", Surface("root")) != Deny || Check("audit.compare", "") != Deny {
		t.Error("an unknown surface must be denied")
	}
}

func TestCodeIsDeniedEverywhere(t *testing.T) {
	for _, op := range []string{"code.run", "code.types", "code.replay"} {
		for _, s := range Surfaces() {
			if Check(op, s) != Deny {
				t.Errorf("%s on %s: %s", op, s, Check(op, s))
			}
		}
	}
}

func TestCasesAndAdminAreHumanOnly(t *testing.T) {
	for _, op := range []string{"cases.list", "cases.show", "doctor", "app.start", "app.stop", "grant.list", "grant.approve", "grant.revoke"} {
		if !Permits(op, CLIHuman) {
			t.Errorf("%s should be allowed for a person", op)
		}
		for _, s := range []Surface{CLIAuto, MCP, Sandbox} {
			if Permits(op, s) {
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
		} else if Permits(r.Op, CLIAuto) {
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
		if Check(op, CLIAuto) != NA || Permits(op, CLIAuto) {
			t.Errorf("%s: n/a for cli_auto, and a call that reaches the owner for it is refused", op)
		}
		if !Permits(op, CLIHuman) {
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
