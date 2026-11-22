package secretscrub

import (
	"math/rand"
	"strings"
	"testing"
	"time"
)

// Every secret in these tests is an obvious fake (FAKEPW...). A failing case prints its name and the output with the fake blanked, so
// a leak never reaches the test log.
const fake = "FAKEPW"

const m = "[m]"

func shown(s string) string { return strings.ReplaceAll(s, fake, "<LEAK>") }

type tcase struct{ name, in, want string }

func run(t *testing.T, cases []tcase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Mask(tc.in, m)
			if got != tc.want {
				t.Errorf("got %q, want %q", shown(got), shown(tc.want))
			}
			if strings.Contains(got, fake) {
				t.Errorf("a fake secret was left in the output")
			}
			if again := Mask(got, m); again != got {
				t.Errorf("not idempotent: %q then %q", shown(got), shown(again))
			}
		})
	}
}

// Form 1: JSON, Python and ADO values with escaped quotes.
func TestMaskEscapedQuotes(t *testing.T) {
	run(t, []tcase{
		{"json backslash quote", `{"password":"FAKEPW\"x y","user":"bob"}`, `{"password":"[m]","user":"bob"}`},
		{"json backslash quote, spaces around colon", `{"password" : "FAKEPW\"x y" , "user":"bob"}`, `{"password" : "[m]" , "user":"bob"}`},
		{"json value ends with escaped backslash", `{"password":"FAKEPW\\","user":"bob"}`, `{"password":"[m]","user":"bob"}`},
		{"json escaped quote then another", `{"pwd":"a\"FAKEPW\"b","n":1}`, `{"pwd":"[m]","n":1}`},
		{"python repr single quotes", `{'password': 'it\'s FAKEPW two', 'user': 'bob'}`, `{'password': '[m]', 'user': 'bob'}`},
		{"json inside a json string", `{"body":"{\"password\":\"FAKEPW a b\",\"user\":\"bob\"}"}`, `{"body":"{\"password\":\"[m]\",\"user\":\"bob\"}"}`},
		{"json inside a json string with an escaped quote", `{"body":"{\"password\":\"a\\\"FAKEPW\",\"u\":1}"}`, `{"body":"{\"password\":\"[m]\",\"u\":1}"}`},
		{"json number", `{"password": 12345, "user":"bob"}`, `{"password": [m], "user":"bob"}`},
		{"json empty string", `{"password":"","user":"bob"}`, `{"password":"","user":"bob"}`},
		{"json value on its own line", "{\n  \"password\":\n    \"FAKEPW x\"\n}", "{\n  \"password\":\n    \"[m]\"\n}"},
		{"ruby hash rocket", `{ 'password' => 'FAKEPW \' x' }`, `{ 'password' => '[m]' }`},
		{"unclosed quote takes the rest", `{"password":"FAKEPW\"x, \"user\":\"bob`, `{"password":"[m]`},
		{"ado doubled quote", `Server=db;Password='a''FAKEPW b';Encrypt=true`, `Server=db;Password='[m]';Encrypt=true`},
		{"ado doubled double quote", `Password="say ""FAKEPW"" now";Encrypt=true`, `Password="[m]";Encrypt=true`},
		{"ordinary json is kept", `{"user":"bob","note":"it said \"hi\"","count":3}`, `{"user":"bob","note":"it said \"hi\"","count":3}`},
		{"token in json with an escaped quote", `{"access_token":"FAKEPW\"x","n":1}`, `{"access_token":"[m]","n":1}`},
	})
}

// Inputs from the first security review of this package (PR 181). Fake values only.
func TestMaskReviewedBypasses(t *testing.T) {
	run(t, []tcase{
		{"json array value", `{"password":["FAKEPW"]}`, `{"password":[m]}`},
		{"json array of two", `{"password":["FAKEPW","FAKEPW b"],"user":"bob"}`, `{"password":[m],"user":"bob"}`},
		{"json object value", `{"password":{"v":"FAKEPW]}","w":1},"user":"bob"}`, `{"password":[m],"user":"bob"}`},
		{"json array never closes", `{"password":["FAKEPW", "b`, `{"password":[m]`},
		{"X-Auth-Token colon", "X-Auth-Token: FAKEPW", "X-Auth-Token: [m]"},
		{"refresh_token colon", "refresh_token: FAKEPW", "refresh_token: [m]"},
		{"auth_token colon", "auth_token: FAKEPW", "auth_token: [m]"},
		{"bearer_token colon", "bearer_token: FAKEPW", "bearer_token: [m]"},
		{"client_secret colon (yaml)", "client_secret: FAKEPW", "client_secret: [m]"},
		{"authToken colon", "authToken: FAKEPW", "authToken: [m]"},
		{"bare token colon stays a sentence", "token: expired, retry", "token: expired, retry"},
		{"bare secret colon stays a sentence", "secret: handled elsewhere", "secret: handled elsewhere"},
		{"name longer than 128 characters", "DB_PASSWORD" + strings.Repeat("_X", 100) + "=FAKEPW", "DB_PASSWORD" + strings.Repeat("_X", 100) + "=[m]"},
		{"password after a long name", strings.Repeat("a", 300) + "_password=FAKEPW", strings.Repeat("a", 300) + "_password=[m]"},
		{"empty quoted value then text", `password=""FAKEPW`, `password="[m]`},
		{"stray quote cuts the value", `password="FAKEPW"RIT9"`, `password="[m]"`},
		{"quoted value then a separator is unchanged", `password="FAKEPW";x=1`, `password="[m]";x=1`},
		{"comment between the tag and the value", "<Password><!-- note -->FAKEPW</Password>", "<Password><!-- note -->[m]</Password>"},
		{"comment that never ends", "<Password><!-- FAKEPW and the rest", "<Password>[m]"},
	})
}

func TestMaskURLUserinfoReviewedBypasses(t *testing.T) {
	for _, tc := range []tcase{
		{"at sign and space", "mongodb://admin:p@ss FAKEPW@host/db", "mongodb://[m]@host/db"},
		{"space in the password", "postgres://u:SEK FAKEPW@h/db", "postgres://[m]@h/db"},
		{"slash in the password", "ftp://u:SEK/FAKEPW@host", "ftp://[m]@host"},
		{"slash in the password, path after", "ftp://u:SEK/FAKEPW@host/dir/f.txt", "ftp://[m]@host/dir/f.txt"},
		{"host port then an at sign in the path", "https://host:8080/users/@me", "https://host:8080/users/@me"},
		{"no colon, at sign in the path", "https://example.com/@user", "https://example.com/@user"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := MaskURLUserinfo(tc.in, m); got != tc.want {
				t.Errorf("got %q, want %q", shown(got), shown(tc.want))
			}
		})
	}
}

// Inputs from the second security review of this package (PR 181). Fake values only.
func TestMaskSecondReviewBypasses(t *testing.T) {
	run(t, []tcase{
		{"yaml literal block", "password: |\n  FAKEPW\n  FAKEPW two\nnext: 1", "password: |\n  [m]\nnext: 1"},
		{"yaml folded block, strip", "password: >-\n  FAKEPW\n", "password: >-\n  [m]\n"},
		{"yaml client_secret block", "client_secret: |\n  FAKEPW\n", "client_secret: |\n  [m]\n"},
		{"yaml block with a blank line inside", "password: |\n  FAKEPW\n\n  FAKEPW\nnext: 1", "password: |\n  [m]\nnext: 1"},
		{"yaml block under a nested key", "db:\n  password: |\n    FAKEPW\n  host: h\n", "db:\n  password: |\n    [m]\n  host: h\n"},
		{"yaml block with CRLF", "password: |\r\n  FAKEPW\r\nnext: 1", "password: |\r\n  [m]\r\nnext: 1"},
		{"yaml client_secret on the next line", "client_secret:\n  FAKEPW\nnext: 1", "client_secret:\n  [m]\nnext: 1"},
		{"yaml api_key on the next line", "api_key:\n  FAKEPW\n", "api_key:\n  [m]\n"},
		{"compound name with an unindented next line is a sentence", "api_key:\nsee the docs", "api_key:\nsee the docs"},
		{"xml tag with a valueless attribute", "<Password x>FAKEPW</Password>", "<Password x>[m]</Password>"},
		{"xml marker of an earlier redaction", "<token hidden> then more text", "<token hidden> then more text"},
		{"xml tag cut off before another tag", "<a b=<Password>FAKEPW</Password>", "<a b=<Password>[m]</Password>"},
	})
}

func TestMaskURLUserinfoSecondReviewBypasses(t *testing.T) {
	for _, tc := range []tcase{
		{"question mark in the password", "ftp://u:SEK?FAKEPW@host", "ftp://[m]@host"},
		{"hash in the password", "ftp://u:SEK#FAKEPW@host", "ftp://[m]@host"},
		{"redis with an empty user", "redis://:SEK/FAKEPW@h", "redis://[m]@h"},
		{"all digit password with a slash", "ftp://admin:1234/FAKEPW@host", "ftp://[m]@host"},
		{"host port and a path with an at sign", "https://h:8080/users/@me", "https://h:8080/users/@me"},
		{"host port and a name path", "https://h:8080/@me", "https://h:8080/@me"},
		{"host port and a query with an at sign", "http://localhost:8080?mail=a@b.example", "http://localhost:8080?mail=a@b.example"},
		{"two urls, the second has the login", "see a://u:x/y b://v:SEK/FAKEPW@h", "see a://u:x/y b://[m]@h"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := MaskURLUserinfo(tc.in, m); got != tc.want {
				t.Errorf("got %q, want %q", shown(got), shown(tc.want))
			}
		})
	}
}

// Inputs from the third security review of this package (PR 181). Fake values only.
func TestMaskThirdReviewBypasses(t *testing.T) {
	run(t, []tcase{
		{"yaml header with a comment", "password: | # note\n  FAKEPW\n", "password: | # note\n  [m]\n"},
		{"yaml header with a tag", "password: !!str |\n  FAKEPW\n", "password: !!str |\n  [m]\n"},
		{"yaml header with an anchor", "password: &a |\n  FAKEPW\n", "password: &a |\n  [m]\n"},
		{"yaml header with a tag and an anchor and a comment", "password: !!str &a >- # c\n  FAKEPW\n", "password: !!str &a >- # c\n  [m]\n"},
		{"yaml tag with a plain value", "password: !secret db_pw\nnext: 1", "password: [m]\nnext: 1"},
	})
}

// Carry: the same forms, one line at a time (the Releases reader).
func TestCarryMasksWhatGoesOnOverTheNextLines(t *testing.T) {
	for _, tc := range []struct {
		name  string
		lines []string
		want  []string
	}{
		{"block scalar", []string{"db:", "  password: |", "    FAKEPW", "    FAKEPW two", "  host: h"}, []string{"db:", "  password: |", "    [m]", "    [m]", "  host: h"}},
		{"block scalar with a blank line", []string{"password: >-", "  FAKEPW", "", "  FAKEPW", "next: 1"}, []string{"password: >-", "  [m]", "", "  [m]", "next: 1"}},
		{"block with a comment header", []string{"password: | # c", "  FAKEPW"}, []string{"password: | # c", "  [m]"}},
		{"list item block", []string{"- password: |", "    FAKEPW"}, []string{"- password: |", "    [m]"}},
		{"env style value on the next line", []string{"DB_PASSWORD=", "FAKEPW", "next"}, []string{"DB_PASSWORD=", "[m]", "next"}},
		{"colon value on the next line", []string{"password:", "  FAKEPW", "next"}, []string{"password:", "  [m]", "next"}},
		{"yaml client_secret", []string{"client_secret:", "  FAKEPW", "next: 1"}, []string{"client_secret:", "  [m]", "next: 1"}},
		{"yaml api_key", []string{"api_key:", "  FAKEPW"}, []string{"api_key:", "  [m]"}},
		{"xml element on its own lines", []string{"<Password>", "  FAKEPW", "</Password>"}, []string{"<Password>", "  [m]", "</Password>"}},
		{"unindented line after a compound name is kept", []string{"api_key:", "see the docs"}, []string{"api_key:", "see the docs"}},
		{"blank line ends the search", []string{"DB_PASSWORD=", "", "later line"}, []string{"DB_PASSWORD=", "", "later line"}},
		{"ordinary lines", []string{"token_count=5", "  indented text", "password was reset"}, []string{"token_count=5", "  indented text", "password was reset"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var c Carry
			for i, l := range tc.lines {
				got := c.Line(l, m)
				if got != tc.want[i] {
					t.Errorf("line %d: got %q, want %q", i, shown(got), shown(tc.want[i]))
				}
				if strings.Contains(got, fake) {
					t.Errorf("line %d: a fake secret was left", i)
				}
			}
		})
	}
}

// A long name made of many secret words, then a long stretch that the value reader scans and fails on, used to be read again for each
// word (quadratic). The time must grow in step with the size.
func TestMaskLongNamesAreLinear(t *testing.T) {
	shape := func(unit, tail string, n int) string {
		return strings.Repeat(unit, n/len(unit)) + "a" + tail
	}
	spaces := func(n int) string { return strings.Repeat(" ", n) + "x" }
	blanks := func(n int) string { return strings.Repeat("\n", n) }
	slashes := func(n int) string { return strings.Repeat(`\`, n) }
	for _, tc := range []struct {
		name string
		gen  func(n int) string
	}{
		{"password_ then spaces", func(n int) string { return shape("password_", spaces(n/2), n/2) }},
		{"password_ then block and blank lines", func(n int) string { return shape("password_", ": |\n"+blanks(n/2), n/2) }},
		{"pwd. then block and blank lines", func(n int) string { return shape("pwd.", ": |\n"+blanks(n/2), n/2) }},
		{"token- then spaces", func(n int) string { return shape("token-", spaces(n/2), n/2) }},
		{"api_key_ then spaces", func(n int) string { return shape("api_key_", spaces(n/2), n/2) }},
		{"password_ then backslashes", func(n int) string { return shape("password_", slashes(n/4), n/2) }},
		{"password_ then colon and blank lines", func(n int) string { return shape("password_", ":\n"+blanks(n/2), n/2) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			best := func(n int) time.Duration {
				in := tc.gen(n)
				b := time.Hour
				for i := 0; i < 3; i++ {
					start := time.Now()
					_ = Mask(in, m)
					if d := time.Since(start); d < b {
						b = d
					}
				}
				return b
			}
			small, big := best(256<<10), best(1<<20)
			if big > time.Second {
				t.Errorf("1 MiB took %v", big)
			}
			if big > 10*small+100*time.Millisecond { // 4x is linear, 16x is quadratic
				t.Errorf("1 MiB took %v, 256 KiB took %v: not linear", big, small)
			}
		})
	}
}

// Form 2: KEY=value with a prefix, in app logs, setup logs and connection text.
func TestMaskPrefixedKeys(t *testing.T) {
	run(t, []tcase{
		{"DB_PASSWORD", "DB_PASSWORD=FAKEPW", "DB_PASSWORD=[m]"},
		{"DB_PASSWORD with spaces", "start DB_PASSWORD=FAKEPW two words", "start DB_PASSWORD=[m]"},
		{"DB_PASSWORD then another variable", "DB_PASSWORD=FAKEPW DB_HOST=db01", "DB_PASSWORD=[m] DB_HOST=db01"},
		{"DB_PASSWORD quoted", `export DB_PASSWORD="FAKEPW x y"`, `export DB_PASSWORD="[m]"`},
		{"MYSQL_PWD", "MYSQL_PWD=FAKEPW", "MYSQL_PWD=[m]"},
		{"PWD", "PWD=FAKEPW", "PWD=[m]"},
		{"pwd with colon", "pwd: FAKEPW x", "pwd: [m]"},
		{"SECRET", "SECRET=FAKEPW", "SECRET=[m]"},
		{"CLIENT_SECRET", "CLIENT_SECRET=FAKEPW next", "CLIENT_SECRET=[m] next"},
		{"SECRET_KEY", "SECRET_KEY=FAKEPW", "SECRET_KEY=[m]"},
		{"AWS_SECRET_ACCESS_KEY", "AWS_SECRET_ACCESS_KEY=FAKEPW", "AWS_SECRET_ACCESS_KEY=[m]"},
		{"TOKEN", "TOKEN=FAKEPW", "TOKEN=[m]"},
		{"GITHUB_TOKEN", "GITHUB_TOKEN=FAKEPW done", "GITHUB_TOKEN=[m] done"},
		{"access_token json", `"access_token": "FAKEPW"`, `"access_token": "[m]"`},
		{"API_KEY", "API_KEY=FAKEPW", "API_KEY=[m]"},
		{"x-api-key", "x-api-key: FAKEPW", "x-api-key: [m]"},
		{"apiKey camel case", `apiKey="FAKEPW"`, `apiKey="[m]"`},
		{"dbPassword camel case", "dbPassword=FAKEPW", "dbPassword=[m]"},
		{"SIGN_PASS", "SIGN_PASS=FAKEPW", "SIGN_PASS=[m]"},
		{"SIGN_PIN", "SIGN_PIN=FAKEPW", "SIGN_PIN=[m]"},
		{"spring dotted key", "spring.datasource.password=FAKEPW", "spring.datasource.password=[m]"},
		{"flag form", "--password=FAKEPW", "--password=[m]"},
		{"password_hash", "password_hash=FAKEPW", "password_hash=[m]"},
		{"password2", "password2=FAKEPW", "password2=[m]"},
		{"secretkey no separator", "secretkey=FAKEPW", "secretkey=[m]"},
		{"query string", "GET /x?db_password=FAKEPW&y=1", "GET /x?db_password=[m]&y=1"},
		{"query string with spaces ends at the space", "GET /x?token=FAKEPW HTTP/1.1", "GET /x?token=[m] HTTP/1.1"},
		{"several on one line", "DB_PASSWORD=FAKEPW;API_KEY=FAKEPW;x=1", "DB_PASSWORD=[m];API_KEY=[m];x=1"},
		{"value on the next line", "DB_PASSWORD=\n  FAKEPW\nlater", "DB_PASSWORD=\n  [m]\nlater"},
		{"CRLF next line", "PWD:\r\nFAKEPW", "PWD:\r\n[m]"},
		{"braced", "DB_PASSWORD={FAKEPW;a}}b};x=1", "DB_PASSWORD={[m]};x=1"},
		{"connection string with a prefixed key", "Server=db;Db_Pwd=FAKEPW x;Encrypt=true", "Server=db;Db_Pwd=[m];Encrypt=true"},
		{"pass count with a colon is kept", "TESTS_PASS: 120 of 120", "TESTS_PASS: 120 of 120"},
		{"empty value", "DB_PASSWORD=;x=1", "DB_PASSWORD=;x=1"},
		{"blank line ends the search", "DB_PASSWORD=\n\nThe server crashed", "DB_PASSWORD=\n\nThe server crashed"},
		{"before a quote that ends an xml attribute", `<add name="x" connectionString="Server=a;Password=FAKEPW" providerName="p"/>`, `<add name="x" connectionString="[m]" providerName="p"/>`},
		{"password last in an attribute, other attributes stay", `<x v="Password=FAKEPW" id="7"/>`, `<x v="Password=[m]" id="7"/>`},
		{"password last in a json string", `{"msg":"login Password=FAKEPW two","level":"warn"}`, `{"msg":"login Password=[m]","level":"warn"}`},
	})
}

// Form 3: XML.
func TestMaskXML(t *testing.T) {
	run(t, []tcase{
		{"element", "<Password>FAKEPW</Password>", "<Password>[m]</Password>"},
		{"element with spaces and semicolons", "<Password>FAKEPW a;b c</Password>", "<Password>[m]</Password>"},
		{"element in a document", "<db><Host>h</Host><Password>FAKEPW</Password><Port>1</Port></db>", "<db><Host>h</Host><Password>[m]</Password><Port>1</Port></db>"},
		{"element with a namespace", "<ns:ApiKey>FAKEPW</ns:ApiKey>", "<ns:ApiKey>[m]</ns:ApiKey>"},
		{"element with an attribute", `<Password type="plain">FAKEPW</Password>`, `<Password type="plain">[m]</Password>`},
		{"prefixed element", "<DbPassword>FAKEPW</DbPassword>", "<DbPassword>[m]</DbPassword>"},
		{"client secret element", "<client_secret>FAKEPW</client_secret>", "<client_secret>[m]</client_secret>"},
		{"element on its own lines", "<Password>\n    FAKEPW\n</Password>", "<Password>\n    [m]\n</Password>"},
		{"cdata", "<Password><![CDATA[FAKEPW <x>]]></Password>", "<Password><![CDATA[[m]]]></Password>"},
		{"nested value element", "<Password><Value>FAKEPW</Value></Password>", "<Password><Value>[m]</Value></Password>"},
		{"never closed", "<Password>FAKEPW and the rest", "<Password>[m]"},
		{"never closed, stops at the next tag", "<Password>FAKEPW<Next>1</Next>", "<Password>[m]<Next>1</Next>"},
		{"empty element", "<Password></Password><Host>h</Host>", "<Password></Password><Host>h</Host>"},
		{"self closing", "<Password/><Host>h</Host>", "<Password/><Host>h</Host>"},
		{"self closing with value attribute", `<Password value="FAKEPW"/>`, `<Password value="[m]"/>`},
		{"appSettings add key", `<add key="password" value="FAKEPW"/>`, `<add key="password" value="[m]"/>`},
		{"appSettings add key, other order", `<add value="FAKEPW" key="DbPassword" />`, `<add value="[m]" key="DbPassword" />`},
		{"appSettings with single quotes", `<add key='Password' value='FAKEPW x'/>`, `<add key='Password' value='[m]'/>`},
		{"appSettings api key", `<add key="Stripe.ApiKey" value="FAKEPW" />`, `<add key="Stripe.ApiKey" value="[m]" />`},
		{"log4net param", `<param name="Password" value="FAKEPW"/>`, `<param name="Password" value="[m]"/>`},
		{"setting with a value element", `<setting name="Password" serializeAs="String"><value>FAKEPW</value></setting>`, `<setting name="Password" serializeAs="String"><value>[m]</value></setting>`},
		{"properties entry", `<entry key="db.password">FAKEPW</entry>`, `<entry key="db.password">[m]</entry>`},
		{"password attribute", `<Login user="bob" password="FAKEPW x" />`, `<Login user="bob" password="[m]" />`},
		{"pwd attribute single quote", `<Login pwd='FAKEPW' user='bob'/>`, `<Login pwd='[m]' user='bob'/>`},
		{"connectionString attribute with a password", `<add name="Db" connectionString="Server=a;Database=b;User Id=sa;Password=FAKEPW;" providerName="System.Data.SqlClient"/>`, `<add name="Db" connectionString="[m]" providerName="System.Data.SqlClient"/>`},
		{"connectionString attribute with Pwd and spaces", `<add name="Db" connectionString="Data Source=a;Pwd=FAKEPW x y;"/>`, `<add name="Db" connectionString="[m]"/>`},
		{"connectionString attribute with a url login", `<add name="Db" connectionString="mongodb://u:FAKEPW@h/db"/>`, `<add name="Db" connectionString="[m]"/>`},
		{"connectionString attribute with account key", `<add name="St" connectionString="AccountName=a;AccountKey=FAKEPW=="/>`, `<add name="St" connectionString="[m]"/>`},
		{"xml encoded quotes in an attribute", `<x c="Password=&quot;FAKEPW&quot;;a=1"/>`, `<x c="Password=[m];a=1"/>`},
		{"ordinary xml is kept", `<config><add key="Timeout" value="30"/><Host>db01</Host><Name>x</Name></config>`, `<config><add key="Timeout" value="30"/><Host>db01</Host><Name>x</Name></config>`},
		{"policy setting is kept", `<add key="PasswordMinLength" value="8"/><PasswordPolicy>strong</PasswordPolicy>`, `<add key="PasswordMinLength" value="8"/><PasswordPolicy>strong</PasswordPolicy>`},
		{"connectionString without a secret is kept", `<add name="Db" connectionString="Server=a;Database=b;Integrated Security=true"/>`, `<add name="Db" connectionString="Server=a;Database=b;Integrated Security=true"/>`},
		{"marker of an earlier redaction", "got <token hidden> then more text <name> here", "got <token hidden> then more text <name> here"},
		{"comparison and arrows", "a < b and password is a > c", "a < b and password is a > c"},
	})
}

func TestMaskLeavesOrdinaryText(t *testing.T) {
	for _, in := range []string{
		"the password was reset; Encrypt=true on db01",
		"copy-files: ok; Encrypt=true",
		"source west read 120 rows in 45 ms",
		"token: expired, retry",
		"tokens=3 max_tokens=100 token_count=5 tokenizer=bpe",
		"secretary=bob secrets_manager is down",
		"password_min_length=8 password_policy=strict password_required=true",
		"pass=5 fail=0 pin of the map",
		"Passwordless login is on",
		"keyboard=us key=value",
		"pwd is the current directory",
		"mail ops@corp.example or see https://host.example/docs",
		"",
		"x",
	} {
		if got := Mask(in, m); got != in {
			t.Errorf("ordinary text changed: %q -> %q", in, got)
		}
	}
}

func TestIsSecretKey(t *testing.T) {
	for _, k := range []string{"password", "Password", "DB_PASSWORD", "client_secret", "apiKey", "api_key", "x-api-key", "access_token", "PWD", "SIGN_PASS", "AWS_SECRET_ACCESS_KEY", "db.password"} {
		if !IsSecretKey(k) {
			t.Errorf("%q should be a secret key", k)
		}
	}
	for _, k := range []string{"user", "host", "token_count", "tokens", "tokenizer", "password_min_length", "pass", "secretary", "", "message"} {
		if IsSecretKey(k) {
			t.Errorf("%q should not be a secret key", k)
		}
	}
}

func TestMaskURLUserinfo(t *testing.T) {
	for _, tc := range []tcase{
		{"plain", "open mongodb://admin:FAKEPW@host:27017/db now", "open mongodb://[m]@host:27017/db now"},
		{"at sign in the password", "mongodb://admin:FAKEPW@x@host/db", "mongodb://[m]@host/db"},
		{"space in the password", "mongodb://admin:FAKEPW x@host/db", "mongodb://[m]@host/db"},
		{"slash-free tail", "postgres://u:FAKEPW%40x@h:5432", "postgres://[m]@h:5432"},
		{"no login", "https://host.example/docs?a=b@c", "https://host.example/docs?a=b@c"},
		{"host then an e-mail address", "see mongodb://host:27017 or mail ops@corp.example", "see mongodb://host:27017 or mail ops@corp.example"},
		{"two urls", "a://u:FAKEPW@h/x b://v:FAKEPW@i/y", "a://[m]@h/x b://[m]@i/y"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := MaskURLUserinfo(tc.in, m)
			if got != tc.want {
				t.Errorf("got %q, want %q", shown(got), shown(tc.want))
			}
		})
	}
}

// Hostile input: long runs of every shape that could make a pattern backtrack or a look-ahead rescan. Each must finish well under the
// bound, which is far above the measured time so a slow CI machine does not fail it.
func TestMaskHostileInputIsFast(t *testing.T) {
	const size = 1 << 20
	rep := func(unit string) string { return strings.Repeat(unit, size/len(unit)+1)[:size] }
	inputs := map[string]string{
		"password= repeated":          rep("password="),
		"password=; repeated":         rep("password=;"),
		"password=newline repeated":   rep("password=\n"),
		"pwd: repeated":               rep("pwd: "),
		"open quotes":                 `password="` + rep(`"'{`),
		"escaped quotes":              `{"password":"` + rep(`\"`),
		"backslash run":               `{"password":"` + rep(`\`),
		"nested escaped":              `\"password\":\"` + rep(`\\\"`),
		"doubled quotes":              `Password='` + rep(`''`),
		"braces":                      "Password={" + rep("}}"),
		"unclosed xml elements":       rep("<Password>"),
		"unclosed cdata":              rep("<Password><![CDATA["),
		"xml open tags":               rep("<add key=\"password\" "),
		"xml nested value":            rep("<Password><a><b><c>"),
		"xml comments":                rep("<Password><!-- -->"),
		"xml attribute quote":         rep(`<a b="`),
		"long identifier":             rep("a") + "password=x",
		"identifier of password":      rep("password"),
		"identifier with underscore":  rep("DB_PASSWORD_"),
		"url markers":                 rep("://"),
		"url with at signs":           "x://" + rep("a@"),
		"spaces after key":            "password=" + rep(" "),
		"space newline pairs":         "password=" + rep(" \n"),
		"setting-like words":          "password=" + rep("a=b "),
		"angle brackets":              rep("<"),
		"key attrs":                   rep(`<add key="password"`),
		"password and comment":        rep("<Password><!--"),
		"password and closed comment": rep("<Password><!-- -->"),
		"key attr and comment":        rep(`<add key="password"><!--`),
		"comment ends the text":       rep("<Password><!--") + "-->",
		"long name":                   "DB_PASSWORD" + rep("_X"),
		"url slash login":             rep("a://u:x/"),
		"url bare":                    rep("://u:"),
		"url no slash":                rep("a://u:x"),
		"url host port path at":       rep("a://h:1/x/@"),
		"url question":                rep("a://u:x?"),
		"xml unclosed attr":           rep("<a b="),
		"xml unclosed password attr":  rep("<Password b="),
		"xml unclosed key value":      rep(`<add key="password" value=`),
		"xml bare names":              rep("<a "),
		"xml valueless attrs":         rep("<Password x y z "),
		"yaml block headers":          rep("password: |\n"),
		"yaml blocks":                 rep("password: |\n  x\n"),
		"yaml next lines":             rep("client_secret:\n  "),
		"many words in one name":      rep("password_token_secret_"),
	}
	for name, in := range inputs {
		t.Run(name, func(t *testing.T) {
			start := time.Now()
			_ = Mask(in, m)
			_ = MaskURLUserinfo(in, m)
			if d := time.Since(start); d > time.Second {
				t.Errorf("took %v for %d bytes", d, len(in))
			}
		})
	}
}

// secretForms writes the fake secret in the forms a log can hold it, each with the text that must survive around it.
func secretForms(r *rand.Rand) (text, keep string) {
	keys := []string{"password", "Password", "DB_PASSWORD", "pwd", "PWD", "MYSQL_PWD", "passwd", "client_secret", "SECRET", "TOKEN", "API_KEY", "apiKey", "access_token", "Db.Password"}
	key := keys[r.Intn(len(keys))]
	pw := []string{"FAKEPW", "FAKEPW x", "x FAKEPW y", "a;FAKEPW", "FAKEPW}b", `a"FAKEPW`, "a'FAKEPW", `FAKEPW\`, "FAKEPW@x", "p/FAKEPW"}
	val := pw[r.Intn(len(pw))]
	word := !strings.ContainsAny(val, " ;}\"'\\@/")
	if lk := strings.ToLower(key); !word && !strings.HasPrefix(lk, "p") && !strings.Contains(lk, "pwd") && !strings.Contains(lk, "password") {
		val = "FAKEPW" // a token is one word unless it is quoted
	}
	esc := func(v, q string) string {
		v = strings.ReplaceAll(v, `\`, `\\`)
		return strings.ReplaceAll(v, q, `\`+q)
	}
	switch r.Intn(9) {
	case 0:
		return `{"` + key + `":"` + esc(val, `"`) + `","user":"bob"}`, `"user":"bob"`
	case 1:
		return `{'` + key + `': '` + esc(val, `'`) + `', 'user': 'bob'}`, `'user': 'bob'`
	case 2:
		return `{"body":"{\"` + key + `\":\"` + strings.ReplaceAll(esc(strings.ReplaceAll(val, `"`, ``), `"`), `\`, ``) + `\",\"u\":1}"}`, `\"u\":1`
	case 3:
		if word {
			return `<` + key + `>` + val + `</` + key + `>`, `</` + key + `>`
		}
		return `<` + key + `><![CDATA[` + strings.ReplaceAll(val, "]]>", "") + `]]></` + key + `>`, `</` + key + `>`
	case 4:
		return `<add key="` + key + `" value="` + strings.ReplaceAll(strings.ReplaceAll(val, `"`, ""), "<", "") + `" other="1"/>`, `other="1"`
	case 5:
		return `<Login ` + key + `="` + strings.ReplaceAll(strings.ReplaceAll(val, `"`, ""), "<", "") + `" user="bob"/>`, `user="bob"`
	case 6:
		if word {
			return "start " + key + "=" + val + ";Encrypt=true", ";Encrypt=true"
		}
		return "start " + key + `="` + esc(val, `"`) + `";Encrypt=true`, ";Encrypt=true"
	case 7:
		return "start " + key + "=\n  " + strings.ReplaceAll(strings.ReplaceAll(val, ";", ""), "\"", "") + "\nlater line", "later line"
	default:
		if word {
			return key + "=" + val + " DB_HOST=db01", "DB_HOST=db01"
		}
		return key + "={" + strings.ReplaceAll(strings.ReplaceAll(val, "}", "}}"), ";", ",") + "};x=1", ";x=1"
	}
}

// Random secrets in random forms never survive and the text around them stays; random bytes never panic.
func TestMaskRandomForms(t *testing.T) {
	r := rand.New(rand.NewSource(20261122)) //nolint:gosec // fixed seed for repeatable test rounds
	for i := 0; i < 20000; i++ {
		text, keep := secretForms(r)
		got := Mask(text, m)
		if strings.Contains(got, fake) {
			t.Fatalf("case %d: a fake secret was left (form %q)", i, shown(text))
		}
		if !strings.Contains(got, keep) {
			t.Fatalf("case %d: the text after the secret was eaten: %q -> %q", i, shown(text), shown(got))
		}
	}
}

func TestMaskRandomInputNeverPanics(t *testing.T) {
	r := rand.New(rand.NewSource(1122)) //nolint:gosec // fixed seed for repeatable test rounds
	frags := []string{"password", "PWD", "=", ":", "=>", `"`, `'`, `\`, `\"`, "{", "}", "}}", ";", "\n", "\r", "\r\n", " ", "\t", "<", ">", "</", "/>", "<![CDATA[", "]]>", "<!--", "-->",
		"key", "value", "name", "add", "Password", "token", "secret", "api_key", "://", "@", "&", "?", "x", "é", "\x00", "\xff", "DB_", "_pass", "connectionString", "&quot;", "[m]", "FAKEPW"}
	for i := 0; i < 30000; i++ {
		var b strings.Builder
		for n := r.Intn(30); n >= 0; n-- {
			b.WriteString(frags[r.Intn(len(frags))])
		}
		in := b.String()
		out := Mask(in, m)
		_ = MaskURLUserinfo(in, m)
		if len(in) > 0 && !strings.Contains(strings.ToLower(in), "password") && !strings.Contains(strings.ToLower(in), "pwd") && !strings.Contains(strings.ToLower(in), "token") &&
			!strings.Contains(strings.ToLower(in), "secret") && !strings.Contains(strings.ToLower(in), "key") && !strings.Contains(in, "_pass") && out != in && !strings.Contains(strings.ToLower(in), "connectionstring") {
			t.Fatalf("text with no secret word changed: %q -> %q", in, out)
		}
	}
}

func FuzzMask(f *testing.F) {
	for _, s := range []string{`{"password":"a\"b"}`, "DB_PASSWORD=x y", "<Password>x</Password>", `<add key="password" value="x"/>`, "Password={a;b", "mongodb://u:p@h/db"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		out := Mask(s, m)
		_ = MaskURLUserinfo(out, m)
		if len(out) > 4*len(s)+64 {
			t.Fatalf("output grew from %d to %d", len(s), len(out))
		}
	})
}
