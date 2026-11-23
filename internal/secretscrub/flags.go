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
	if i := strings.LastIndexAny(word, "(=`"); i >= 0 {
		word = word[i+1:] // "$(mysql", "x=mysql"
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
	next = e
	if e > p && s[e-1] == '`' {
		e-- // the end of markdown code: `sqlcmd -P x`
	}
	return p, e, next, e > p
}

var httpMethods = map[string]bool{"GET": true, "POST": true, "PUT": true, "DELETE": true, "PATCH": true, "HEAD": true, "OPTIONS": true, "CONNECT": true, "TRACE": true}

// endsLine says whether only white space follows position k on its line.
func endsLine(s string, k int) bool {
	for k < len(s) && (s[k] == ' ' || s[k] == '\t' || s[k] == '\r') {
		k++
	}
	return k >= len(s) || s[k] == '\n'
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
	for n := 0; n < 2; n++ {
		for p < len(s) && (s[p] == ' ' || s[p] == '\t' || s[p] == ',') {
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
	return flagValue(s, p)
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
			if k := i - 1; !(k >= 0 && (s[k] == '\\' || s[k] == '^' || s[k] == '`') || k >= 1 && s[k] == '\r' && (s[k-1] == '\\' || s[k-1] == '^' || s[k-1] == '`')) {
				tools = 0 // a line continuation keeps the tool of the line before
			}
			prev, first, listItem = "", true, false
			i++
			continue
		}
		if isSpace(c) || c == ',' {
			i++
			continue
		}
		// One word.
		a := i
		for i < len(s) && !isSpace(s[i]) && s[i] != ',' {
			i++
		}
		w.spend(i - a + 1)
		raw := s[a:i]
		word := strings.TrimLeft(raw, "\"'(`$[")
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
		if wordT == "" || (wordT[0] != '-' && wordT[0] != '/') {
			continue
		}
		if wordT[0] == '/' && httpMethods[before] {
			continue // "POST /token HTTP/1.1": a request path, not an option
		}
		// An option with its value attached: sqlcmd -Phunter2, mysql -phunter2, redis-cli -ahunter2, curl -ubob:hunter2.
		if len(word) > 2 && word[0] == '-' && word[1] != '-' {
			if o := word[1]; (o == 'P' && tools&toolSQLServer != 0) || (o == 'a' && tools&toolRedis != 0) || (o == 'p' && tools&toolMySQL != 0) || (o == 'u' && tools&toolCurl != 0) {
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
		if strings.ContainsAny(wordT, "=:") {
			continue // "--password=x" and "/password:x" are read by maskKV
		}
		name := strings.TrimLeft(wordT, "-/")
		if !optionName(name) {
			continue // a path ("/usr/lib/password-store/x"), not an option
		}
		secret := false
		switch {
		case len(name) > 1:
			secret = keyClass(name) != classNone && !negated(name)
		case wordT[0] == '-' || tools&toolSQLServer != 0: // a one-letter option: "-P" or sqlcmd's "/P"
			o := name[0]
			secret = (o == 'P' && tools&toolSQLServer != 0) || (o == 'a' && tools&toolRedis != 0) || (o == 'p' && tools&toolMongo != 0)
		}
		isUser := tools&toolCurl != 0 && (name == "u" || name == "user") && wordT[0] == '-'
		if !secret && !isUser {
			continue
		}
		from, to, next, ok := optionValue(s, i, listItem && before == "-")
		if !ok {
			continue
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
		i = next
	}
	return apply(s, mask, sortEdits(edits))
}
