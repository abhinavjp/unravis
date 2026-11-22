package releasedash

import (
	"math/rand"
	"strings"
	"testing"
	"time"
)

const fakePW = "FAKEPW"

func blank(s string) string { return strings.ReplaceAll(s, fakePW, "<LEAK>") }

// Secret forms the first sweep left open (OTA-2 follow-up) in the Releases app log reader.
func TestRedactorSecretFormsFromTheSweep(t *testing.T) {
	r := NewRedactor()
	for _, tc := range []struct{ name, in string }{
		{"json escaped quote", `{"password":"FAKEPW\"x y","user":"bob"}`},
		{"python repr escaped quote", `{'password': 'it\'s FAKEPW two'}`},
		{"json inside a json string", `{"body":"{\"password\":\"FAKEPW x\",\"u\":1}"}`},
		{"DB_PASSWORD", "start DB_PASSWORD=FAKEPW two"},
		{"colon form", "db_password: FAKEPW two"},
		{"XML element", "<Password>FAKEPW a;b</Password>"},
		{"appSettings add key", `<add key="Password" value="FAKEPW x"/>`},
		{"password attribute", `<Login user="bob" password="FAKEPW x" />`},
		{"url login with an @ and a space", "open mongodb://admin:p@ss FAKEPW@host/db now"},
		{"url login with a space", "postgres://u:SEK FAKEPW@h/db"},
		{"url login with a slash", "ftp://u:SEK/FAKEPW@host"},
		{"json array", `{"password":["FAKEPW"],"u":1}`},
		{"compound token with a colon", "X-Auth-Token: FAKEPW"},
		{"client_secret colon", "client_secret: FAKEPW"},
		{"connectionString attribute", `<add name="Db" connectionString="Server=a;Password=FAKEPW;" providerName="p"/>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := r.Line(tc.in); strings.Contains(got, fakePW) {
				t.Errorf("a fake secret was left: %q", blank(got))
			}
		})
	}
	// Ordinary lines, and lines an earlier redaction already cleaned (the reader runs again over the script's own log), stay as they are.
	for _, in := range []string{
		"ok pkg 1.2s", "TESTS_PASS: 120 of 120", "token_count=5 max_tokens=100",
		`<add key="Timeout" value="30"/><Host>db01</Host>`,
		"push with <token hidden> was refused by the server", "got <private key hidden> and <name> here",
	} {
		if got := r.Line(in); got != in {
			t.Errorf("%q changed to %q", in, got)
		}
	}
}

func TestRedactorHostileInputIsFast(t *testing.T) {
	const size = 1 << 17
	rep := func(unit string) string { return strings.Repeat(unit, size/len(unit)+1)[:size] }
	for name, in := range map[string]string{
		"password=":    rep("password="),
		"escaped":      `{"password":"` + rep(`\"`),
		"xml":          rep("<Password>"),
		"xml comment":  rep("<Password><!--"),
		"key comment":  rep(`<add key="password"><!--`),
		"xml key attr": rep(`<add key="password" `),
		"long ident":   rep("DB_PASSWORD_"),
	} {
		r := NewRedactor()
		start := time.Now()
		_ = r.Line(in)
		if d := time.Since(start); d > 10*time.Second {
			t.Errorf("%s: took %v", name, d)
		}
	}
}

func TestRedactorRandomInputNeverPanics(t *testing.T) {
	r := rand.New(rand.NewSource(10)) //nolint:gosec // fixed seed for repeatable test rounds
	frags := []string{"password", "DB_PWD", "=", ":", `"`, `'`, `\`, "{", "}", ";", " ", "<", ">", "</", "/>", "<![CDATA[", "key", "value", "x", "é", "\xff", "-----BEGIN PRIVATE KEY-----"}
	red := NewRedactor("abc")
	for i := 0; i < 20000; i++ {
		var b strings.Builder
		for n := r.Intn(25); n >= 0; n-- {
			b.WriteString(frags[r.Intn(len(frags))])
		}
		_ = red.Line(b.String())
	}
}
