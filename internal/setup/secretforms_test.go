package setup

import (
	"math/rand"
	"strings"
	"testing"
	"time"
)

const fakePW = "FAKEPW"

func blank(s string) string { return strings.ReplaceAll(s, fakePW, "<LEAK>") }

// Secret forms the first sweep left open (OTA-2 follow-up): escaped quotes in JSON, KEY=value with a prefix, XML, a login in a URL.
func TestRedactSecretFormsFromTheSweep(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"json escaped quote", `{"password":"FAKEPW\"x y","user":"bob"}`, `{"password":"[hidden]","user":"bob"}`},
		{"python repr escaped quote", `{'password': 'it\'s FAKEPW', 'user': 'bob'}`, `{'password': '[hidden]', 'user': 'bob'}`},
		{"json inside a json string", `{"body":"{\"password\":\"FAKEPW x\",\"u\":1}"}`, `{"body":"{\"password\":\"[hidden]\",\"u\":1}"}`},
		{"DB_PASSWORD", "start DB_PASSWORD=FAKEPW two", "start DB_PASSWORD=[hidden]"},
		{"DB_PASSWORD then another variable", "DB_PASSWORD=FAKEPW DB_HOST=db01", "DB_PASSWORD=[hidden] DB_HOST=db01"},
		{"PWD", "PWD=FAKEPW", "PWD=[hidden]"},
		{"SECRET", "SECRET=FAKEPW", "SECRET=[hidden]"},
		{"TOKEN", "GITHUB_TOKEN=FAKEPW done", "GITHUB_TOKEN=[hidden] done"},
		{"API_KEY", "API_KEY=FAKEPW", "API_KEY=[hidden]"},
		{"colon form with a prefix", "db_password: FAKEPW two", "db_password: [hidden]"},
		{"export with quotes", `export MYSQL_PWD="FAKEPW a b"`, `export MYSQL_PWD="[hidden]"`},
		{"XML element", "<Password>FAKEPW a;b</Password>", "<Password>[hidden]</Password>"},
		{"appSettings add key", `<add key="Password" value="FAKEPW x"/>`, `<add key="Password" value="[hidden]"/>`},
		{"password attribute", `<Login user="bob" password="FAKEPW x" />`, `<Login user="bob" password=[hidden] />`},
		{"connectionString attribute", `<add name="Db" connectionString="Server=a;User Id=sa;Password=FAKEPW;" providerName="p"/>`, `<add name="Db" connectionString="[hidden]" providerName="p"/>`},
		{"url login", "connect postgres://u:FAKEPW%40x@h:5432/db", "connect postgres://[hidden]@h:5432/db"},
		{"url login with an @ in the password", "open mongodb://admin:FAKEPW@x@host/db", "open mongodb://[hidden]@host/db"},
		{"ordinary line", "copy-files: ok; token_count=5; Encrypt=true", "copy-files: ok; token_count=5; Encrypt=true"},
		{"ordinary xml", `<add key="Timeout" value="30"/><Host>db01</Host>`, `<add key="Timeout" value="30"/><Host>db01</Host>`},
		{"url without a login", "https://host.example/docs?a=b", "https://host.example/docs?a=b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Redact(tc.in)
			if got != tc.want {
				t.Errorf("got %q, want %q", blank(got), blank(tc.want))
			}
		})
	}
}

func TestRedactHostileInputIsFast(t *testing.T) {
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
		_ = Redact(in)
		if d := time.Since(start); d > 10*time.Second {
			t.Errorf("%s: took %v", name, d)
		}
	}
}

func TestRedactRandomInputNeverPanics(t *testing.T) {
	r := rand.New(rand.NewSource(8))
	frags := []string{"password", "DB_PWD", "=", ":", `"`, `'`, `\`, "{", "}", ";", "\n", " ", "<", ">", "</", "/>", "<![CDATA[", "key", "value", "://", "@", "x", "é", "\xff"}
	for i := 0; i < 20000; i++ {
		var b strings.Builder
		for n := r.Intn(25); n >= 0; n-- {
			b.WriteString(frags[r.Intn(len(frags))])
		}
		_ = Redact(b.String())
	}
}
