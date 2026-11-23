package privacy

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// Each case holds a canary that must not survive, in the form a driver or a tool error really writes it.
func TestScrubTextRemovesCanaries(t *testing.T) {
	cases := []struct{ name, in, canary string }{
		{"ado password", "Cannot open: Server=db01.corp.local;Database=Orders;User Id=sa;Password=CANARY1;", "CANARY1"},
		{"ado server", "Cannot open: Server=db01.corp.local;Database=Orders;Password=x;", "db01.corp.local"},
		{"ado database", "Server=a;Initial Catalog=CANARYdb;Integrated Security=true", "CANARYdb"},
		{"ado user", "Data Source=h;User ID=CANARYuser;", "CANARYuser"},
		{"quoted value", `Server="CANARYhost,1433";x=1`, "CANARYhost"},
		{"mongodb uri", "connect failed mongodb://admin:CANARY2@cluster0.example.net:27017/?authSource=admin", "CANARY2"},
		{"mongodb host", "connect failed mongodb+srv://u:p@CANARYcluster.example.net/db", "CANARYcluster"},
		{"postgres uri", "dial postgres://u:p@CANARYpg:5432/orders?sslmode=disable", "CANARYpg"},
		{"jdbc", "jdbc:sqlserver://CANARYjdbc:1433;databaseName=x", "CANARYjdbc"},
		{"https login", "GET https://svc:CANARY3@api.example.com/v1/users/42?token=abc", "CANARY3"},
		{"https path id", "GET https://api.example.com/v1/users/CANARYid/orders?uid=77", "CANARYid"},
		{"https query id", "GET https://api.example.com/v1/x?userId=CANARYq", "CANARYq"},
		{"windows path", `open C:\Users\CANARYuser\AppData\Roaming\Unravis\case.db: access denied`, "CANARYuser"},
		{"windows path spaces", `open C:\Program Files\CANARYdir\x.exe failed`, "CANARYdir"},
		{"windows forward", `open C:/Users/CANARYfwd/x.db failed`, "CANARYfwd"},
		{"unc", `open \\CANARYsrv\share\case.db failed`, "CANARYsrv"},
		{"env path", `open %APPDATA%\CANARYenv\x failed`, "CANARYenv"},
		{"file uri", "open file:///C:/Users/CANARYuri/x.db failed", "CANARYuri"},
		{"unix home", "open /home/CANARYhome/x.db failed", "CANARYhome"},
		{"unix quoted", `open "/Users/CANARYmac/x.db" failed`, "CANARYmac"},
		{"sql string", "Incorrect syntax: SELECT * FROM Users WHERE Email = 'CANARY@example.com' AND x = 1", "CANARY@example.com"},
		{"sql nstring", "failed: UPDATE Orders SET Note = N'CANARYnote' WHERE Id = 5", "CANARYnote"},
		{"sql insert", "INSERT INTO t (a) VALUES ('CANARYins')", "CANARYins"},
		{"sql number", "SELECT * FROM t WHERE UserId = 987654321 AND y = 2", "987654321"},
		{"sql login", "CREATE LOGIN bob WITH PASSWORD = 'CANARYlogin'", "CANARYlogin"},
		{"json secret", `{"error":"x","password":"CANARY4"}`, "CANARY4"},
		{"flag secret", "run failed: tool --password CANARY5 --verbose", "CANARY5"},
	}
	for _, c := range cases {
		out := ScrubText(c.in)
		if strings.Contains(out, c.canary) {
			t.Errorf("%s: %q survives in %q", c.name, c.canary, out)
		}
	}
}

func TestScrubTextKeepsOrdinaryText(t *testing.T) {
	keep := []string{
		"The script made more than 50 tool calls.",
		"audit_compare failed: source not found",
		"result bigger than 1 MB, return a summary",
		"call 3 of 50 timed out after 30000 ms",
		"unknown field \"dim\" in breakdown request",
		"Cannot read properties of undefined (reading 'rows')", // a JS error: quotes but no SQL
		"it's fine, don't panic",
		"GET https://api.example.com",
	}
	for _, in := range keep {
		if out := ScrubText(in); out != in {
			t.Errorf("changed: %q -> %q", in, out)
		}
	}
}

func TestScrubTextKeepsServiceHost(t *testing.T) {
	out := ScrubText("GET https://api.example.com/v1/users/42 failed")
	if !strings.Contains(out, "https://api.example.com/") || strings.Contains(out, "42") {
		t.Fatalf("got %q", out)
	}
}

func TestScrubErrorCapAndUTF8(t *testing.T) {
	in := strings.Repeat("é", 4000)
	out := ScrubError(in)
	if len(out) > MaxErrorBytes || !utf8.ValidString(out) {
		t.Fatalf("len %d valid %v", len(out), utf8.ValidString(out))
	}
	if !strings.HasSuffix(out, "...") {
		t.Fatal("cut is not marked")
	}
	if ScrubError("short") != "short" {
		t.Fatal("short text changed")
	}
	if bad := ScrubError("a\xffb"); !utf8.ValidString(bad) {
		t.Fatal("invalid UTF-8 left in")
	}
	if ScrubError("") != "" {
		t.Fatal("empty changed")
	}
}

func TestScrubIdempotent(t *testing.T) {
	in := `Server=h;Password=CANARY;path C:\Users\x\a.db and mongodb://u:p@h/db SELECT * FROM t WHERE a = 'z'`
	once := ScrubText(in)
	if twice := ScrubText(once); twice != once {
		t.Fatalf("not idempotent:\n%q\n%q", once, twice)
	}
}

func TestScrubLinearOnHostileInput(t *testing.T) {
	for name, in := range map[string]string{
		"backslashes": strings.Repeat(`C:\a b\`, 1<<17),
		"servers":     strings.Repeat("server=", 1<<17),
		"quotes":      strings.Repeat("select ' from ", 1<<16),
		"slashes":     strings.Repeat("/home/", 1<<17),
		"urls":        strings.Repeat("https://a@", 1<<17),
		"huge":        strings.Repeat("x", 8<<20),
	} {
		start := time.Now()
		out := ScrubText(in)
		if d := time.Since(start); d > 20*time.Second {
			t.Errorf("%s: %v", name, d)
		}
		if len(out) > 2*maxScrubInput {
			t.Errorf("%s: output %d bytes", name, len(out))
		}
	}
}

// FuzzScrubCanary plants a secret in a connection string after arbitrary text and checks it does not survive, and that the text
// stays valid UTF-8 and bounded.
func FuzzScrubCanary(f *testing.F) {
	for _, s := range []string{"", "x", "'", "\"", "{", "<", "-- ", "/*", "Server=", "C:\\", "mongodb://", "\\\\", "select ", "\n"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, pre string) {
		in := pre + "\nServer=h;Database=d;User Id=u;Password=CANARYsecret9;\n"
		start := time.Now()
		out := ScrubError(in)
		if d := time.Since(start); d > 750*time.Millisecond {
			t.Fatalf("slow: %v for %d bytes", d, len(in))
		}
		if strings.Contains(out, "CANARYsecret9") {
			t.Fatalf("secret survives: %q", out)
		}
		if !utf8.ValidString(out) || len(out) > MaxErrorBytes {
			t.Fatalf("bad output: len %d valid %v", len(out), utf8.ValidString(out))
		}
	})
}
