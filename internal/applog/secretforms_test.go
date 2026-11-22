package applog

import (
	"bytes"
	"log/slog"
	"math/rand"
	"strings"
	"testing"
	"time"
)

// The fake secret is FAKEPW; a failing case prints the output with it blanked.
const fakePW = "FAKEPW"

func blank(s string) string { return strings.ReplaceAll(s, fakePW, "<LEAK>") }

// Secret forms the first sweep left open (OTA-2 follow-up): escaped quotes in JSON, KEY=value with a prefix, XML.
func TestSanitizeSecretFormsFromTheSweep(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"json escaped quote", `{"password":"FAKEPW\"x y","user":"bob"}`, `{"password":"[redacted]","user":"bob"}`},
		{"python repr escaped quote", `{'password': 'it\'s FAKEPW', 'user': 'bob'}`, `{'password': '[redacted]', 'user': 'bob'}`},
		{"json inside a json string", `{"body":"{\"password\":\"FAKEPW x\",\"u\":1}"}`, `{"body":"{\"password\":\"[redacted]\",\"u\":1}"}`},
		{"DB_PASSWORD", "start DB_PASSWORD=FAKEPW two", "start DB_PASSWORD=[redacted]"},
		{"DB_PASSWORD then another variable", "DB_PASSWORD=FAKEPW DB_HOST=db01", "DB_PASSWORD=[redacted] DB_HOST=db01"},
		{"PWD", "PWD=FAKEPW", "PWD=[redacted]"},
		{"SECRET", "SECRET=FAKEPW", "SECRET=[redacted]"},
		{"CLIENT_SECRET", "CLIENT_SECRET=FAKEPW", "CLIENT_SECRET=[redacted]"},
		{"TOKEN", "GITHUB_TOKEN=FAKEPW done", "GITHUB_TOKEN=[redacted] done"},
		{"API_KEY", "API_KEY=FAKEPW", "API_KEY=[redacted]"},
		{"colon form with a prefix", "db_password: FAKEPW two", "db_password: [redacted]"},
		{"export with quotes", `export MYSQL_PWD="FAKEPW a b"`, `export MYSQL_PWD="[redacted]"`},
		{"XML element", "<Password>FAKEPW a;b</Password>", "<Password>[redacted]</Password>"},
		{"XML element on its own lines", "<Password>\n  FAKEPW\n</Password>", "<Password>\n  [redacted]\n</Password>"},
		{"appSettings add key", `<add key="Password" value="FAKEPW x"/>`, `<add key=[redacted] value="[redacted]"/>`},
		{"password attribute", `<Login user="bob" password="FAKEPW x" />`, `<Login user="bob" password=[redacted] />`},
		{"connectionString attribute", `<add name="Db" connectionString="Server=a;User Id=sa;Password=FAKEPW;" providerName="p"/>`, `<add name="Db" connectionString="[redacted]" providerName="p"/>`},
		{"url login with an @ and a space in the password", "dial mongodb://admin:FAKEPW w0rd@host/db failed", "dial mongodb://[redacted]@host/db failed"},
		// Ordinary text stays.
		{"ordinary line", "source west read 120 rows in 45 ms; token_count=5 max_tokens=100", "source west read 120 rows in 45 ms; token_count=5 max_tokens=100"},
		{"ordinary xml", `<add name="Timeout" value="30"/><Host>db01</Host>`, `<add name="Timeout" value="30"/><Host>db01</Host>`},
		{"password policy words", "password_min_length=8; the password was reset", "password_min_length=8; the password was reset"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Sanitize(tc.in)
			if got != tc.want {
				t.Errorf("got %q, want %q", blank(got), blank(tc.want))
			}
		})
	}
}

// A secret passed as a slog attribute is hidden by its key, whatever the value looks like.
func TestSlogAttributeKeysHideTheValue(t *testing.T) {
	var buf bytes.Buffer
	l := slog.New(sanitizeHandler{inner: slog.NewTextHandler(&buf, nil)})
	l.Info("x", "password", "FAKEPW two words", "db_password", "FAKEPW", "api_key", "FAKEPW", "token_count", "5", "user", "bob")
	out := buf.String()
	if strings.Contains(out, fakePW) {
		t.Fatalf("a fake secret reached the log: %s", blank(out))
	}
	if !strings.Contains(out, "token_count=5") || !strings.Contains(out, "user=bob") {
		t.Errorf("ordinary attributes changed: %s", out)
	}
}

func TestSanitizeHostileInputIsFast(t *testing.T) {
	const size = 1 << 17
	rep := func(unit string) string { return strings.Repeat(unit, size/len(unit)+1)[:size] }
	for name, in := range map[string]string{
		"password=":    rep("password="),
		"open quotes":  `password="` + rep(`"'{`),
		"escaped":      `{"password":"` + rep(`\"`),
		"xml":          rep("<Password>"),
		"xml key attr": rep(`<add key="password" `),
		"long ident":   rep("DB_PASSWORD_"),
		"url markers":  rep("a://b@"),
		"spaces":       "password=" + rep(" \n"),
	} {
		start := time.Now()
		_ = Sanitize(in)
		if d := time.Since(start); d > 10*time.Second {
			t.Errorf("%s: took %v", name, d)
		}
	}
}

func TestSanitizeRandomInputNeverPanics(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	frags := []string{"password", "DB_PWD", "=", ":", `"`, `'`, `\`, "{", "}", ";", "\n", " ", "<", ">", "</", "/>", "<![CDATA[", "key", "value", "://", "@", "x", "é", "\xff"}
	for i := 0; i < 20000; i++ {
		var b strings.Builder
		for n := r.Intn(25); n >= 0; n-- {
			b.WriteString(frags[r.Intn(len(frags))])
		}
		_ = Sanitize(b.String())
	}
}
