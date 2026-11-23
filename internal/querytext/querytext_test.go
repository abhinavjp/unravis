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
	f.Fuzz(func(t *testing.T, s string) {
		got := Scrub(s, 200)
		if !utf8.ValidString(got) || utf8.RuneCountInString(got) > 200 {
			t.Fatalf("bad output for %q: %q", s, got)
		}
	})
}
