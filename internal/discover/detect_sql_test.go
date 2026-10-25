package discover_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"api-audit-explorer/internal/discover"
	"api-audit-explorer/internal/discover/fake"
	"api-audit-explorer/internal/importer/all"
)

const (
	sqlNamesKey = `HKLM\SOFTWARE\Microsoft\Microsoft SQL Server\Instance Names\SQL`
	sqlLogDir   = `C:\Program Files\Microsoft SQL Server\MSSQL15.MSSQLSERVER\MSSQL\Log`
)

func sqlParamsKey(id string) string {
	return `HKLM\SOFTWARE\Microsoft\Microsoft SQL Server\` + id + `\MSSQLServer\Parameters`
}

// sqlInstance registers a service, its instance ID and its startup arguments.
func sqlInstance(e *fake.Env, service, instance, id string, args ...string) {
	dbtAddService(e, service, "sqlservr.exe", `C:\Program Files\Microsoft SQL Server\`+id+`\MSSQL\Binn`)
	e.Registry.Set(sqlNamesKey, instance, id)
	for i, a := range args {
		e.Registry.Set(sqlParamsKey(id), fmt.Sprintf("SQLArg%d", i), a)
	}
}

func sqlDefaultArgs(logDir string) []string {
	return []string{
		`-dC:\Program Files\Microsoft SQL Server\MSSQL15.MSSQLSERVER\MSSQL\DATA\` + dbtMarker + `\master.mdf`,
		`-e` + logDir + `\ERRORLOG`,
		`-lC:\Program Files\Microsoft SQL Server\MSSQL15.MSSQLSERVER\MSSQL\DATA\` + dbtMarker + `\mastlog.ldf`,
	}
}

func sqlFillDir(e *fake.Env, dir string) {
	dbtFile(e, dir+`\ERRORLOG`, dbtSQLLog)
	dbtFile(e, dir+`\ERRORLOG.1`, dbtSQLLog)
	dbtFile(e, dir+`\ERRORLOG.2`, dbtSQLLog)
	dbtFile(e, dir+`\system_health_0_132000000000000000.xel`, dbtMarker)
	dbtFile(e, dir+`\SQLAGENT.OUT`, dbtMarker)
	dbtFile(e, dir+`\SQLAGENT.1`, dbtMarker)
	dbtFile(e, dir+`\ERRORLOG.bak`, dbtMarker)
	dbtFile(e, dir+`\log_5.trc`, dbtMarker)
}

func runSQL(e *fake.Env) ([]discover.Finding, []discover.Gap) {
	return discover.SQLDetector{}.Detect(context.Background(), e.Env(all.Registry()))
}

func TestSQLErrorLogSetAndExportFindings(t *testing.T) {
	e := fake.New()
	sqlInstance(e, "MSSQLSERVER", "MSSQLSERVER", "MSSQL15.MSSQLSERVER", sqlDefaultArgs(sqlLogDir)...)
	sqlFillDir(e, sqlLogDir)
	rec := &dbtRecFS{FS: e.FS}
	env := e.Env(all.Registry())
	env.FS = rec

	findings, gaps := discover.SQLDetector{}.Detect(context.Background(), env)
	if len(gaps) != 0 || len(findings) != 3 {
		t.Fatalf("findings=%d gaps=%+v, want 3 findings and no gap", len(findings), gaps)
	}
	var set, xe, agent *discover.Finding
	for i := range findings {
		switch findings[i].Shows {
		case "discover.sql.shows":
			set = &findings[i]
		case "discover.sqlxe.shows":
			xe = &findings[i]
		case "discover.sqlagent.shows":
			agent = &findings[i]
		}
	}
	if set == nil || xe == nil || agent == nil {
		t.Fatalf("findings = %+v", findings)
	}

	if set.Kind != discover.KindLogSet || set.Importer != "sql-errorlog" || set.Confidence != 90 || set.Files != 3 {
		t.Errorf("log set = %+v", *set)
	}
	want := []string{sqlLogDir + `\ERRORLOG`, sqlLogDir + `\ERRORLOG.1`, sqlLogDir + `\ERRORLOG.2`}
	if got := set.Paths(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("paths = %v, want %v (ERRORLOG.bak, SQLAGENT.1 and *.trc are not part of the set)", got, want)
	}
	if set.SizeBytes != int64(3*len(dbtSQLLog)) {
		t.Errorf("size = %d", set.SizeBytes)
	}

	for _, f := range []*discover.Finding{xe, agent} {
		if f.Kind != discover.KindExportNeeded || f.Importer != "" || f.How == "" || len(f.Paths()) != 0 || f.Files != 1 {
			t.Errorf("export finding = %+v, paths %v", *f, f.Paths())
		}
	}
	if xe.How != "discover.sqlxe.how" || agent.How != "discover.sqlagent.how" {
		t.Errorf("How keys: %q, %q", xe.How, agent.How)
	}

	// The .xel and SQLAGENT.OUT files are listed, never opened, and the files
	// that are not part of any set are never touched.
	for _, p := range rec.touched {
		lp := strings.ToLower(p)
		for _, banned := range []string{".xel", "sqlagent", ".bak", ".trc"} {
			if strings.Contains(lp, banned) {
				t.Errorf("detector touched %s", p)
			}
		}
	}

	dbtAssertClean(t, findings, gaps)
	fake.AssertFindingsValid(t, findings, all.Registry())
}

func TestSQLNeverReturnsOtherStartupArgs(t *testing.T) {
	// -d and -l come before and after -e and carry the marker; so does a trace
	// flag. None may reach the result, and the -e value may be in any of its
	// forms.
	forms := map[string]string{
		"bare":       `-e` + sqlLogDir + `\ERRORLOG`,
		"space":      `-e ` + sqlLogDir + `\ERRORLOG`,
		"quoted":     `-e"` + sqlLogDir + `\ERRORLOG"`,
		"quoteSpace": `-e "` + sqlLogDir + `\ERRORLOG"`,
		"two args":   `-e` + sqlLogDir + `\ERRORLOG -l` + `C:\data\` + dbtMarker + `\x.ldf`,
	}
	for name, eArg := range forms {
		t.Run(name, func(t *testing.T) {
			e := fake.New()
			sqlInstance(e, "MSSQLSERVER", "MSSQLSERVER", "MSSQL15.MSSQLSERVER",
				`-dC:\data\`+dbtMarker+`\master.mdf`, `-T1118`, eArg, `-lC:\data\`+dbtMarker+`\mastlog.ldf`)
			dbtFile(e, sqlLogDir+`\ERRORLOG`, dbtSQLLog)
			findings, gaps := runSQL(e)
			if len(gaps) != 0 || len(findings) != 1 {
				t.Fatalf("findings=%d gaps=%+v", len(findings), gaps)
			}
			if p := findings[0].Paths(); len(p) != 1 || p[0] != sqlLogDir+`\ERRORLOG` {
				t.Errorf("paths = %v", p)
			}
			dbtAssertClean(t, findings, gaps)
		})
	}
}

func TestSQLErrorLogArg(t *testing.T) {
	const m = dbtMarker
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{`-eC:\Logs\ERRORLOG`, `C:\Logs\ERRORLOG`, true},
		{`-e C:\Logs\ERRORLOG`, `C:\Logs\ERRORLOG`, true},
		{`-e"C:\Program Files\X\ERRORLOG"`, `C:\Program Files\X\ERRORLOG`, true},
		{`-e "C:\Program Files\X\ERRORLOG"`, `C:\Program Files\X\ERRORLOG`, true},
		{`-eC:\a b\ERRORLOG`, `C:\a b\ERRORLOG`, true},
		{`  -eC:\Logs\ERRORLOG  `, `C:\Logs\ERRORLOG`, true},
		{`-eC:\a\ERRORLOG -lC:\` + m + `\x.ldf`, `C:\a\ERRORLOG`, true},
		{`-eC:\a\ERRORLOG  -dC:\` + m + `\x.mdf`, `C:\a\ERRORLOG`, true},
		{`-e"C:\a\ERRORLOG" -dC:\` + m, `C:\a\ERRORLOG`, true},
		{`-e"C:\a\ERRORLOG" junk ` + m, ``, false},
		{`-e"C:\a\ERRORLOG`, ``, false},
		{`-e""`, ``, false},
		{`-e`, ``, false},
		{`-e   `, ``, false},
		{`-dC:\` + m + `\master.mdf`, ``, false},
		{`-lC:\` + m + `\mastlog.ldf`, ``, false},
		{`-T1118`, ``, false},
		{`-m`, ``, false},
		{``, ``, false},
		{m, ``, false},
	}
	for _, c := range cases {
		got, ok := discover.SQLErrorLogArg(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("SQLErrorLogArg(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
		if strings.Contains(got, m) {
			t.Errorf("SQLErrorLogArg(%q) returned the marker", c.in)
		}
	}
}

func TestSQLNamedInstances(t *testing.T) {
	e := fake.New()
	sqlInstance(e, "MSSQLSERVER", "MSSQLSERVER", "MSSQL15.MSSQLSERVER", sqlDefaultArgs(sqlLogDir)...)
	dbtFile(e, sqlLogDir+`\ERRORLOG`, dbtSQLLog)
	expDir := `D:\Sql\SQLEXPRESS\Log`
	sqlInstance(e, `MSSQL$SQLEXPRESS`, "SQLEXPRESS", "MSSQL15.SQLEXPRESS", `-e`+expDir+`\ERRORLOG`)
	dbtFile(e, expDir+`\ERRORLOG`, dbtSQLLog)
	dbtFile(e, expDir+`\ERRORLOG.1`, dbtSQLLog)
	// Other SQL-looking services are not instances.
	dbtAddService(e, "SQLSERVERAGENT", "sqlagent.exe", `C:\Sql`)
	dbtAddService(e, `MSSQLFDLauncher`, "fdlauncher.exe", `C:\Sql`)
	dbtAddService(e, `MSSQL$BAD NAME`, "sqlservr.exe", `C:\Sql`)

	findings, gaps := runSQL(e)
	if len(gaps) != 0 || len(findings) != 2 {
		t.Fatalf("findings=%d gaps=%+v", len(findings), gaps)
	}
	var named *discover.Finding
	for i := range findings {
		if strings.Contains(findings[i].Display, "SQLEXPRESS") {
			named = &findings[i]
		}
	}
	if named == nil || named.Files != 2 || len(named.Paths()) != 2 {
		t.Fatalf("named instance finding = %+v", named)
	}
	if !strings.HasPrefix(named.Paths()[0], expDir) {
		t.Errorf("paths = %v", named.Paths())
	}
}

func TestSQLLogSetEdgeCases(t *testing.T) {
	t.Run("reparse member skipped with a gap", func(t *testing.T) {
		e := fake.New()
		sqlInstance(e, "MSSQLSERVER", "MSSQLSERVER", "MSSQL15.MSSQLSERVER", sqlDefaultArgs(sqlLogDir)...)
		dbtFile(e, sqlLogDir+`\ERRORLOG`, dbtSQLLog)
		dbtFile(e, sqlLogDir+`\ERRORLOG.1`, dbtSQLLog)
		e.FS.AddReparse(sqlLogDir + `\ERRORLOG.2`)
		findings, gaps := runSQL(e)
		if len(findings) != 1 || findings[0].Files != 2 {
			t.Fatalf("findings = %+v", findings)
		}
		dbtOneGap(t, gaps, discover.ReasonUnsafePath)
	})
	t.Run("only export files", func(t *testing.T) {
		e := fake.New()
		sqlInstance(e, "MSSQLSERVER", "MSSQLSERVER", "MSSQL15.MSSQLSERVER", sqlDefaultArgs(sqlLogDir)...)
		dbtFile(e, sqlLogDir+`\system_health_0_1.xel`, dbtMarker)
		findings, gaps := runSQL(e)
		if len(findings) != 1 || findings[0].Kind != discover.KindExportNeeded {
			t.Fatalf("findings = %+v", findings)
		}
		dbtOneGap(t, gaps, discover.ReasonNotFound)
	})
	t.Run("too many entries", func(t *testing.T) {
		e := fake.New()
		sqlInstance(e, "MSSQLSERVER", "MSSQLSERVER", "MSSQL15.MSSQLSERVER", sqlDefaultArgs(sqlLogDir)...)
		dbtFile(e, sqlLogDir+`\ERRORLOG`, dbtSQLLog)
		for i := 0; i < discover.MaxDirEntries+5; i++ {
			e.FS.AddFile(fmt.Sprintf(`%s\z%05d.trc`, sqlLogDir, i), 1, dbtTime, nil)
		}
		findings, gaps := runSQL(e)
		if len(findings) != 1 {
			t.Fatalf("findings = %+v", findings)
		}
		dbtOneGap(t, gaps, discover.ReasonTooLarge)
	})
	t.Run("log folder missing", func(t *testing.T) {
		e := fake.New()
		sqlInstance(e, "MSSQLSERVER", "MSSQLSERVER", "MSSQL15.MSSQLSERVER", sqlDefaultArgs(sqlLogDir)...)
		findings, gaps := runSQL(e)
		if len(findings) != 0 {
			t.Fatalf("findings = %+v", findings)
		}
		dbtOneGap(t, gaps, discover.ReasonNotFound)
	})
}

func TestSQLUnusableRegistry(t *testing.T) {
	cases := []struct {
		name  string
		setup func(e *fake.Env)
		want  discover.Reason
	}{
		{"instance not in the names map", func(e *fake.Env) {
			dbtAddService(e, "MSSQLSERVER", "sqlservr.exe", `C:\Sql`)
		}, discover.ReasonNotFound},
		{"no -e argument", func(e *fake.Env) {
			sqlInstance(e, "MSSQLSERVER", "MSSQLSERVER", "MSSQL15.MSSQLSERVER", `-dC:\data\master.mdf`, `-lC:\data\mastlog.ldf`)
		}, discover.ReasonNotFound},
		{"instance id with a path in it", func(e *fake.Env) {
			dbtAddService(e, "MSSQLSERVER", "sqlservr.exe", `C:\Sql`)
			e.Registry.Set(sqlNamesKey, "MSSQLSERVER", `..\..\evil`)
		}, discover.ReasonConfigUnparsed},
		{"unc error log path", func(e *fake.Env) {
			sqlInstance(e, "MSSQLSERVER", "MSSQLSERVER", "MSSQL15.MSSQLSERVER", `-e\\fileserver\share\ERRORLOG`)
		}, discover.ReasonUnsafePath},
		{"relative error log path", func(e *fake.Env) {
			sqlInstance(e, "MSSQLSERVER", "MSSQLSERVER", "MSSQL15.MSSQLSERVER", `-eLog\ERRORLOG`)
		}, discover.ReasonUnsafePath},
		{"secret word in the path", func(e *fake.Env) {
			sqlInstance(e, "MSSQLSERVER", "MSSQLSERVER", "MSSQL15.MSSQLSERVER", `-eC:\password\ERRORLOG`)
		}, discover.ReasonConfigUnparsed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := fake.New()
			c.setup(e)
			findings, gaps := runSQL(e)
			if len(findings) != 0 {
				t.Fatalf("findings = %+v", findings)
			}
			dbtOneGap(t, gaps, c.want)
		})
	}
}

func TestSQLMissingServiceIsSilent(t *testing.T) {
	e := fake.New()
	dbtAddService(e, "Spooler", "spoolsv.exe", `C:\Windows\System32`)
	dbtAddService(e, "SQLSERVERAGENT", "sqlagent.exe", `C:\Sql`)
	findings, gaps := runSQL(e)
	if len(findings) != 0 || len(gaps) != 0 {
		t.Fatalf("findings=%+v gaps=%+v, want neither for a host without SQL Server", findings, gaps)
	}
}

func TestSQLDenied(t *testing.T) {
	seed := func() *fake.Env {
		e := fake.New()
		sqlInstance(e, "MSSQLSERVER", "MSSQLSERVER", "MSSQL15.MSSQLSERVER", sqlDefaultArgs(sqlLogDir)...)
		sqlFillDir(e, sqlLogDir)
		return e
	}
	fake.AssertDeniedGap(t, discover.SQLDetector{}, seed())

	stages := map[string]func(e *fake.Env){
		"service list":   func(e *fake.Env) { e.Services.Err = discover.ErrAccessDenied },
		"instance names": func(e *fake.Env) { e.Registry.Deny(sqlNamesKey, "MSSQLSERVER") },
		"startup args":   func(e *fake.Env) { e.Registry.Deny(sqlParamsKey("MSSQL15.MSSQLSERVER"), "SQLArg0") },
		"log folder":     func(e *fake.Env) { e.FS.Deny(sqlLogDir) },
	}
	for name, deny := range stages {
		t.Run(name, func(t *testing.T) {
			e := seed()
			deny(e)
			findings, gaps := runSQL(e)
			if len(findings) != 0 {
				t.Fatalf("findings = %+v", findings)
			}
			dbtOneGap(t, gaps, discover.ReasonNeedsElevation)
		})
	}

	// A denied errorlog file is a gap for the set; the export findings, which
	// come from the folder listing alone, still appear.
	e := seed()
	e.FS.Deny(sqlLogDir + `\ERRORLOG`)
	findings, gaps := runSQL(e)
	dbtOneGap(t, gaps, discover.ReasonNeedsElevation)
	if len(findings) != 2 {
		t.Fatalf("findings = %+v, want the two export findings", findings)
	}
	for _, f := range findings {
		if f.Kind != discover.KindExportNeeded {
			t.Errorf("finding = %+v", f)
		}
	}
}

func TestSQLHostWithoutRegistry(t *testing.T) {
	findings, gaps := discover.SQLDetector{}.Detect(context.Background(), discover.Env{})
	if len(findings) != 0 {
		t.Fatalf("findings = %+v", findings)
	}
	dbtOneGap(t, gaps, discover.ReasonUnreadable)
}
