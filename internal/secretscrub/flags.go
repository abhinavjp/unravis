package secretscrub

import "strings"

// Command-line forms (SWP-1122-10): the value is the next word, not "=value": `--password hunter2`, `--api-key x`, `/token x`, and
// the short options of the common database tools that take a secret (`sqlcmd -P x`, `mysql -px`, `redis-cli -a x`,
// `mongosh -p x`, `curl -u user:x`). The "--password=x" form is read by maskKV.
//
// One left-to-right pass over the words of each line; the tool names seen on the line (sqlcmd, mysql, ...) decide whether a short
// option is a secret (`-p` is a port for psql, so it is judged only after a tool that uses it for the password). Every byte read is
// charged; over budget, the pass makes no change and the later readers (which share the budget) fail closed.

// tool kinds, as bits of a line's mask.
const (
	toolSQLServer = 1 << iota // sqlcmd, osql, bcp: -P password
	toolMySQL                 // mysql, mysqldump, mysqladmin, mariadb: -ppassword (attached)
	toolRedis                 // redis-cli: -a password
	toolMongo                 // mongo, mongosh, mongodump, mongorestore, mongoexport, mongoimport: -p password
	toolCurl                  // curl: -u user:password
	toolPwdP                  // sshpass, psexec: -p password
	toolLDAP                  // ldapsearch and the other ldap tools: -w password
	toolSchtasks              // schtasks: /RP password
	toolSecure                // ConvertTo-SecureString: -String is the secret
	toolRegistry              // docker, podman, nerdctl, helm, az: "login -p password"
)

var toolNames = map[string]int{
	"sqlcmd": toolSQLServer, "osql": toolSQLServer, "bcp": toolSQLServer,
	"mysql": toolMySQL, "mysqldump": toolMySQL, "mysqladmin": toolMySQL, "mariadb": toolMySQL, "mariadb-dump": toolMySQL,
	"redis-cli": toolRedis,
	"mongo":     toolMongo, "mongosh": toolMongo, "mongodump": toolMongo, "mongorestore": toolMongo, "mongoexport": toolMongo, "mongoimport": toolMongo,
	"curl": toolCurl, "sshpass": toolPwdP, "psexec": toolPwdP, "psexec64": toolPwdP,
	"ldapsearch": toolLDAP, "ldapmodify": toolLDAP, "ldapadd": toolLDAP, "ldapdelete": toolLDAP, "ldapwhoami": toolLDAP, "ldappasswd": toolLDAP,
	"schtasks": toolSchtasks, "docker": toolRegistry, "podman": toolRegistry, "nerdctl": toolRegistry, "helm": toolRegistry, "az": toolRegistry,
}

// extraSecretOptions are option names that hold a secret although the shared name classifier does not take them for one (it wants
// the whole word "pass"): keytool and openssl pass options, cmdkey and redis-cli "--pass".
var extraSecretOptions = map[string]bool{"pass": true, "storepass": true, "keypass": true, "passin": true, "passout": true, "passwd": true,
	"srcstorepass": true, "deststorepass": true, "srckeypass": true, "destkeypass": true, "pw": true}

// toolOf names the tool kind a word is: the file name of a path, without quotes or ".exe", in lower case.
func toolOf(word string) int {
	word = strings.Trim(word, "\"'")
	if i := strings.LastIndexAny(word, "(=`|&;:\"'"); i >= 0 {
		word = word[i+1:] // "$(mysql", "x=mysql", "a|sqlcmd", "\"cmd\":\"sqlcmd"
	}
	if i := strings.LastIndexAny(word, `/\`); i >= 0 {
		word = word[i+1:]
	}
	if len(word) > 16 {
		return 0
	}
	word = strings.TrimSuffix(lowerASCII(word), ".exe")
	return toolNames[word]
}

// optionName says whether the text after the dashes is a plain option name: letters, digits, "_" and "-", nothing else.
func optionName(n string) bool {
	if n == "" || len(n) > 48 {
		return false
	}
	for i := 0; i < len(n); i++ {
		if !isIdent(n[i]) && n[i] != '-' {
			return false
		}
	}
	return true
}

// negated says whether an option name turns a feature off ("--no-password", "--skip-token"): the word after it is not a secret.
func negated(name string) bool {
	n := lowerASCII(name)
	for _, p := range []string{"no-", "no_", "skip-", "disable", "without-", "ask-", "prompt"} {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	// A name that says what kind of thing the word after it is, and that is not the secret itself.
	for _, x := range []string{"store", "lifetime", "expires", "neverexpires", "expiry", "expiration", "timeout", "ttl", "name", "file", "path", "url", "id", "type", "provider", "policy", "length", "count"} {
		if strings.HasSuffix(n, x) {
			return true
		}
	}
	return false
}

// valueEnds says whether the unquoted value ends at k: white space, a quote (also a JSON-escaped one, \"), a closing bracket, or the
// start of a closing tag. The structure around the value stays.
func valueEnds(s string, k int) bool {
	switch s[k] {
	case ' ', '\t', '\r', '\n', '\f', '\v', '"', '\'', ')', ']', '}':
		return true
	case '\\':
		return k+1 < len(s) && s[k+1] == '"'
	case '<':
		return k+1 < len(s) && s[k+1] == '/'
	}
	return false
}

// flagValue reads the value word that starts at p (after the white space that follows an option): a quoted string inside its quotes,
// or the text up to the next white space. ok is false when there is no value on this line, or the word is another option.
func flagValue(s string, p int) (from, to, next int, ok bool) {
	if p >= len(s) {
		return 0, 0, p, false
	}
	switch c := s[p]; c {
	case '\n', '\r':
		return 0, 0, p, false
	case '\\':
		if p+1 < len(s) && s[p+1] == '"' {
			// JSON-escaped quotes (a command line inside a JSON string): \"value\"
			for k := p + 2; k+1 < len(s) && s[k] != '\n' && s[k] != '\r'; k++ {
				if s[k] == '\\' && s[k+1] == '"' {
					return p + 2, k, k + 2, k > p+2
				}
			}
			e := p + 2
			for e < len(s) && s[e] != '\n' && s[e] != '\r' && s[e] != '"' {
				e++
			}
			return p + 2, e, e, e > p+2
		}
	case '"', '\'':
		for k := p + 1; k < len(s) && s[k] != '\n' && s[k] != '\r'; k++ {
			if s[k] == '\\' && c == '"' && k+1 < len(s) {
				k++
				continue
			}
			if s[k] == c {
				return p + 1, k, k + 1, k > p+1
			}
		}
		// Never closed on the line: the rest of the line.
		e := p + 1
		for e < len(s) && s[e] != '\n' && s[e] != '\r' {
			e++
		}
		return p + 1, e, e, e > p+1
	case '-':
		if p+1 < len(s) && (isLower(s[p+1]) || isUpper(s[p+1]) || s[p+1] == '-') {
			return 0, 0, p, false // another option
		}
	}
	e := p
	for e < len(s) && !valueEnds(s, e) {
		e++ // a quote or a closing tag ends the word: the structure around the value stays
	}
	next = e
	if e > p && s[e-1] == '`' {
		e-- // the end of markdown code: `sqlcmd -P x`
	}
	return p, e, next, e > p
}

var httpMethods = map[string]bool{"GET": true, "POST": true, "PUT": true, "DELETE": true, "PATCH": true, "HEAD": true, "OPTIONS": true, "CONNECT": true, "TRACE": true}

// isWordBreak says whether c separates words besides white space: JSON punctuation, so ["--password","x"] reads as two words.
func isWordBreak(c byte) bool { return c == ',' || c == '[' || c == ']' || c == '{' || c == '}' }

// endsLine says whether only white space follows position k on its line.
func endsLine(s string, k int) bool {
	for k < len(s) && (s[k] == ' ' || s[k] == '\t' || s[k] == '\r') {
		k++
	}
	return k >= len(s) || s[k] == '\n'
}

// continued says whether the line that ends at the line break at nl ends with a continuation mark (backslash, caret or backtick).
func continued(s string, nl int) bool {
	k := nl - 1
	if k >= 0 && s[k] == '\r' {
		k--
	}
	return k >= 0 && (s[k] == '\\' || s[k] == '^' || s[k] == '`')
}

// lineStartAfter returns the start of the line after the one that holds k, or len(s).
func lineStartAfter(s string, k int) int {
	if i := strings.IndexByte(s[k:], '\n'); i >= 0 {
		return k + i + 1
	}
	return len(s)
}

// optionValue finds the value of an option whose name ends at p: the next word, also on the next line after a line continuation
// (a backslash, a backtick or a caret at the end of the line), a comma (a JSON array of arguments) and, for a block list item
// ("- --password" then "- x"), the item on the next line.
func optionValue(s string, p int, listItem bool) (from, to, next int, ok bool) {
	lines := 0 // line breaks crossed after a comma
	for n := 0; n < 3; n++ {
		comma := false
		for p < len(s) && (s[p] == ' ' || s[p] == '\t' || s[p] == ',' || (comma && (s[p] == '\n' || s[p] == '\r'))) {
			comma = comma || s[p] == ','
			if s[p] == '\n' {
				lines++
			}
			p++
		}
		if p < len(s) && (s[p] == '\\' || s[p] == '^' || s[p] == '`') && endsLine(s, p+1) {
			p = lineStartAfter(s, p)
			continue
		}
		break
	}
	if listItem && (p >= len(s) || s[p] == '\n' || s[p] == '\r') {
		q := lineStartAfter(s, min(p, len(s)))
		for q < len(s) && (s[q] == ' ' || s[q] == '\t') {
			q++
		}
		if q+1 < len(s) && s[q] == '-' && s[q+1] == ' ' {
			p = q + 2
			for p < len(s) && (s[p] == ' ' || s[p] == '\t') {
				p++
			}
		}
	}
	for p < len(s) && (s[p] == ' ' || s[p] == '\t') {
		p++
	}
	from, to, next, ok = flagValue(s, p)
	if ok && lines > 0 && (lines > 1 || nextLineKey(s[from:to])) {
		// A value on a later line, after a comma, is the next element of an array, not a new line of the text: not after a blank line,
		// and not a word that starts a setting ("password: x", "KEY=x"), which is read on its own.
		return 0, 0, 0, false
	}
	return from, to, next, ok
}

// nextLineKey says whether a word reads as the start of a setting ("password:", "DB_PASSWORD=x").
func nextLineKey(v string) bool {
	return strings.HasSuffix(v, ":") || strings.ContainsAny(v, "=")
}

// commaBreak returns the position of the line break when the value at "from" was found on a later line, after a comma that ends the
// line the option is on (an array of arguments). It is -1 otherwise.
func commaBreak(s string, p, from int) int {
	comma := false
	for k := p; k < from && k < len(s); k++ {
		switch s[k] {
		case ',':
			comma = true
		case '\n', '\r':
			if comma {
				return k
			}
			return -1
		}
	}
	return -1
}

// maskFlags masks the value words of secret command-line options.
func maskFlags(s, mask string, w *work) string {
	if strings.IndexByte(s, '-') < 0 && strings.IndexByte(s, '/') < 0 {
		return s
	}
	var edits []edit
	tools := 0
	i := 0
	prev := ""        // the word before, on this line
	first := true     // no word read yet on this line
	listItem := false // the line is a block list item ("- ...")
	for i < len(s) {
		if w.out() {
			// Over budget: fail closed. The edits found so far stay and the rest of the text goes.
			edits = append(edits, edit{i, len(s)})
			break
		}
		c := s[i]
		if c == '\n' {
			if !continued(s, i) {
				tools = 0 // a line continuation keeps the tool of the line before
			}
			prev, first, listItem = "", true, false
			i++
			continue
		}
		if isSpace(c) || isWordBreak(c) {
			i++
			continue
		}
		// One word.
		a := i
		for i < len(s) && !isSpace(s[i]) && !isWordBreak(s[i]) {
			i++
		}
		w.spend(i - a + 1)
		raw := s[a:i]
		if raw[0] == '<' {
			if k := strings.LastIndexByte(raw, '>'); k >= 0 {
				a += k + 1
				raw = raw[k+1:] // "<Arg>--password" reads as "--password"
			}
		}
		word := strings.TrimLeft(raw, "\"'(`$[\\")
		a += len(raw) - len(word)
		wordT := strings.TrimRight(word, "\"'`);]")
		before := prev
		prev = wordT
		if wordT == "-" && first {
			listItem = true
		}
		first = false
		switch wordT {
		case "|", "||", "&&", "&", ";":
			tools = 0
			continue
		}
		tools |= toolOf(wordT)
		if strings.HasSuffix(word, ";") {
			tools = 0
		}
		if tools&toolRegistry != 0 && wordT == "login" {
			tools |= toolPwdP // docker login -p x; "docker run -p 80:80" is a port and stays
		}
		if strings.EqualFold(wordT, "ConvertTo-SecureString") {
			// ConvertTo-SecureString "hunter2" -AsPlainText: the first argument is the secret itself.
			if from, to, next, ok := optionValue(s, i, false); ok {
				edits = append(edits, edit{from, to})
				w.spend(next - i)
				i = next
			}
			tools |= toolSecure
			continue
		}
		if wordT == "" || (wordT[0] != '-' && wordT[0] != '/') {
			continue
		}
		// An option with its value attached: sqlcmd -Phunter2, mysql -phunter2, redis-cli -ahunter2, curl -ubob:hunter2.
		if len(wordT) > 2 && wordT[0] == '-' && wordT[1] != '-' {
			if o := wordT[1]; (o == 'P' && tools&toolSQLServer != 0) || (o == 'a' && tools&toolRedis != 0) || (o == 'p' && tools&toolMySQL != 0) || (o == 'u' && tools&toolCurl != 0) {
				from, to, next, ok := flagValue(s, a+2)
				if ok {
					if o == 'u' {
						k := strings.IndexByte(s[from:to], ':')
						if k < 0 {
							continue
						}
						from += k + 1
					}
					if to > from {
						edits = append(edits, edit{from, to})
					}
					w.spend(next - a)
					i = max(i, next)
					continue
				}
			}
		}
		if k := strings.IndexAny(wordT, "=:"); k > 0 {
			// "--pass:x", "/pass:x", "--user=u:p": the options that maskKV does not take for secrets.
			if n := lowerASCII(strings.TrimLeft(wordT[:k], "-/")); extraSecretOptions[n] || (tools&toolCurl != 0 && n == "user" && wordT[k] == '=') {
				from, to := a+k+1, a+len(wordT)
				if n == "user" {
					c := strings.IndexByte(s[from:to], ':')
					if c < 0 {
						continue
					}
					from += c + 1
				}
				if to > from {
					edits = append(edits, edit{from, to})
				}
			}
			continue // the rest ("--password=x", "/password:x") is read by maskKV
		}
		if wordT[0] == '/' && (httpMethods[before] || (tools == 0 && !isProgram(before))) {
			continue // "POST /token HTTP/1.1", "See /token for details": a path or prose, not an option
		}
		name := strings.TrimLeft(wordT, "-/")
		if !optionName(name) {
			continue // a path ("/usr/lib/password-store/x"), not an option
		}
		lname := lowerASCII(name)
		secret := false
		switch {
		case len(name) > 1:
			secret = (keyClass(name) != classNone && !negated(name)) || extraSecretOptions[lname] ||
				(lname == "string" && tools&toolSecure != 0) || (lname == "rp" && tools&toolSchtasks != 0)
		case wordT[0] == '-' || tools&toolSQLServer != 0: // a one-letter option: "-P" or sqlcmd's "/P"
			o := name[0]
			secret = (o == 'P' && tools&toolSQLServer != 0) || (o == 'a' && tools&toolRedis != 0) || (o == 'p' && tools&(toolMongo|toolPwdP) != 0) || (o == 'w' && tools&toolLDAP != 0)
		}
		isUser := tools&toolCurl != 0 && wordT[0] == '-' && (lname == "u" || lname == "user" || isCurlCluster(name))
		if !secret && !isUser {
			continue
		}
		if before != "" && prose[lowerASCII(before)] && tools == 0 {
			continue // "Use --password to set it": help text
		}
		from, to, next, ok := optionValue(s, i, listItem && before == "-")
		if !ok || !valueLooksSecret(s[from:to]) {
			continue
		}
		if s[from] == '(' {
			continue // "-AccountPassword (ConvertTo-SecureString ...)": the expression is read on its own
		}
		if isUser {
			k := strings.IndexByte(s[from:to], ':')
			if k < 0 {
				continue
			}
			from += k + 1
		}
		if to > from && !strings.HasPrefix(s[from:], "HTTP/") {
			edits = append(edits, edit{from, to})
		}
		w.spend(next - i)
		if k := commaBreak(s, i, from); k >= 0 {
			// The value came from the next line, after a comma: that line is read again from its start, so an option of its own
			// ("-P" then a value) is not lost.
			next = k
		}
		i = next
	}
	return apply(s, mask, sortEdits(edits))
}

// prose are words that come before an option name in help text and sentences, not in a command line.
var prose = map[string]bool{"use": true, "the": true, "see": true, "with": true, "option": true, "flag": true, "by": true, "using": true, "set": true,
	"add": true, "specify": true, "via": true, "to": true, "and": true, "or": true, "of": true, "for": true, "in": true, "|": true, "location": true}

// isProgram says whether a word names a program of Windows ("app.exe /password x").
func isProgram(w string) bool {
	w = lowerASCII(w)
	return strings.HasSuffix(w, ".exe") || strings.HasSuffix(w, ".bat") || strings.HasSuffix(w, ".cmd") || strings.HasSuffix(w, ".com")
}

// isCurlCluster says whether an option name is a run of curl's one-letter options that ends in "u" ("-sSu").
func isCurlCluster(n string) bool {
	if len(n) < 2 || len(n) > 8 || n[len(n)-1] != 'u' {
		return false
	}
	for i := 0; i < len(n); i++ {
		if !isLower(n[i]) && !isUpper(n[i]) {
			return false
		}
	}
	return true
}

// valueLooksSecret says whether the word after an option can be the value: it holds a letter or a digit, and is not a PowerShell
// literal such as $true.
func valueLooksSecret(v string) bool {
	switch lowerASCII(v) {
	case "$true", "$false", "$null":
		return false
	}
	for i := 0; i < len(v); i++ {
		if isIdent(v[i]) {
			return true
		}
	}
	return len(v) > 0 && v[0] > 127
}
