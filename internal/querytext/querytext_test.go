package querytext

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestLiteralsBecomeQuestionMarksAndShapeStays(t *testing.T) {
	got := Scrub("SELECT TOP 10 name, 'a''b' AS x FROM [dbo].[Orders] WHERE id = 42 AND ref = N'ZX-9' AND p = 0xDEADBEEF AND r = 3.5e-2 AND c = @p1", 0)
	want := "SELECT TOP ? name, ? AS x FROM [dbo].[Orders] WHERE id = ? AND ref = ? AND p = ? AND r = ? AND c = @p1"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestNumbersInsideNamesStay(t *testing.T) {
	if got := Scrub("SELECT col1, t2.x FROM tbl3 JOIN [a 1] ON 1=1", 0); got != "SELECT col1, t2.x FROM tbl3 JOIN [a 1] ON ?=?" {
		t.Fatal(got)
	}
}

func TestCommentsAreDroppedAndUnclosedFailsClosed(t *testing.T) {
	if got := Scrub("SELECT 1 -- note: use hunter2\nFROM t /* also x */ WHERE a=1", 0); strings.Contains(got, "hunter2") || strings.Contains(got, "note") || strings.Contains(got, "also") {
		t.Fatal(got)
	}
	if got := Scrub("SELECT a FROM t /* never closed SELECT secretvalue", 0); strings.Contains(got, "never") || strings.Contains(got, "secretvalue") {
		t.Fatal(got)
	}
	if got := Scrub("SELECT a FROM t WHERE b = 'never closed tail", 0); strings.Contains(got, "tail") {
		t.Fatal(got)
	}
}

func TestCredentialStatementsAreWithheld(t *testing.T) {
	for _, q := range []string{
		"CREATE LOGIN bob WITH PASSWORD = 'x'",
		"alter login sa with pwd='x'",
		"EXEC sp_addlinkedsrvlogin 'srv','false',NULL,'u','p'",
		"SELECT * FROM OPENROWSET('SQLNCLI','Server=a;Trusted_Connection=yes;','select 1')",
		"CREATE CERTIFICATE c WITH SUBJECT='x'",
		"EXECUTE AS USER = 'dbo'",
		"SELECT token FROM sessions",
	} {
		if got := Scrub(q, 0); got != Withheld {
			t.Errorf("%q -> %q", q, got)
		}
	}
}

func TestSecretscrubIsASecondNet(t *testing.T) {
	// A key=value form that is not a literal still goes through the shared scrubber.
	if got := Scrub("EXEC p @conn = Server=a;Pwd=hunter2;Db=b", 0); strings.Contains(got, "hunter2") {
		t.Fatal(got)
	}
}

func TestSizeCapIsInCharactersAndNeverSplitsARune(t *testing.T) {
	long := "SELECT " + strings.Repeat("é, ", 400) + "x FROM t"
	got := Scrub(long, 100)
	if utf8.RuneCountInString(got) > 100 || !utf8.ValidString(got) || !strings.HasSuffix(got, "…") {
		t.Fatalf("%d %q", utf8.RuneCountInString(got), got)
	}
	if got := Scrub(strings.Repeat("a ", 3000), 99999); utf8.RuneCountInString(got) > MaxMaxChars {
		t.Fatal("cap above the maximum")
	}
	if got := Scrub(strings.Repeat("a ", 300), 5); utf8.RuneCountInString(got) > MinMaxChars {
		t.Fatal("cap below the minimum was not raised")
	}
}

func TestBlankAndHugeInput(t *testing.T) {
	if Scrub("  \n\t ", 0) != "" {
		t.Fatal("blank")
	}
	if got := Scrub(strings.Repeat("SELECT 1; ", 100000), 0); len(got) > 4*MaxMaxChars {
		t.Fatal("huge input not bounded")
	}
}

func TestInvalidUTF8Survives(t *testing.T) {
	if got := Scrub("SELECT \xff\xfe FROM t", 0); !utf8.ValidString(got) {
		t.Fatalf("%q", got)
	}
}

func FuzzScrub(f *testing.F) {
	for _, s := range []string{"SELECT 1", "'", "/*", "--", "[", "0x", "N'a", "\"", "1e+", "a" + strings.Repeat("'", 50)} {
		f.Add(s)
	}
	for _, s := range []string{"/* /* a */ don't */ 1", "/* /* a */ [ */ x", "EXEC u bob, x", "$12.50", "12AB34", "[a@b]", "[x"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got := Scrub(s, 200)
		if !utf8.ValidString(got) || utf8.RuneCountInString(got) > 200 {
			t.Fatalf("bad output for %q: %q", s, got)
		}
	})
}

// Inputs from the security review of the first version. Each one leaked before.
func TestReviewRegressions(t *testing.T) {
	leaks := map[string][]string{
		"/* /* note */ don't */ SELECT * FROM t WHERE c = 'alice@example.com' AND n = 'John Smith' AND z = 'x'": {"alice", "John", "Smith", "don"},
		"/* /* x */ call Alice Smith at home */ 1":                                                              {"Alice", "Smith", "home"},
		"SELECT [x FROM t WHERE ssn='123-45-6789'":                                                              {"123-45", "ssn"},
		"/* /* a */ [ */ SELECT * FROM t WHERE ssn = '123-45-6789'":                                             {"123-45"},
		"EXEC dbo.usp_SignIn bob, Hunter2":                                                                      {"bob", "Hunter2"},
		"EXEC dbo.usp_Find @email = alice, @code = Zx9Q":                                                        {"alice", "Zx9Q"},
		"EXEC sp_setapprole r, Hunter2":                                                                         {"Hunter2"},
		"EXEC sp_addapprole r, Hunter2":                                                                         {"Hunter2"},
		"SELECT * FROM t WHERE amount = $12.50":                                                                 {"$12", "12", "50"},
		"SELECT [alice@example.com], [John@x] FROM t":                                                           {"alice@", "John@"},
		"SELECT * FROM (SELECT 1 a) p PIVOT (MAX(a) FOR d IN ([2024-01-01]))":                                   {"2024"},
		"CREATE  LOGIN [corp\\bob] FROM WINDOWS":                                                                {"corp", "bob"},
		"CREATE/**/LOGIN [corp\\bob] FROM WINDOWS":                                                              {"corp", "bob"},
		"EXECUTE\nAS LOGIN = 'sa'":                                                                              {"sa"},
		"SELECT 12AB34":                                                                                         {"AB34", "12"},
		"EXEC usp one; SELECT 1; EXEC usp @a = 1, two":                                                          {"two"},
	}
	for in, bad := range leaks {
		got := Scrub(in, 0)
		for _, b := range bad {
			if strings.Contains(got, b) {
				t.Errorf("%q leaks %q: %q", in, b, got)
			}
		}
	}
}

// What stays readable: ordinary calls, names and the shape of a query.
func TestOrdinaryTextStaysReadable(t *testing.T) {
	cases := map[string]string{
		"EXEC dbo.usp_Find @email = 'a@b.c', @n = 5, @out = @x OUTPUT": "EXEC dbo.usp_Find @email = ?, @n = ?, @out = @x OUTPUT",
		"EXEC @rc = dbo.usp_Do @a = DEFAULT, @b = NULL":                "EXEC @rc = dbo.usp_Do @a = DEFAULT, @b = NULL",
		"SELECT * FROM [Order Details] WHERE OrderID = 10248":          "SELECT * FROM [Order Details] WHERE OrderID = ?",
		"SELECT amount FROM t WHERE c = $-3.5 AND x = 'y'":             "SELECT amount FROM t WHERE c = ? AND x = ?",
		"/* outer /* inner */ still outer */ SELECT 1":                 "SELECT ?",
	}
	for in, want := range cases {
		if got := Scrub(in, 0); got != want {
			t.Errorf("%q\n got  %q\n want %q", in, got, want)
		}
	}
}

// Second review: procedure calls without EXEC, a variable that holds the procedure name, bracketed and parenthesised arguments, and a ; inside a name.
func TestSecondReviewRegressions(t *testing.T) {
	leaks := map[string][]string{
		"usp_SignIn bob, Hunter2":                    {"bob", "Hunter2"},
		"dbo.usp_SignIn @user = bob, @pin = Hunter2": {"bob", "Hunter2"},
		"EXEC usp @p = N'x'; usp2 bob":               {"bob"},
		"EXEC @proc Hunter2":                         {"Hunter2"},
		"EXEC usp [Hunter2]":                         {"Hunter2"},
		"EXEC usp [a;b], Hunter2":                    {"Hunter2"},
		"EXEC usp @a = [;] , @b = Hunter2":           {"Hunter2"},
		"EXEC usp @p = (Hunter2)":                    {"Hunter2"},
		"EXEC @rc = @proc bob, Hunter2":              {"bob", "Hunter2"},
		"SELECT 1; usp_SignIn bob":                   {"bob"},
	}
	for in, bad := range leaks {
		got := Scrub(in, 0)
		for _, b := range bad {
			if strings.Contains(got, b) {
				t.Errorf("%q leaks %q: %q", in, b, got)
			}
		}
	}
	keep := map[string]string{
		"SELECT * FROM [Order Details] WHERE a = 1; SELECT 2": "SELECT * FROM [Order Details] WHERE a = ?; SELECT ?",
		"EXEC @rc = [dbo].[usp_Do] @a = (?), @b = @c OUT":     "EXEC @rc = [dbo].[usp_Do] @a = (?), @b = @c OUT",
		"EXEC @proc @a = 1":                      "EXEC @proc @a = ?",
		"(SELECT 1) UNION (SELECT 2)":            "(SELECT ?) UNION (SELECT ?)",
		"WITH c AS (SELECT 1 a) SELECT a FROM c": "WITH c AS (SELECT ? a) SELECT a FROM c",
	}
	for in, want := range keep {
		if got := Scrub(in, 0); got != want {
			t.Errorf("%q\n got  %q\n want %q", in, got, want)
		}
	}
}

// Third review (all low): blocks, unreserved words as procedure names, invisible separators, account names.
func TestThirdReviewRegressions(t *testing.T) {
	leaks := map[string][]string{
		"BEGIN usp bob END":                {"bob"},
		"IF 1=1 SELECT 1 ELSE usp bob":     {"bob"},
		"BEGIN TRY usp bob END TRY":        {"bob"},
		"next bob, Hunter2":                {"bob", "Hunter2"},
		"count bob":                        {"bob"},
		"EXEC usp​Hunter2":                 {"Hunter2"},
		"usp​Hunter2":                      {"Hunter2"},
		"GRANT SELECT ON t TO [corp\\bob]": {"corp", "bob"},
	}
	for in, bad := range leaks {
		got := Scrub(in, 0)
		for _, b := range bad {
			if strings.Contains(got, b) {
				t.Errorf("%q leaks %q: %q", in, b, got)
			}
		}
	}
	if got := Scrub("BEGIN TRANSACTION t1; SELECT 1; COMMIT", 0); got != "BEGIN TRANSACTION t1; SELECT ?; COMMIT" {
		t.Errorf("ordinary block text changed: %q", got)
	}
}
