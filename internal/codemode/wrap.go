package codemode

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
)

// wrapPrefix and wrapSuffix make the async function the script runs in (spec D4). The prefix sits on the same first line as the
// script, so line numbers match, and `await` and `return` work at the top of the script.
const (
	wrapPrefix = "(async()=>{"
	wrapSuffix = "\n})()"
)

// stripFailure is a script that could not become JavaScript.
type stripFailure struct {
	code string
	msg  string
}

var (
	// A line that starts with import or export is a module statement. Inside the wrapper it is a syntax error, and the plain reason is
	// that scripts are not modules.
	moduleLine = regexp.MustCompile(`(?m)^[ \t]*(?:import|export)\b`)
	// The printed form of the script has no comment and no odd spacing (esbuild prints from its syntax tree), so a dynamic import or
	// import.meta is always one of these two spellings. A string that holds the same text matches as well; that is refused too,
	// because over-refusing is the safe direction and the worker removes import() anyway.
	dynamicImport = regexp.MustCompile(`\bimport\s*[(.]`)
)

// strip wraps the TypeScript or JavaScript source and has esbuild remove the types. It runs no script. The result is plain ES2023
// that the worker evaluates; esbuild never reads a file or the network here (Transform has no resolver).
func strip(source string) (js string, fail *stripFailure) {
	defer func() {
		// esbuild is a parser of hostile text: a panic in it is a failed run, never a crashed app.
		if r := recover(); r != nil {
			js, fail = "", &stripFailure{code: CodeSyntaxError, msg: "The script could not be read."}
		}
	}()
	res := api.Transform(wrapPrefix+source+wrapSuffix, api.TransformOptions{
		Loader:        api.LoaderTS,
		Target:        api.ES2023,
		LegalComments: api.LegalCommentsNone,
		Sourcefile:    "script.ts",
		LogLevel:      api.LogLevelSilent,
	})
	if len(res.Errors) > 0 {
		first := res.Errors[0]
		msg := first.Text
		if first.Location != nil {
			col := first.Location.Column
			if first.Location.Line == 1 {
				col -= len(wrapPrefix)
			}
			if col < 0 {
				col = 0
			}
			msg = fmt.Sprintf("%s (line %d, column %d)", msg, first.Location.Line, col+1)
		}
		if moduleLine.MatchString(source) {
			return "", &stripFailure{code: CodeImportNotAllowed, msg: "A script is not a module: import and export are not allowed. " + msg}
		}
		return "", &stripFailure{code: CodeSyntaxError, msg: msg}
	}
	out := string(res.Code)
	if len(out) > MaxStrippedJS {
		return "", &stripFailure{code: CodeSourceTooLarge, msg: "The script is too big once the types are removed."}
	}
	if dynamicImport.MatchString(out) {
		return "", &stripFailure{code: CodeImportNotAllowed, msg: "import() and import.meta are not allowed."}
	}
	if strings.ContainsRune(out, 0) {
		return "", &stripFailure{code: CodeSyntaxError, msg: "The script holds a NUL character."}
	}
	return out, nil
}
