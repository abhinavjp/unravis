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
)

var toolNames = map[string]int{
	"sqlcmd": toolSQLServer, "osql": toolSQLServer, "bcp": toolSQLServer,
	"mysql": toolMySQL, "mysqldump": toolMySQL, "mysqladmin": toolMySQL, "mariadb": toolMySQL, "mariadb-dump": toolMySQL,
	"redis-cli": toolRedis,
	"mongo":     toolMongo, "mongosh": toolMongo, "mongodump": toolMongo, "mongorestore": toolMongo, "mongoexport": toolMongo, "mongoimport": toolMongo,
	"curl": toolCurl,
}

// toolOf names the tool kind a word is: the file name of a path, without quotes or ".exe", in lower case.
func toolOf(word string) int {
	word = strings.Trim(word, "\"'")
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
	return strings.HasPrefix(n, "no-") || strings.HasPrefix(n, "no_") || strings.HasPrefix(n, "skip-") || strings.HasPrefix(n, "disable") ||
		strings.HasPrefix(n, "without-") || strings.HasPrefix(n, "ask-") || strings.HasPrefix(n, "prompt")
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
	for e < len(s) && !isSpace(s[e]) {
		e++
	}
	return p, e, e, e > p
}

// maskFlags masks the value words of secret command-line options.
func maskFlags(s, mask string, w *work) string {
	if strings.IndexByte(s, '-') < 0 && strings.IndexByte(s, '/') < 0 {
		return s
	}
	var edits []edit
	tools := 0
	i := 0
	for i < len(s) {
		if w.out() {
			break
		}
		c := s[i]
		if c == '\n' || c == '\r' {
			tools = 0
			i++
			continue
		}
		if isSpace(c) {
			i++
			continue
		}
		// One word.
		a := i
		for i < len(s) && !isSpace(s[i]) {
			i++
		}
		w.spend(i - a + 1)
		word := strings.TrimLeft(s[a:i], "\"'(")
		if word == "" {
			continue
		}
		a += len(s[a:i]) - len(word)
		tools |= toolOf(word)
		if word[0] != '-' && word[0] != '/' {
			continue
		}
		if len(word) > 2 && word[0] == '-' && word[1] == 'p' && tools&toolMySQL != 0 {
			// mysql -phunter2: the password is attached to the option.
			from := a + 2
			to := i
			if q := strings.TrimRight(s[from:to], "\"'"); len(q) < to-from {
				to = from + len(q)
			}
			if to > from {
				edits = append(edits, edit{from, to})
			}
			continue
		}
		if strings.ContainsAny(word, "=:") {
			continue // "--password=x" and "/password:x" are read by maskKV
		}
		name := strings.TrimLeft(word, "-/")
		if !optionName(name) {
			continue // a path ("/usr/lib/password-store/x"), not an option
		}
		long := strings.HasPrefix(word, "--") || word[0] == '/'
		switch {
		case long && len(name) > 1 && keyClass(name) != classNone && !negated(name):
			p := i
			for p < len(s) && (s[p] == ' ' || s[p] == '\t') {
				p++
			}
			if from, to, next, ok := flagValue(s, p); ok {
				edits = append(edits, edit{from, to})
				w.spend(next - p)
				i = next
			}
		case !long && len(name) == 1 && name[0] == 'P' && tools&toolSQLServer != 0,
			!long && len(name) == 1 && name[0] == 'a' && tools&toolRedis != 0,
			!long && len(name) == 1 && name[0] == 'p' && tools&toolMongo != 0:
			p := i
			for p < len(s) && (s[p] == ' ' || s[p] == '\t') {
				p++
			}
			if from, to, next, ok := flagValue(s, p); ok {
				edits = append(edits, edit{from, to})
				w.spend(next - p)
				i = next
			}
		case !long && len(name) == 1 && name[0] == 'u' && tools&toolCurl != 0, long && name == "user" && tools&toolCurl != 0:
			// curl -u user:password: the part after the first colon.
			p := i
			for p < len(s) && (s[p] == ' ' || s[p] == '\t') {
				p++
			}
			if from, to, next, ok := flagValue(s, p); ok {
				if k := strings.IndexByte(s[from:to], ':'); k >= 0 && from+k+1 < to {
					edits = append(edits, edit{from + k + 1, to})
				}
				w.spend(next - p)
				i = next
			}
		}
	}
	if w.out() {
		return s // the later readers still run; maskKV fails closed on its own budget
	}
	return apply(s, mask, sortEdits(edits))
}
