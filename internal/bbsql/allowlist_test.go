package bbsql

import (
	"context"
	"regexp"
	"strings"
	"testing"
)

// requiredIDs are the seven statements this packet must define. The lint below
// runs over every entry in Statements (so a later fixed addition, e.g. the
// bb:hadr guard from BBX-5-14, is linted too) while this list pins the seven.
var requiredIDs = []string{"bb:requests", "bb:waits", "bb:schedulers", "bb:semaphores", "bb:tempdb", "bb:filestats", "bb:ag"}

// bannedColumns are exfiltration tokens: a case-insensitive substring match
// anywhere in the text (literals included) is forbidden.
var bannedColumns = []string{
	"sql_text", "dm_exec_sql_text", "query_plan", "input_buffer", "_handle",
	"statement_", "resource_description", "program_name", "host_name", "login",
	"physical_name", "master_files", "availability_replicas",
}

// bannedKeywords are statement/execution tokens, matched as words after string
// literals are removed. This keeps the benign wait-filter data (N'WAITFOR',
// N'SP_SERVER_DIAGNOSTICS_SLEEP') from reading as the WAITFOR statement or an
// sp_ procedure, and keeps dm_exec_* / grantee_count from reading as EXEC/GRANT.
var bannedKeywords = regexp.MustCompile(`(?i)\bEXEC|\bsp_|\bxp_|\b(?:INSERT|UPDATE|DELETE|MERGE|ALTER|CREATE|DROP|GRANT|DBCC|KILL|OPENROWSET|WAITFOR)\b`)

var stringLit = regexp.MustCompile(`'(?:[^']|'')*'`)

// bannedToken returns the first forbidden token in text, or "" when clean.
func bannedToken(text string) string {
	lower := strings.ToLower(text)
	for _, tok := range bannedColumns {
		if strings.Contains(lower, tok) {
			return tok
		}
	}
	if m := bannedKeywords.FindString(stringLit.ReplaceAllString(text, "''")); m != "" {
		return m
	}
	return ""
}

// TestLintC2 proves every statement is the C3 SET prefix + exactly one SELECT +
// its tag, with none of the banned tokens.
func TestLintC2(t *testing.T) {
	for _, id := range requiredIDs {
		if _, ok := Statements[id]; !ok {
			t.Fatalf("required statement %q is missing", id)
		}
	}
	for id, text := range Statements {
		t.Run(id, func(t *testing.T) {
			if !strings.HasPrefix(text, setPrefix) {
				t.Error("does not start with the C3 SET prefix")
			}
			if n := strings.Count(strings.ToUpper(text), "SELECT"); n != 1 {
				t.Errorf("want exactly 1 SELECT, got %d", n)
			}
			if tag := "/* unravis:" + id + " */"; !strings.Contains(text, tag) {
				t.Errorf("missing tag %q", tag)
			}
			if tok := bannedToken(text); tok != "" {
				t.Errorf("contains banned token %q", tok)
			}
		})
	}
	// The lint must have teeth: injected danger is flagged.
	for _, bad := range []string{
		setPrefix + "SELECT 1; EXEC sp_who /* unravis:bb:x */",
		setPrefix + "SELECT sql_text FROM sys.dm_exec_sql_text(0x0) /* unravis:bb:x */",
		setPrefix + "SELECT program_name FROM sys.dm_exec_sessions /* unravis:bb:x */",
		setPrefix + "SELECT 1; WAITFOR DELAY '00:00:05' /* unravis:bb:x */",
		setPrefix + "SELECT login FROM sys.syslogins /* unravis:bb:x */",
	} {
		if bannedToken(bad) == "" {
			t.Errorf("lint failed to flag dangerous text: %q", bad)
		}
	}
	if n := strings.Count(strings.ToUpper(setPrefix+"SELECT 1; SELECT 2 /* unravis:bb:x */"), "SELECT"); n == 1 {
		t.Error("lint failed to flag a second SELECT")
	}
}

// TestLintTOPCaps pins the C6 defense-in-depth TOP n on the row-returning
// statements; the reader also stops at cap+1, so the cap holds at both ends.
func TestLintTOPCaps(t *testing.T) {
	for id, want := range map[string]string{
		"bb:requests":   "SELECT TOP 256 ",
		"bb:waits":      "SELECT TOP 20 ",
		"bb:semaphores": "SELECT TOP 16 ",
		"bb:filestats":  "SELECT TOP 256 ",
		"bb:ag":         "SELECT TOP 64 ",
	} {
		if !strings.Contains(Statements[id], want) {
			t.Errorf("%s: missing %q", id, want)
		}
	}
}

// TestAllowlistExact proves IsAllowed accepts each constant and refuses any
// mutation (added space, token, newline, case change, or a second SELECT).
func TestAllowlistExact(t *testing.T) {
	for id, text := range Statements {
		if !IsAllowed(text) {
			t.Fatalf("IsAllowed false for allowlisted %q", id)
		}
		for _, m := range []string{text + " ", " " + text, text + "\n", text + "; SELECT 1", text + " EXEC xp_cmdshell", strings.ToUpper(text)} {
			if IsAllowed(m) {
				t.Fatalf("IsAllowed true for a mutation of %q: %q", id, m)
			}
		}
	}
	if IsAllowed("") || IsAllowed(setPrefix) {
		t.Fatal("IsAllowed true for empty text or the bare prefix")
	}
}

// TestUnknownStatementRefused proves Statement never builds or returns a query
// for an id outside the allowlist.
func TestUnknownStatementRefused(t *testing.T) {
	for _, id := range []string{"bb:evil", "", "bb:requests ", "BB:REQUESTS", "requests", "bb:requests; DROP TABLE x", "'; --"} {
		if text, ok := Statement(id); ok || text != "" {
			t.Fatalf("Statement(%q) = (%q, %v); want (\"\", false)", id, text, ok)
		}
	}
}

// fakeQueryer records every text sent to it.
type fakeQueryer struct{ sent []string }

func (f *fakeQueryer) Query(_ context.Context, text string) error {
	f.sent = append(f.sent, text)
	return nil
}

// sendByID models the runner's only send path: look up the fixed statement,
// refuse unknown ids, and re-check the allowlist before sending.
func sendByID(ctx context.Context, q *fakeQueryer, id string) bool {
	text, ok := Statement(id)
	if !ok || !IsAllowed(text) {
		return false
	}
	return q.Query(ctx, text) == nil
}

// TestFakeQueryerOnlyAllowlisted proves nothing outside the allowlist is ever
// sent, and that Statement never yields a non-allowlisted text.
func TestFakeQueryerOnlyAllowlisted(t *testing.T) {
	ctx := context.Background()
	q := &fakeQueryer{}
	for id := range Statements {
		if !sendByID(ctx, q, id) {
			t.Fatalf("sendByID refused allowlisted id %q", id)
		}
	}
	for _, text := range q.sent {
		if !IsAllowed(text) {
			t.Fatalf("non-allowlisted text sent: %q", text)
		}
	}
	before := len(q.sent)
	if sendByID(ctx, q, "bb:evil") || len(q.sent) != before {
		t.Fatal("an unknown id caused a send")
	}
	for id := range Statements {
		if text, ok := Statement(id); !ok || !IsAllowed(text) {
			t.Fatalf("Statement(%q) returned a non-allowlisted text", id)
		}
	}
}

var aliasRe = regexp.MustCompile(`(?i)\bAS\s+([A-Za-z_][A-Za-z0-9_]*)\s*$`)
var identRe = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\s*$`)

// outputColumns extracts the result column names of the single SELECT (the
// alias when present, else the trailing identifier), splitting only on commas
// at paren depth 0 so expressions like COUNT(DISTINCT CASE ...) stay intact.
func outputColumns(t *testing.T, text string) []string {
	t.Helper()
	up := strings.ToUpper(text)
	si := strings.Index(up, "SELECT")
	if si < 0 {
		t.Fatal("no SELECT found")
	}
	rest := up[si+len("SELECT"):]
	fi := strings.Index(rest, "FROM")
	if fi < 0 {
		t.Fatal("no FROM after SELECT")
	}
	list := text[si+len("SELECT") : si+len("SELECT")+fi]
	var cols []string
	depth, start := 0, 0
	flush := func(end int) {
		part := strings.TrimSpace(list[start:end])
		start = end + 1
		if part == "" {
			return
		}
		if m := aliasRe.FindStringSubmatch(part); m != nil {
			cols = append(cols, m[1])
		} else if m := identRe.FindStringSubmatch(part); m != nil {
			cols = append(cols, m[1])
		} else {
			t.Fatalf("cannot parse an output column from %q", part)
		}
	}
	for i, r := range list {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				flush(i)
			}
		}
	}
	flush(len(list))
	return cols
}

// TestNoBannedTokenInOutput proves the OUTPUT column names carry no session,
// login, host, program, resource, path, handle, offset or replica-server data.
func TestNoBannedTokenInOutput(t *testing.T) {
	banned := []string{
		"session_id", "login", "program_name", "host_name", "resource_description",
		"physical_name", "sql_text", "query_plan", "sql_handle", "plan_handle",
		"statement_start_offset", "statement_end_offset", "replica_server_name", "server_name",
	}
	for id, text := range Statements {
		cols := outputColumns(t, text)
		if len(cols) == 0 {
			t.Fatalf("%q: no output columns parsed", id)
		}
		for _, c := range cols {
			lc := strings.ToLower(c)
			for _, b := range banned {
				if strings.Contains(lc, b) {
					t.Errorf("%q: output column %q contains banned %q", id, c, b)
				}
			}
		}
	}
}