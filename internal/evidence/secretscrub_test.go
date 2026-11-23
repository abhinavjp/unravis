package evidence

import (
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/racedetect"
)

// A password may hold spaces and semicolons. The value of a password key runs to
// the next ";" or the end of the line, a quote or a brace pair; it is not cut at
// the first space (OTA-1).
func TestScrubSecretsPasswordValueShapes(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"report string 1 (connection string, spaces)",
			"Server=db;User ID=sa;Password=correct horse battery;Encrypt=true", "[removed]Encrypt=true"},
		{"report string 2 (braced with a semicolon and a space)",
			"Password = {a;b c}", "[removed]"},
		{"single pair, spaces, text after the semicolon stays",
			"Password=correct horse battery;Encrypt=true", "[removed];Encrypt=true"},
		{"double quoted", `Password="correct horse";Encrypt=true`, "[removed];Encrypt=true"},
		{"single quoted", `pwd='correct horse';Encrypt=true`, "[removed];Encrypt=true"},
		{"braced with }} escape", "Password={a}}b c};Encrypt=true", "[removed];Encrypt=true"},
		{"braced then text", "Password = {a;b c} and then this", "[removed] and then this"},
		{"value at end of line", "login failed, Password=correct horse battery", "login failed, [removed]"},
		{"stops at a line break", "Password=correct horse\nnext line stays", "[removed]\nnext line stays"},
		{"empty value: the next line is the value", "Password=\nhunter2", "[removed]"},
		{"empty value, colon, indented next line", "password:\n  hunter2", "[removed]"},
		{"empty value, lone CR", "Password=\rhunter2", "[removed]"},
		{"empty value, lone CR then spaces", "Password= \r  hunter two", "[removed]"},
		{"blank lines end the search", "Password:\n\n\nThe server crashed", "[removed]\n\n\nThe server crashed"},
		{"blank line with CRLF ends the search", "Password:\r\n\r\nThe server crashed", "[removed]\r\n\r\nThe server crashed"},
		{"empty value, CRLF", "password=\r\n  hunter2 x", "[removed]"},
		{"empty value, text before and a line after stays", "note\nPassword:\n  hunter2\nlater line", "note\n[removed]\nlater line"},
		{"JSON with the value on its own line", "{\n  \"password\":\n    \"hunter2\"\n}", "{\n  \"[removed]\n}"},
		{"empty value then a semicolon", "Password=;Encrypt=true", "[removed];Encrypt=true"},
		{"doubled single quote", "Password='it''s';Encrypt=true", "[removed];Encrypt=true"},
		{"doubled single quote with spaces", "Password='O''Brien secret'", "[removed]"},
		{"doubled double quote", `Password="say ""hi"" now";Encrypt=true`, "[removed];Encrypt=true"},
		{"builder output with all three quote kinds", `Server=db;Password='a"b;c''d e';Encrypt=true`, "[removed]Encrypt=true"},
		{"JDBC string with a spaced password", "jdbc:sqlserver://db01:1433;databaseName=pay;user=sa;password=correct horse;encrypt=true", "[removed]"},
		{"JDBC string, password only", "jdbc:sqlserver://db01;password=correct horse;x=1", "[removed]"},
		{"unclosed brace takes the rest", "Password={a;b c", "[removed]"},
		{"unclosed double quote takes the rest", `Password="a;b c`, "[removed]"},
		{"unclosed single quote takes the rest", "pwd='a;b c", "[removed]"},
		{"query form is cut at the line end, not at &", "GET /x?password=a b&y=1", "GET /x?[removed]"},
		{"pwd with colon", "pwd: correct horse battery", "[removed]"},
		{"unclosed quote runs to end of line", `Password="correct horse`, "[removed]"},
		{"connection string pair, pwd key", "Server=db;Pwd=correct horse;Database=x", "[removed]"},
		{"innocuous line is unchanged", "the password was reset; Encrypt=true on db01", "the password was reset; Encrypt=true on db01"},
		{"other keys keep their value rule", "api_key=abc123 failed", "[removed] failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ScrubSecrets(tc.in); got != tc.want {
				t.Errorf("ScrubSecrets(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// A backslash escapes a quote (JSON, Python repr), a key may carry a prefix
// (DB_PASSWORD), and XML or web.config hold the password as text or an attribute.
func TestScrubSecretsEscapesPrefixesAndXML(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"JSON value with an escaped quote", `{"password":"ab\"cd ef","user":"bob"}`, `{"[removed],"user":"bob"}`},
		{"Python repr value with an escaped quote", `{'password': 'it\'s secret two'}`, `{'[removed]}`},
		{"escaped backslash then the closing quote", `{"pwd":"a\\","user":"bob"}`, `{"[removed],"user":"bob"}`},
		{"token value with an escaped quote", `{"token":"a\"b c","x":1}`, `{"[removed],"x":1}`},
		{"api key value with an escaped quote", `api_key="a\"b c" ok`, `[removed] ok`},
		{"prefixed key, equals", "DB_PASSWORD=hunter two", "DB_[removed]"},
		{"prefixed key, colon", "DB_PASSWD: hunter two", "DB_[removed]"},
		{"prefixed key, quoted value", `export MYSQL_PWD="a b"`, "export MYSQL_[removed]"},
		{"pass after a separator", "db_pass=hunter two", "db[removed]"},
		{"pass after a dot", "mysql.pass = hunter two;x=1", "mysql[removed];x=1"},
		{"XML element", "<password>hunter two</password>", "<password>[removed]</password>"},
		{"XML element with an attribute", `<Password attr="x">hunter two</Password>`, `<Password attr="x">[removed]</Password>`},
		{"XML element with a prefix and CDATA", "<db:UserPassword><![CDATA[a b]]></db:UserPassword>", "<db:UserPassword>[removed]</db:UserPassword>"},
		{"web.config key first", `<add key="Password" value="hunter two"/>`, `<add key="Password" value="[removed]"/>`},
		{"web.config single quotes", `<add key='pwd' value='hunter two'/>`, `<add key='pwd' value="[removed]"/>`},
		{"web.config value first", `<add value="hunter two" key="Password"/>`, `<add value="[removed]" key="Password"/>`},
		{"web.config name attribute", `<add name="DB_PASSWORD" value="hunter two" />`, `<add name="DB_PASSWORD" value="[removed]" />`},
		{"web.config other key stays", `<add key="PasswordPolicy" value="8 chars"/>`, `<add key="PasswordPolicy" value="8 chars"/>`},
		{"web.config two entries", `<add key="Mode" value="a b"/><add key="Password" value="x y"/>`, `<add key="Mode" value="a b"/><add key="Password" value="[removed]"/>`},
		// A value that ends in a backslash still goes whole (the escaped form does not close).
		{"secret ends in a backslash", `secret="abc\"`, "[removed]"},
		{"api key ends in a backslash", `api_key='abc\'`, "[removed]"},
		{"token ends in a backslash, text after stays", `token="abc\" rest`, "[removed] rest"},
		{"api key with a path", `api_key="C:\k\" next=1`, "[removed] next=1"},
		{"CDATA on its own line", "<password>\n  <![CDATA[Secr3tCD]]>\n</password>", "<password>[removed]</password>"},
		{"CDATA with spaces around it", "<Password> <![CDATA[Secr3tSp]]> </Password>", "<Password>[removed]</Password>"},
		{"Hibernate property", `<property name="hibernate.connection.password">Secr3tHib</property>`, `<property name="hibernate.connection.password">[removed]</property>`},
		{"Java properties entry", `<entry key="password">Secr3tEntry</entry>`, `<entry key="password">[removed]</entry>`},
		{"app.config setting with a value child", `<setting name="Password" serializeAs="String"><value>Secr3tSet</value></setting>`, `<setting name="Password" serializeAs="String"><value>[removed]</value></setting>`},
		{"self-closing entry does not eat the text after it", "<add key=\"Password\" value=\"x\"/>\n<add key=\"Other\" value=\"y\"/>", "<add key=\"Password\" value=\"[removed]\"/>\n<add key=\"Other\" value=\"y\"/>"},
		{"quoted db_pass", `{"db_pass":"Secr3t Two","u":"bob"}`, `{"db[removed],"u":"bob"}`},
		{"quoted pass, repr", `{'pass': 'x y'}`, `{[removed]}`},
		{"JSON number password", `{"password": 12345678, "user":"bob"}`, `{"[removed]`},
		{"short client_secret and tokens", `{"client_secret":"x","access_token":"y"}`, `{"client_[removed],"access_[removed]}`},
		{"spaced value after a prefixed key in a query", "a?b.password=Secr3t Two", "a?b.[removed]"},
		{"colon form after a semicolon", "user=sa;Password: Secr3t Two", "user=sa;[removed]"},
		// Words that must stay.
		{"pass words stay", "passes=3 compass: north passwordless=true tokens=5 PasswordExpiry: 90 days", "passes=3 compass: north passwordless=true tokens=5 PasswordExpiry: 90 days"},
		{"policy element and paths stay", `<PasswordPolicy>8</PasswordPolicy> /usr/lib/password-store/foo.go {"pass_count": 3}`, `<PasswordPolicy>8</PasswordPolicy> /usr/lib/password-store/foo.go {"pass_count": 3}`},
		// Lines that must stay as they are.
		{"prose with the word", "the password was wrong", "the password was wrong"},
		{"policy text", "password policy: 8 chars min", "password policy: 8 chars min"},
		{"bypass is not pass", "bypass=1 and boarding-pass: gate 4", "bypass=1 and boarding-pass: gate 4"},
		{"plain XML", "<user>bob</user><note>a b</note>", "<user>bob</user><note>a b</note>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ScrubSecrets(tc.in); got != tc.want {
				t.Errorf("ScrubSecrets(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// Every new pattern stays linear on 256 KB of hostile text (RE2 only).
func TestScrubSecretsStaysLinear(t *testing.T) {
	if testing.Short() || racedetect.Enabled {
		t.Skip("timing test; the race detector slows it 5 to 20 times (see internal/racedetect)")
	}
	for _, unit := range []string{
		`<password a="x">`, `<add key="Password" `, `<add value="a" `, `"password":"\\\"`, "DB_PASSWORD:\n ",
	} {
		in := strings.Repeat(unit, (256<<10)/len(unit))
		start := time.Now()
		_ = ScrubSecrets(in)
		if d := time.Since(start); d > 10*time.Second {
			t.Errorf("ScrubSecrets on 256 KB of %q took %v", unit, d)
		}
	}
}

// SWP-1122-13: a secret whose name and value are two fields of one record, through the real case-report scrubber.
func TestScrubSecretsNameAndValueRecords(t *testing.T) {
	for _, tc := range []struct{ name, in string }{
		{"kubernetes env", "env:\n  - name: DB_PASSWORD\n    value: hunter2zz\n"},
		{"json record", `{"name":"DB_PASSWORD","value":"hunter2zz"}`},
		{"plist", "<key>Password</key>\n<string>hunter2zz</string>"},
	} {
		got := ScrubSecrets(tc.in)
		if strings.Contains(got, "hunter2zz") {
			t.Errorf("%s: the value is still in the text: %q", tc.name, got)
		}
		if !strings.Contains(got, "[removed]") {
			t.Errorf("%s: no mask in %q", tc.name, got)
		}
	}
}
