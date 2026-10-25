package discover

import (
	"regexp"
	"strings"
)

// A config can name any file as a log sink. These rules confine what the applog
// detector will report from such a name (security review F1): the file name must
// carry a log-style extension and nothing that looks like a key, certificate or
// credential, and a sink outside the site folders must also have a head that a
// known importer recognises. A refused file becomes an unsafe-path gap.

// logExtRe matches a log extension (.log .txt .json .clef .xml .out .csv),
// optionally followed by one rolling counter or date such as ".1", ".20010101",
// "-2001" or "_2001-10-25".
var logExtRe = regexp.MustCompile(`\.(log|txt|json|clef|xml|out|csv)([._-][0-9][0-9_.-]*)?$`)

// deniedExts are never log files, whatever else the name says.
var deniedExts = map[string]bool{
	".pem": true, ".key": true, ".pfx": true, ".p12": true, ".pkcs12": true, ".cer": true, ".crt": true,
	".der": true, ".jks": true, ".kdbx": true, ".ppk": true, ".p7b": true, ".keytab": true, ".gpg": true,
	".asc": true, ".ovpn": true, ".rdp": true, ".config": true, ".env": true,
}

// deniedParts are substrings that mark a key, credential or settings file.
var deniedParts = []string{
	"id_rsa", "id_dsa", "id_ecdsa", "id_ed25519", "credential", "passwd", "password", "shadow", "secret",
	"token", "private", "keystore", "apikey", "api-key", "api_key", "privkey", "appsettings", "web.config",
	"applicationhost", "connectionstring", "htpasswd", "npmrc", "netrc", "pgpass", "ntds.dit",
}

// deniedWords are whole words (split on anything that is not a letter or digit)
// that mark a key or credential file.
var deniedWords = map[string]bool{
	"key": true, "keys": true, "pem": true, "pfx": true, "cert": true, "certs": true, "cred": true,
	"creds": true, "kdbx": true, "pwd": true, "sam": true,
}

// sensitiveLogName reports whether a file name looks like a key, certificate or
// credential file.
func sensitiveLogName(name string) bool {
	lower := strings.ToLower(name)
	for _, p := range deniedParts {
		if strings.Contains(lower, p) {
			return true
		}
	}
	if i := strings.LastIndexByte(lower, '.'); i >= 0 && deniedExts[lower[i:]] {
		return true
	}
	words := strings.FieldsFunc(lower, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
	for _, w := range words {
		if deniedWords[w] {
			return true
		}
	}
	return false
}

// logExtOK reports whether the file name ends in a log extension, ignoring one
// rolling counter or date after it (app.log.20010101).
func logExtOK(name string) bool {
	return logExtRe.MatchString(strings.ToLower(name))
}

// underRoots reports whether dir is one of roots or lies below one.
func underRoots(dir string, roots []string) bool {
	d := CanonKey(strings.TrimRight(dir, `\`)) + `\`
	for _, r := range roots {
		if strings.HasPrefix(d, CanonKey(strings.TrimRight(r, `\`))+`\`) {
			return true
		}
	}
	return false
}
