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
		{"empty value does not eat the next line", "Password=\nnext line stays", "[removed]\nnext line stays"},
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
