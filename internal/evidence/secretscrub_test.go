package evidence

import "testing"

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
