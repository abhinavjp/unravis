package applog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Each case names one secret shape and the value that must be absent from the
// sanitized output. The log file is written to be handed to a developer, so
// every one of these staying out is the point of the helper.
func TestSanitizeStripsSecrets(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory on this machine")
	}
	profilePath := filepath.Join(home, "secret-file.txt")
	sessionToken := "tok-session-abcdef0123456789abcdef0123456789"
	caseKey := "casekey-zyxwvutsrqponmlk9876543210"
	RegisterSecret(sessionToken)
	RegisterSecret(caseKey)

	cases := []struct {
		name   string
		in     string
		absent []string
	}{
		{
			name:   "connection string with password",
			in:     "dial failed: Server=db.corp.local;User Id=svc;Password=Hunter2!;Database=audit",
			absent: []string{"Hunter2!"},
		},
		{
			name:   "mongodb url with credentials",
			in:     "open mongodb://audituser:MongoPass99@mongo.corp.local:27017/audit failed",
			absent: []string{"MongoPass99", "audituser:MongoPass99"},
		},
		{
			name:   "bare user pass at host",
			in:     "login svc-account:Passw0rd!@sql01 rejected",
			absent: []string{"Passw0rd!"},
		},
		{
			name:   "bearer token",
			in:     "upstream said 401 for Authorization: Bearer abcDEF123456ghiJKL7890mnop",
			absent: []string{"abcDEF123456ghiJKL7890mnop"},
		},
		{
			name:   "password equals in text",
			in:     "settings were password=Sup3rSecret and pwd=An0therOne here",
			absent: []string{"Sup3rSecret", "An0therOne"},
		},
		{
			name:   "json secret field",
			in:     `body {"username":"svc","password":"JsonPass123"} refused`,
			absent: []string{"JsonPass123"},
		},
		{
			name:   "long base64 secret",
			in:     "key material QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVo= leaked",
			absent: []string{"QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWVo"},
		},
		{
			name:   "profile path",
			in:     "could not read " + profilePath,
			absent: []string{home},
		},
		{
			name:   "session token registered",
			in:     "request carried token " + sessionToken + " onwards",
			absent: []string{sessionToken},
		},
		{
			name:   "case key registered",
			in:     "unlock tried with " + caseKey,
			absent: []string{caseKey},
		},
		{
			name:   "token equals",
			in:     "GET /x?token=abc12345secret HTTP/1.1",
			absent: []string{"abc12345secret"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := Sanitize(tc.in)
			for _, gone := range tc.absent {
				if strings.Contains(out, gone) {
					t.Errorf("Sanitize(%q) = %q, still contains %q", tc.in, out, gone)
				}
			}
		})
	}
}

func TestSanitizeKeepsOrdinaryText(t *testing.T) {
	in := "source west read 120 rows in 45 ms; window 2026-10-22T12:00:00Z to 2026-10-22T13:00:00Z"
	if out := Sanitize(in); out != in {
		t.Errorf("ordinary text changed:\n in: %q\nout: %q", in, out)
	}
}

func TestSanitizeProfilePathBecomesPlaceholder(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory on this machine")
	}
	out := Sanitize("could not read " + filepath.Join(home, "x.txt"))
	if !strings.Contains(out, "%USERPROFILE%") {
		t.Errorf("profile path not replaced: %q", out)
	}
}

// A password may hold spaces and semicolons: an unquoted value runs to the next
// ";" or the end of the line (ADO syntax), a quoted or braced value to its
// closing mark. In a URL query string it still ends at "&" (OTA-2).
func TestSanitizePasswordValueShapes(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"report string (spaces, text after stays)",
			"Server=db;User ID=sa;Password=correct horse battery;Encrypt=true",
			"Server=db;User ID=sa;Password=[redacted];Encrypt=true"},
		{"braced with a semicolon and a space", "Password = {a;b c}", "Password=[redacted]"},
		{"braced with }} escape", "pwd={a}}b c};Encrypt=true", "pwd=[redacted];Encrypt=true"},
		{"double quoted", `Password="correct horse";Encrypt=true`, `Password=[redacted];Encrypt=true`},
		{"single quoted", `pwd='correct horse';Encrypt=true`, `pwd=[redacted];Encrypt=true`},
		{"value at end of line", "login failed Password=correct horse battery", "login failed Password=[redacted]"},
		{"stops at a line break", "Password=correct horse\nnext line stays", "Password=[redacted]\nnext line stays"},
		{"empty value: the next line is the value", "password=\nhunter2", "password=[redacted]"},
		{"empty value, lone CR", "Password=\rhunter2", "Password=[redacted]"},
		{"blank lines end the search", "Password=\n\n\nThe server crashed", "Password=[redacted]\n\n\nThe server crashed"},
		{"empty value, CRLF", "Password=\r\n  hunter2 x\nlater", "Password=[redacted]\nlater"},
		{"doubled single quote", "Password='it''s';Encrypt=true", "Password=[redacted];Encrypt=true"},
		{"doubled single quote with spaces", "Password='O''Brien secret'", "Password=[redacted]"},
		{"doubled double quote", `Password="say ""hi"" now";Encrypt=true`, "Password=[redacted];Encrypt=true"},
		{"unclosed brace takes the rest", "Password={a;b c", "Password=[redacted]"},
		{"unclosed double quote takes the rest", `Password="a;b c`, "Password=[redacted]"},
		{"url query doubled quote", "GET /x?pwd='a''b'&y=1", "GET /x?pwd=[redacted]&y=1"},
		{"url query form stops at &", "GET /x?password=hunter2&y=1 HTTP/1.1", "GET /x?password=[redacted]&y=1 HTTP/1.1"},
		{"url query form, later parameter", "GET /x?a=1&Pwd=hunter2&y=1", "GET /x?a=1&Pwd=[redacted]&y=1"},
		{"url query form with spaces ends at the space", "GET /x?password=hunter2 HTTP/1.1", "GET /x?password=[redacted] HTTP/1.1"},
		{"token keeps the old rule", "token=abc and more", "token=[redacted] and more"},
		{"innocuous line is unchanged", "the password was reset; Encrypt=true on db01", "the password was reset; Encrypt=true on db01"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Sanitize(tc.in); got != tc.want {
				t.Errorf("Sanitize(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
