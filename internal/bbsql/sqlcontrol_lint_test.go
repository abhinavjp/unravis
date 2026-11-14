package bbsql

// sqlcontrol_lint_test.go is the W7-05 regression gate: the restricted C2
// grammar must reject the demonstrated mutants (post-prefix SET, SELECT INTO,
// trailing statements, comment tricks, unapproved sources), and the send gates
// must read the private registry so a mutation of the exported compatibility
// map cannot widen the send policy.

import (
	"strings"
	"testing"
)

// TestLintGrammarAcceptsRegistry proves every fixed entry passes the
// restricted grammar (the init self-check enforces this in production too).
func TestLintGrammarAcceptsRegistry(t *testing.T) {
	for id, text := range statements {
		if err := lintStatement(id, text); err != nil {
			t.Errorf("%s: %v", id, err)
		}
	}
}

// TestLintGrammarRejectsMutants is the teeth: each mutant keeps the old
// prefix/count/tag/blacklist checks happy but breaks the restricted grammar.
func TestLintGrammarRejectsMutants(t *testing.T) {
	base := Statements["bb:schedulers"]
	mutants := map[string]string{
		// W7-05's demonstrated bypass: a session-option override right after
		// the required prefix. One SELECT, the tag, no blacklist hit — but a
		// semicolon and a second SET.
		"post-prefix SET": setPrefix + "SET LOCK_TIMEOUT -1; " + strings.TrimPrefix(base, setPrefix),
		"SELECT INTO":     setPrefix + `SELECT SUM(current_tasks_count) AS current_tasks INTO #x FROM sys.dm_os_schedulers /* unravis:bb:schedulers */`,
		"trailing stmt":   base + "; SELECT 1",
		"second SELECT":   setPrefix + `SELECT 1; SELECT 2 /* unravis:bb:schedulers */`,
		"UNION":           setPrefix + `SELECT 1 UNION SELECT 2 /* unravis:bb:schedulers */`,
		"line comment":    setPrefix + "SELECT 1 -- hide\n /* unravis:bb:schedulers */",
		"inner comment":   setPrefix + "SELECT /* sneak */ 1 /* unravis:bb:schedulers */",
		"unapproved FROM": setPrefix + `SELECT name FROM sys.syslogins /* unravis:bb:schedulers */`,
		"EXEC":            setPrefix + "SELECT 1; EXEC sp_who /* unravis:bb:schedulers */",
		"bare literal":    setPrefix + "SELECT 'x' /* unravis:bb:schedulers */",
		"missing tag":     setPrefix + "SELECT 1",
		"wrong tag":       setPrefix + "SELECT 1 /* unravis:bb:other */",
		"no prefix":       "SELECT 1 /* unravis:bb:schedulers */",
		"waitfor":         setPrefix + "SELECT 1; WAITFOR DELAY '00:00:05' /* unravis:bb:schedulers */",
		"variable":        setPrefix + "SELECT @x /* unravis:bb:schedulers */",
		"unbounded TOP":   setPrefix + "SELECT TOP x 1 /* unravis:bb:schedulers */",
	}
	for name, text := range mutants {
		if err := lintStatement("bb:schedulers", text); err == nil {
			t.Errorf("%s: grammar accepted the mutant", name)
		}
	}
}

// TestSendGatesReadPrivateRegistry proves the exported compatibility map is
// not the send policy (W7-05): mutating it changes neither Statement nor
// IsAllowed.
func TestSendGatesReadPrivateRegistry(t *testing.T) {
	orig, ok := Statement("bb:schedulers")
	if !ok {
		t.Fatal("bb:schedulers missing")
	}
	saved := Statements["bb:schedulers"]
	Statements["bb:schedulers"] = setPrefix + "SELECT 1 /* unravis:bb:schedulers */"
	defer func() { Statements["bb:schedulers"] = saved }()

	if got, _ := Statement("bb:schedulers"); got != orig {
		t.Fatal("Statement followed a mutation of the exported map")
	}
	if IsAllowed(Statements["bb:schedulers"]) {
		t.Fatal("IsAllowed admitted a mutation of the exported map")
	}
	if !IsAllowed(orig) {
		t.Fatal("IsAllowed lost the fixed statement")
	}
}