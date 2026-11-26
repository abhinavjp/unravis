// Package grammar decides what the remote layer may ask a server, and nothing else does.
// It is a pure port of the read-only gate in tools/remote/RemoteSalt.Lib.ps1 (PR 348) so the Servers screen,
// `unravis ops server` and code mode share one set of rules. testdata/vectors.json is the contract:
// the Go tests read it, and the PowerShell tool's tests can read the same file.
// Every function returns "" when the input is allowed, otherwise one plain sentence saying why not.
package grammar

import (
	"regexp"
	"strings"
)

var (
	reHost        = regexp.MustCompile(`^([A-Za-z0-9]([A-Za-z0-9.\-]{0,251}[A-Za-z0-9])?|[0-9A-Fa-f:]{2,45})\z`)
	reUser        = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._@\-]{0,63}\z`)
	reFingerprint = regexp.MustCompile(`^SHA256:[A-Za-z0-9+/]{43}\z`)
	reMinion      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._\-]{0,127}\z`)
	rePath        = regexp.MustCompile(`^[A-Za-z0-9_ .:\\/\-]{1,260}\z`)
	rePathWild    = regexp.MustCompile(`^[A-Za-z0-9_ .:\\/\-*?]{1,260}\z`)
	reWord        = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.\-]{0,79}\z`)
	rePattern     = regexp.MustCompile(`^[A-Za-z0-9_ .:\\/\-=,@\[\]*+?]{1,200}\z`)
	reCmdChars    = regexp.MustCompile(`^[A-Za-z0-9_ .:\\/\-=,@*?]+\z`)
	reNumber      = regexp.MustCompile(`^[0-9]{1,6}\z`)
	reWinDrive    = regexp.MustCompile(`^[A-Za-z]:`)
	reYamlColon   = regexp.MustCompile(`:(\s|\z)`)
	reYamlHash    = regexp.MustCompile(`(^|\s)#`)
	reYamlLead    = regexp.MustCompile("^[\\[\\]{}!&*|>%@`,?-]\\s*\\z")
)

// Host checks an IP address or DNS name.
func Host(s string) string {
	if !reHost.MatchString(s) {
		return "The address must be an IP address or a DNS name (letters, digits, dots, dashes)."
	}
	return ""
}

// Port checks a TCP port number.
func Port(n int) string {
	if n < 1 || n > 65535 {
		return "The port must be a number from 1 to 65535."
	}
	return ""
}

// User checks a login name.
func User(s string) string {
	if !reUser.MatchString(s) {
		return "The user name has characters that are not allowed."
	}
	return ""
}

// Fingerprint checks the SHA256 host key fingerprint format.
func Fingerprint(s string) string {
	if !reFingerprint.MatchString(s) {
		return "The fingerprint must look like SHA256:xxxxxxxx (43 characters after the colon)."
	}
	return ""
}

// MinionID checks one exact Salt minion id (no wildcards, lists or compound matches).
func MinionID(s string) string {
	if !reMinion.MatchString(s) {
		return "A server id must be one exact id, with no wildcards, lists or compound matches."
	}
	return ""
}

// SafeWord checks one plain name (service, grain, log name).
func SafeWord(s string) string {
	if !reWord.MatchString(s) {
		return "That is not a plain name (letters, digits, . _ - only)."
	}
	return ""
}

// SafePattern checks a search pattern.
func SafePattern(s string) string {
	if !rePattern.MatchString(s) {
		return "The pattern may only use letters, digits, space and . : \\ / - = , @ [ ] * + ?"
	}
	if r := YamlSafe(s); r != "" {
		return r
	}
	return ""
}

// YamlSafe refuses text Salt could read as YAML ("key: value", " #", a lone indicator).
func YamlSafe(s string) string {
	if reYamlColon.MatchString(s) || reYamlHash.MatchString(s) || reYamlLead.MatchString(s) {
		return `That could be read as YAML (": " or " #"). Rephrase it.`
	}
	return ""
}

// PathCharset checks a folder or file path that must be literal (no wildcards).
func PathCharset(s string) string {
	if !rePath.MatchString(s) {
		return "That path has characters that are not allowed (letters, digits, space, . : \\ / _ - only)."
	}
	if strings.Contains(s, "..") {
		return `".." is not allowed.`
	}
	return ""
}

var (
	reWinRoot = regexp.MustCompile(`^[A-Za-z]:[\\/][^\\/:]`)
	reNixRoot = regexp.MustCompile(`^/[^/:]`)
)

// StrictPath is PathCharset plus the rules the remote layer adds: a colon only right after a drive letter (so no alternate data stream),
// and no doubled separators or UNC or device prefixes.
func StrictPath(s string) string {
	if r := PathCharset(s); r != "" {
		return r
	}
	for i := 0; i < len(s); i++ {
		if s[i] == ':' && !(i == 1 && reWinDrive.MatchString(s)) {
			return "A colon is only allowed after the drive letter."
		}
	}
	if strings.Contains(s, `\\`) || strings.Contains(s, "//") || strings.Contains(s, `\/`) || strings.Contains(s, `/\`) {
		return "Use single separators; network paths and double separators are not allowed."
	}
	return ""
}

// AllowedRoot checks a folder that may be put on the allowed list: an absolute path at least one folder deep, never a whole drive or the
// whole file system.
func AllowedRoot(s string) string {
	if r := StrictPath(s); r != "" {
		return r
	}
	if reWinDrive.MatchString(s) {
		if !reWinRoot.MatchString(s) {
			return `Allow a folder such as D:\Redis\logs, not a whole drive.`
		}
		return ""
	}
	if !reNixRoot.MatchString(s) || strings.Contains(s, `\`) {
		return "Allow a full folder path such as /var/log/redis, not the top of the file system."
	}
	return ""
}

// PathAllowed checks that path is one of allowed or lies under one of them. A wildcard is accepted only in the last segment.
func PathAllowed(path string, allowed []string) string {
	if len(allowed) == 0 {
		return "No folder is allowed yet."
	}
	if !rePathWild.MatchString(path) {
		return "That path has characters that are not allowed."
	}
	if strings.Contains(path, "..") {
		return `".." is not allowed.`
	}
	// A backslash separates folders only in Windows (drive letter) paths; on Linux it is a literal character.
	isWin := reWinDrive.MatchString(path)
	if !isWin && strings.Contains(path, `\`) {
		return "A backslash is not a path separator on this kind of server."
	}
	norm := strings.ReplaceAll(path, `\`, "/")
	dirPart := ""
	if i := strings.LastIndex(norm, "/"); i >= 0 {
		dirPart = norm[:i]
	}
	if strings.ContainsAny(dirPart, "*?") {
		return "A wildcard is only allowed in the last part of the path."
	}
	for _, a := range allowed {
		an := strings.TrimRight(strings.ReplaceAll(a, `\`, "/"), "/")
		if an == "" {
			continue
		}
		if isWin {
			if strings.EqualFold(norm, an) || (len(norm) > len(an) && strings.EqualFold(norm[:len(an)+1], an+"/")) {
				return ""
			}
		} else if norm == an || strings.HasPrefix(norm, an+"/") {
			return ""
		}
	}
	return "That path is outside the folders you allowed."
}

type cmdSpec struct {
	flags map[string]bool
	num   map[string]bool
}

func set(v ...string) map[string]bool {
	m := map[string]bool{}
	for _, s := range v {
		m[s] = true
	}
	return m
}

var cmdFlags = map[string]cmdSpec{
	"tail":          {set("-n", "-c"), set("-n", "-c")},
	"head":          {set("-n", "-c"), set("-n", "-c")},
	"cat":           {set(), set()},
	"type":          {set(), set()},
	"get-content":   {set("-tail", "-totalcount", "-raw"), set("-tail", "-totalcount")},
	"ls":            {set("-l", "-a", "-la", "-al", "-r"), set()},
	"dir":           {set("-name", "-recurse", "-file", "-force"), set()},
	"get-childitem": {set("-name", "-recurse", "-file", "-force"), set()},
}

// ReadOnlyCommand gates a free-form command string: file readers and listers over allowed paths, nothing that writes, chains or follows.
func ReadOnlyCommand(cmd string, allowed []string) string {
	if strings.TrimSpace(cmd) == "" {
		return "Empty command."
	}
	if len(cmd) > 400 {
		return "The command is too long."
	}
	if !reCmdChars.MatchString(cmd) {
		return "The command has characters that are not allowed (no quotes, | & ; < > $ ` % ^ ( ) { } or line breaks)."
	}
	if r := YamlSafe(cmd); r != "" {
		return r
	}
	if strings.Contains(cmd, "..") {
		return `".." is not allowed.`
	}
	words := strings.Fields(cmd)
	spec, ok := cmdFlags[strings.ToLower(words[0])]
	if !ok {
		return "That command is not on the read-only list (tail, head, cat, type, Get-Content, ls, dir, Get-ChildItem)."
	}
	paths, numNext := 0, false
	for _, t := range words[1:] {
		if numNext {
			numNext = false
			if reNumber.MatchString(t) {
				continue
			}
			return "The word after the option must be a number."
		}
		if strings.HasPrefix(t, "-") {
			tl := strings.ToLower(t)
			if !spec.flags[tl] {
				return "That option is not allowed for this command."
			}
			numNext = spec.num[tl]
			continue
		}
		if r := PathAllowed(t, allowed); r != "" {
			return r
		}
		paths++
	}
	if numNext {
		return "The option needs a number."
	}
	if paths == 0 {
		return "Give the full path of what to read."
	}
	return ""
}
