package dbhealth

import (
	"strings"
	"testing"
)

func TestEraOf(t *testing.T) {
	for in, want := range map[string]SQLEra{
		"16.0.1000.6": Era2022, "17.0.1": Era2022, "15.0.2000.5": Era2019, "14.0.3": Era2019, "13.0": Era2019,
		"": Era2022, "garbage": Era2022, "  15.0.1 ": Era2019,
	} {
		if got := EraOf(in); got != want {
			t.Errorf("EraOf(%q) = %s, want %s", in, got, want)
		}
	}
	if _, ok := ParseEra("2017"); ok {
		t.Error("2017 must be refused")
	}
	if e, ok := ParseEra(" 2019 "); !ok || e != Era2019 {
		t.Error("2019 not parsed")
	}
}

func TestQuoteIdent(t *testing.T) {
	if q, ok := QuoteIdent("unravis_ro"); !ok || q != "[unravis_ro]" {
		t.Errorf("plain = %q %v", q, ok)
	}
	if q, ok := QuoteIdent("CORP\\svc]x"); !ok || q != "[CORP\\svc]]x]" {
		t.Errorf("bracket not doubled: %q", q)
	}
	for _, bad := range []string{"", "  ", "a;DROP LOGIN sa", "a--b", "a/*b", "a*/b", "a\nb", "a\x00b", strings.Repeat("x", 129)} {
		if _, ok := QuoteIdent(bad); ok {
			t.Errorf("%q must be refused", bad)
		}
	}
}

func TestSQLGrantScriptPerEra(t *testing.T) {
	s22 := SQLGrantScript(Era2022, "unravis_ro", "AuditDb")
	for _, want := range []string{
		"GRANT VIEW SERVER PERFORMANCE STATE TO [unravis_ro];", "GRANT VIEW ANY ERROR LOG TO [unravis_ro];",
		"USE [AuditDb];", "GRANT VIEW DATABASE PERFORMANCE STATE TO [unravis_ro];",
		"USE msdb;", "ALTER ROLE SQLAgentReaderRole ADD MEMBER [unravis_ro];", "SQL Server 2022",
	} {
		if !strings.Contains(s22, want) {
			t.Errorf("2022 script lacks %q:\n%s", want, s22)
		}
	}
	s19 := SQLGrantScript(Era2019, "unravis_ro", "AuditDb")
	for _, want := range []string{"GRANT VIEW SERVER STATE TO [unravis_ro];", "GRANT VIEW DATABASE STATE TO [unravis_ro];", "SQLAgentReaderRole", "SQL Server 2019"} {
		if !strings.Contains(s19, want) {
			t.Errorf("2019 script lacks %q:\n%s", want, s19)
		}
	}
	for _, no := range []string{"PERFORMANCE STATE", "VIEW ANY ERROR LOG"} {
		if strings.Contains(s19, no) {
			t.Errorf("2019 script must not use %q:\n%s", no, s19)
		}
	}
	// Creating the login is shown but commented out: the owner decides.
	for _, line := range strings.Split(s22, "\n") {
		if strings.Contains(line, "CREATE LOGIN") && !strings.HasPrefix(line, "--") {
			t.Errorf("CREATE LOGIN must be commented out: %s", line)
		}
	}
}

func TestSQLGrantScriptRefusesInjection(t *testing.T) {
	s := SQLGrantScript(Era2022, "x]; DROP LOGIN sa; --", "Db]; DROP DATABASE y; --")
	if strings.Contains(s, "DROP") {
		t.Fatalf("injection reached the script:\n%s", s)
	}
	if !strings.Contains(s, "[login]") || !strings.Contains(s, "[ApiDb]") {
		t.Errorf("placeholders missing:\n%s", s)
	}
	// A bracket inside a name is doubled rather than closing the identifier.
	s = SQLGrantScript(Era2022, "a]b", "d")
	if !strings.Contains(s, "[a]]b]") {
		t.Errorf("bracket not escaped:\n%s", s)
	}
}

func TestSQLCheckGrantEachCheck(t *testing.T) {
	for _, c := range []string{CheckServer, CheckErrorLog, CheckQueryStore, CheckAgentJobs} {
		for _, era := range []SQLEra{Era2019, Era2022} {
			g := SQLCheckGrant(c, era, "u", "d")
			if g.Permission == "" || g.Script == "" {
				t.Errorf("%s/%s empty: %+v", c, era, g)
			}
			if strings.HasPrefix(strings.TrimSpace(g.Script), "GRANT") {
				t.Errorf("%s/%s script starts with a bare GRANT: no comment about who runs it", c, era)
			}
		}
	}
	if g := SQLCheckGrant("nope", Era2022, "u", "d"); g.Script != "" {
		t.Errorf("unknown check = %+v", g)
	}
	// 2019 has no grantable error-log permission: the script must not pretend to grant one.
	if g := SQLCheckGrant(CheckErrorLog, Era2019, "u", "d"); strings.Contains(strings.ToUpper(g.Script), "\nGRANT ") {
		t.Errorf("2019 error-log script grants something: %s", g.Script)
	}
}

func TestMongoGrantScript(t *testing.T) {
	s := MongoGrantScript("unravis_ro", "AuditDb")
	for _, want := range []string{`role: "clusterMonitor", db: "admin"`, `role: "read", db: "AuditDb"`, `user: "unravis_ro"`, "passwordPrompt()"} {
		if !strings.Contains(s, want) {
			t.Errorf("mongo script lacks %q:\n%s", want, s)
		}
	}
	bad := MongoGrantScript("u\"}); db.dropDatabase(); //", "a\nb")
	// The text stays inside one escaped JSON string, so it can never end the string and start a statement.
	if !strings.Contains(bad, `user: "u\"}); db.dropDatabase(); //", pwd:`) || strings.Contains(bad, "a\nb") {
		t.Errorf("injection reached the script:\n%s", bad)
	}
	if !strings.Contains(bad, `"<AuditDb>"`) {
		t.Errorf("placeholder missing:\n%s", bad)
	}
	// A quote inside a name is escaped, so the string cannot end early.
	q := MongoGrantScript(`a"b`, "d")
	if !strings.Contains(q, `"a\"b"`) {
		t.Errorf("quote not escaped:\n%s", q)
	}
}

func TestMongoProfilerGuide(t *testing.T) {
	g := MongoProfilerGuide("AuditDb")
	if g.Title == "" || len(g.Steps) < 3 || g.Why == "" || g.Cost == "" {
		t.Fatalf("guide = %+v", g)
	}
	for _, want := range []string{"setProfilingLevel(1", "slowms: 100", "capped: true", `getSiblingDB("AuditDb")`} {
		if !strings.Contains(g.SwitchOn, want) {
			t.Errorf("switch-on lacks %q:\n%s", want, g.SwitchOn)
		}
	}
	if !strings.Contains(g.SwitchOff, "setProfilingLevel(0)") || !strings.Contains(g.Check, "getProfilingStatus") {
		t.Errorf("off/check = %q / %q", g.SwitchOff, g.Check)
	}
	if strings.Contains(MongoProfilerGuide(`x"); drop`).SwitchOn, "drop`") {
		t.Error("database name escaped the string")
	}
}
