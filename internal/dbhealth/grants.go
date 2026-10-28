package dbhealth

import (
	"encoding/json"
	"strings"
	"unicode"
)

// Grant scripts are text shown to the database owner. Unravis never runs one (decision A1): there is no function in this package that sends a
// script to a server, and the SQL and Mongo lanes below only read. The names a person typed are placed inside brackets (SQL) or a JSON string
// (Mongo) after a strict check, and a name that fails the check is replaced by a visible placeholder, so a script can never carry a second statement.

// SQL lane check ids, in the order they run. Each one needs exactly one grant (decision A30).
const (
	CheckServer     = "server"
	CheckErrorLog   = "errorlog"
	CheckQueryStore = "querystore"
	CheckAgentJobs  = "agentjobs"
)

// SQLEra says which grant names a server understands: 2022 and later have the narrower VIEW ... PERFORMANCE STATE permissions.
type SQLEra string

const (
	Era2019 SQLEra = "2019"
	Era2022 SQLEra = "2022"
)

// EraOf reads the product version ("16.0.1000.6") and picks the grant names. A blank or unreadable version is treated as 2022, the version
// the production server runs, because that is what the script is for; the script says which version it was written for.
func EraOf(version string) SQLEra {
	major := 0
	for _, r := range strings.TrimSpace(version) {
		if r < '0' || r > '9' {
			break
		}
		major = major*10 + int(r-'0')
		if major > 1000 {
			break
		}
	}
	if major > 0 && major < 16 {
		return Era2019
	}
	return Era2022
}

// ParseEra turns the "2019" or "2022" the browser sends into an era. Anything else is refused.
func ParseEra(s string) (SQLEra, bool) {
	switch strings.TrimSpace(s) {
	case "2019":
		return Era2019, true
	case "2022":
		return Era2022, true
	}
	return "", false
}

const maxIdent = 128

// QuoteIdent returns name as a bracketed SQL Server identifier. It refuses empty or long names, control characters, a semicolon, line breaks and
// comment markers; the closing bracket is doubled the way SQL Server expects.
func QuoteIdent(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > maxIdent {
		return "", false
	}
	for _, r := range name {
		if unicode.IsControl(r) || r == ';' {
			return "", false
		}
	}
	if strings.Contains(name, "--") || strings.Contains(name, "/*") || strings.Contains(name, "*/") {
		return "", false
	}
	return "[" + strings.ReplaceAll(name, "]", "]]") + "]", true
}

func identOr(name, placeholder string) string {
	if q, ok := QuoteIdent(name); ok {
		return q
	}
	return "[" + placeholder + "]"
}

// GrantFix is what a check that cannot run shows next to its "Cannot test yet": which permission is missing and the lines the owner runs.
type GrantFix struct {
	Permission string `json:"permission"`
	Script     string `json:"script"`
}

// SQLCheckGrant is the fix for one SQL lane check. login and database are what the person typed (either may be blank).
func SQLCheckGrant(check string, era SQLEra, login, database string) GrantFix {
	l := identOr(login, "login")
	d := identOr(database, "ApiDb")
	switch check {
	case CheckServer:
		if era == Era2019 {
			return GrantFix{"VIEW SERVER STATE", "-- Server level, run by a SQL Server administrator.\nGRANT VIEW SERVER STATE TO " + l + ";\n"}
		}
		return GrantFix{"VIEW SERVER PERFORMANCE STATE", "-- Server level, run by a SQL Server administrator.\nGRANT VIEW SERVER PERFORMANCE STATE TO " + l + ";\n"}
	case CheckErrorLog:
		if era == Era2019 {
			return GrantFix{"securityadmin or sysadmin (SQL Server 2019 has no narrower grant for the error log)",
				"-- SQL Server 2019 and older cannot grant the error log on its own.\n-- Either leave this check as it is (the other three still work), or ask the owner whether to accept\n-- that this login reads the log through a member of securityadmin. Unravis does not ask for that.\n"}
		}
		return GrantFix{"VIEW ANY ERROR LOG", "-- Server level, run by a SQL Server administrator.\nGRANT VIEW ANY ERROR LOG TO " + l + ";\n"}
	case CheckQueryStore:
		perm := "VIEW DATABASE PERFORMANCE STATE"
		if era == Era2019 {
			perm = "VIEW DATABASE STATE"
		}
		return GrantFix{perm, "-- In the application database, run by its owner.\nUSE " + d + ";\nCREATE USER " + l + " FOR LOGIN " + l + ";\nGRANT " + perm + " TO " + l + ";\n"}
	case CheckAgentJobs:
		return GrantFix{"SQLAgentReaderRole in msdb", "-- In msdb, run by a SQL Server administrator.\nUSE msdb;\nCREATE USER " + l + " FOR LOGIN " + l + ";\nALTER ROLE SQLAgentReaderRole ADD MEMBER " + l + ";\n"}
	}
	return GrantFix{}
}

// SQLGrantScript is the whole script for one version: every grant the four checks need, in one place.
func SQLGrantScript(era SQLEra, login, database string) string {
	l := identOr(login, "login")
	var b strings.Builder
	b.WriteString("-- Unravis: read-only access for SQL Server " + string(era) + ".\n")
	b.WriteString("-- Run once by a SQL Server administrator. Unravis never runs this for you and only reads.\n\n")
	b.WriteString("-- Only if the login does not exist yet (pick one):\n")
	b.WriteString("-- CREATE LOGIN " + l + " WITH PASSWORD = N'<set by the database owner>', CHECK_POLICY = ON;\n")
	b.WriteString("-- CREATE LOGIN " + l + " FROM WINDOWS;\n\n")
	for i, c := range []string{CheckServer, CheckErrorLog, CheckQueryStore, CheckAgentJobs} {
		g := SQLCheckGrant(c, era, login, database)
		b.WriteString("-- " + string(rune('1'+i)) + ". " + g.Permission + "\n")
		b.WriteString(g.Script + "\n")
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func jsString(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" || len([]rune(s)) > maxIdent {
		return "", false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return "", false
		}
	}
	b, err := json.Marshal(s)
	if err != nil {
		return "", false
	}
	// json.Marshal escapes <, > and & as <; undo that so the script stays readable. The result is still one JSON string.
	out := strings.NewReplacer(`<`, "<", `>`, ">", `&`, "&").Replace(string(b))
	return out, true
}

func jsOr(s, placeholder string) string {
	if q, ok := jsString(s); ok {
		return q
	}
	return `"<` + placeholder + `>"`
}

// MongoGrantScript is the read-only user for the Mongo lane: clusterMonitor on admin and read on the audit database (decision A29).
func MongoGrantScript(login, database string) string {
	var b strings.Builder
	b.WriteString("// Unravis: read-only access for MongoDB.\n")
	b.WriteString("// Run once in mongosh by the MongoDB administrator. Unravis never runs this for you and only reads.\n")
	b.WriteString("db.getSiblingDB(\"admin\").createUser({\n")
	b.WriteString("  user: " + jsOr(login, "login") + ", pwd: passwordPrompt(),\n")
	b.WriteString("  roles: [ { role: \"clusterMonitor\", db: \"admin\" },\n")
	b.WriteString("           { role: \"read\", db: " + jsOr(database, "AuditDb") + " } ]\n")
	b.WriteString("})\n")
	return b.String()
}

// ProfilerGuide is the "Switched-on" guide: what the profiler is, what it costs, how to see whether it is on and how to switch it on or off.
// The scripts are for the owner to run; Unravis only reads the level (profile: -1).
type ProfilerGuide struct {
	Title     string   `json:"title"`
	Why       string   `json:"why"`
	Cost      string   `json:"cost"`
	Steps     []string `json:"steps"`
	Check     string   `json:"check"`
	SwitchOn  string   `json:"switchOn"`
	SwitchOff string   `json:"switchOff"`
}

// MongoProfilerGuide builds the guide for one database.
func MongoProfilerGuide(database string) ProfilerGuide {
	d := jsOr(database, "AuditDb")
	return ProfilerGuide{
		Title: "Switch the MongoDB profiler on",
		Why:   "The profiler keeps a short record of slow operations in the audit database, with how each one was answered (an index, or a read of the whole collection). Without it Unravis cannot say whether a slow search was reading everything.",
		Cost:  "It writes one small record for each operation slower than 100 ms, on the server being diagnosed. It needs a short quiet moment because the record collection is replaced. Switch it off again when the case is closed.",
		Steps: []string{
			"Pick a quiet moment. The record collection is dropped and created again, so older profiler records are lost.",
			"Run the script below in mongosh as a MongoDB administrator.",
			"Come back here and read the database again. The step turns to Ready once slow operations are being recorded.",
		},
		Check:     "// Read-only: shows the level (0 off, 1 slow operations, 2 everything).\nconst d = db.getSiblingDB(" + d + ");\nd.getProfilingStatus()\n",
		SwitchOn:  "const d = db.getSiblingDB(" + d + ");\nd.setProfilingLevel(0);\nd.system.profile.drop();\nd.createCollection(\"system.profile\", { capped: true, size: 100 * 1024 * 1024 });\nd.setProfilingLevel(1, { slowms: 100, sampleRate: 1.0 });\n",
		SwitchOff: "const d = db.getSiblingDB(" + d + ");\nd.setProfilingLevel(0);\n",
	}
}
