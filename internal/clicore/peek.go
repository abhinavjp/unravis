package clicore

import (
	"os"
	"strings"
)

// Mode is the output format of a command.
type Mode string

const (
	ModeTable Mode = "table"
	ModeJSON  Mode = "json"
	ModeJSONL Mode = "jsonl"
)

// allowedEnv are the only environment variables the command line reads (spec D25). A flag beats a variable. None of them can relax a
// safety rule. UNRAVIS_RUN_DIR exists only in test builds and is not listed here.
var allowedEnv = []string{"UNRAVIS_OUTPUT", "UNRAVIS_PROFILE", "UNRAVIS_TIMEOUT", "NO_COLOR"}

// AllowedEnv returns the names of the environment variables the command line reads.
func AllowedEnv() []string { return append([]string(nil), allowedEnv...) }

// Getenv reads an environment variable, but only one of the allowed names; any other name gives "". It is the only place of this
// package that calls os.Getenv (a test scans the source for it).
func Getenv(name string) string {
	for _, n := range allowedEnv {
		if n == name {
			return os.Getenv(name)
		}
	}
	return ""
}

// ParseMode reads a mode name. ok is false for a name that is not one of the three.
func ParseMode(s string) (Mode, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "table":
		return ModeTable, true
	case "json":
		return ModeJSON, true
	case "jsonl":
		return ModeJSONL, true
	}
	return ModeTable, false
}

// PeekOutputMode finds the output mode in the arguments before any command parser runs, so that even a usage error ("unravis audit
// nosuch --json") is reported as JSON. The last of "-o X", "-oX", "--output X", "--output=X" and "--json" wins; arguments after "--"
// are not read; the variable UNRAVIS_OUTPUT is used only when no flag names a mode. A value that is not a mode is ignored here (the
// command reports UNSUPPORTED_FORMAT, and it does so as a table). getenv is os.Getenv-like; nil means the real, allowed variables.
func PeekOutputMode(args []string, getenv func(string) string) Mode {
	if getenv == nil {
		getenv = Getenv
	}
	mode, found := ModeTable, false
	set := func(v string) {
		if m, ok := ParseMode(v); ok {
			mode, found = m, true
		}
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			i = len(args)
		case a == "--json":
			mode, found = ModeJSON, true
		case a == "-o" || a == "--output":
			if i+1 < len(args) {
				i++
				set(args[i])
			}
		case strings.HasPrefix(a, "--output="):
			set(strings.TrimPrefix(a, "--output="))
		case strings.HasPrefix(a, "-o") && len(a) > 2 && !strings.HasPrefix(a, "--"):
			set(strings.TrimPrefix(strings.TrimPrefix(a, "-o"), "="))
		}
	}
	if !found {
		set(getenv("UNRAVIS_OUTPUT"))
	}
	return mode
}
