package evidence

import (
	"math/rand"
	"strings"
	"testing"
	"time"
)

const fakePW = "FAKEPW"

func blank(s string) string { return strings.ReplaceAll(s, fakePW, "<LEAK>") }

// Secret forms the first sweep left open (OTA-1 follow-up): escaped quotes in JSON, KEY=value with a prefix, XML.
func TestScrubSecretsFormsFromTheSweep(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"json escaped quote", `{"password":"FAKEPW\"x y","user":"bob"}`, `{"[removed],"user":"bob"}`},
		{"python repr escaped quote", `{'password': 'it\'s FAKEPW', 'user': 'bob'}`, `{'[removed], 'user': 'bob'}`},
		{"json inside a json string", `{"body":"{\"password\":\"FAKEPW x\",\"u\":1}"}`, `{"body":"{\"password\":\"[removed]\",\"u\":1}"}`},
		{"DB_PASSWORD", "start DB_PASSWORD=FAKEPW two", "start DB_[removed]"},
		{"PWD", "PWD=FAKEPW", "[removed]"},
		{"SECRET", "SECRET=FAKEPW", "[removed]"},
		{"CLIENT_SECRET", "CLIENT_SECRET=FAKEPW", "CLIENT_[removed]"},
		{"SECRET_KEY", "SECRET_KEY=FAKEPW", "SECRET_KEY=[removed]"},
		{"AWS secret", "AWS_SECRET_ACCESS_KEY=FAKEPW", "AWS_SECRET_[removed]"},
		{"TOKEN", "GITHUB_TOKEN=FAKEPW done", "GITHUB_[removed] done"},
		{"API_KEY", "API_KEY=FAKEPW", "[removed]"},
		{"x-api-key colon", "x-api-key: FAKEPW", "x-[removed]"},
		{"XML element", "<Password>FAKEPW a;b</Password>", "<Password>[removed]</Password>"},
		{"XML element on its own lines", "<Password>\n  FAKEPW\n</Password>", "<Password>\n  [removed]\n</Password>"},
		{"appSettings add key", `<add key="Password" value="FAKEPW x"/>`, `<add key="Password" value="[removed]"/>`},
		{"password attribute", `<Login user="bob" password="FAKEPW x" />`, `<Login user="bob" [removed] />`},
		{"connectionString attribute", `<add name="Db" connectionString="Server=a;User Id=sa;Password=FAKEPW;" providerName="p"/>`, `<add name="Db" connectionString="[removed]" providerName="p"/>`},
		{"url login with an @ and a space", "open mongodb://admin:p@ss FAKEPW@host/db now", "open [removed] now"},
		{"url login with a space", "postgres://u:SEK FAKEPW@h/db", "[removed]"},
		{"url login with a slash", "ftp://u:SEK/FAKEPW@host", "[removed]"},
		{"json array", `{"password":["FAKEPW"],"u":1}`, `{"[removed]`},
		{"compound token with a colon", "X-Auth-Token: FAKEPW", "X-Auth-Token: [removed]"},
		{"client_secret colon", "client_secret: FAKEPW", "client_secret: [removed]"},
		{"ordinary note", "the password was reset; token_count=5; max_tokens=100", "the password was reset; token_count=5; max_tokens=100"},
		{"ordinary xml", `<add key="Timeout" value="30"/><Host>db01</Host>`, `<add key="Timeout" value="30"/><Host>db01</Host>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ScrubSecrets(tc.in)
			if got != tc.want {
				t.Errorf("got %q, want %q", blank(got), blank(tc.want))
			}
			if strings.Contains(got, fakePW) {
				t.Errorf("a fake secret was left in the output")
			}
		})
	}
}

func TestScrubSecretsHostileInputIsFast(t *testing.T) {
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
		_ = ScrubSecrets(in)
		if d := time.Since(start); d > 10*time.Second {
			t.Errorf("%s: took %v", name, d)
		}
	}
}

func TestScrubSecretsRandomInputNeverPanics(t *testing.T) {
	r := rand.New(rand.NewSource(9)) //nolint:gosec // fixed seed for repeatable test rounds
	frags := []string{"password", "DB_PWD", "=", ":", `"`, `'`, `\`, "{", "}", ";", "\n", " ", "<", ">", "</", "/>", "<![CDATA[", "key", "value", "://", "@", "x", "é", "\xff"}
	for i := 0; i < 20000; i++ {
		var b strings.Builder
		for n := r.Intn(25); n >= 0; n-- {
			b.WriteString(frags[r.Intn(len(frags))])
		}
		_ = ScrubSecrets(b.String())
	}
}
