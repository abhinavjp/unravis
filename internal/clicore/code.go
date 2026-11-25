package clicore

// The command line side of code mode (BL-371): the limits it checks before it sends a script, and the plain words of every way a run
// can be turned away or can fail. The owner (internal/clisrv) and the command (internal/clicore/cmdtree) both use CodeModeError, so a
// refusal and a failed run read the same everywhere. This file imports nothing: the thin command line must not pull in the broker.

// Limits a script is checked against on this side, so a person gets a usage error at once instead of after the round trip. The owner
// checks them again. internal/codeapi pins each to the broker's own constant (TestCLILimitsMatchTheBroker).
const (
	// MaxCodeSource is the largest script, in bytes (codemode.MaxSource).
	MaxCodeSource = 256 << 10
	// MaxCodeTimeoutMS is the largest --timeout-ms: the time a script itself may run (codemode.MaxTimeout, in milliseconds).
	MaxCodeTimeoutMS = 120000
)

// The code-mode codes that are not the broker's own: many_cases is a no_case refusal of a run that named no case while more than one is open
// (the person can fix it with --case, so it has its own words and exit code).
const CodeModeManyCases = "many_cases"

type codeText struct {
	code    Code
	message string
	action  string
}

// codeModeTexts maps each code of the broker (internal/codemode/errors.go) to the command line code, the words and the next step. The
// words are fixed: nothing the script, the case or the request said is ever copied into them.
var codeModeTexts = map[string]codeText{
	"code_mode_off": {CodeEndpointUnavailable, "Code mode is switched off, so nothing was run.", "Turn it on in the app's code-mode settings, then run this again."},
	"denied": {CodePermissionDenied, "Unravis did not allow this run.",
		"If you gave --allow-identifiers, the operator has to allow identifiers in code runs first. Run again without it."},
	"cli_closed": {CodePermissionDenied, "The command line is not allowed to run scripts right now.",
		"In the app, open the code-mode settings and allow the command line for a while, then run this again."},
	"no_case":          {CodeNotFound, "No case is open to record this run in.", "Open a case in the app, or name an open case with --case ID."},
	CodeModeManyCases:  {CodeInvalidArgument, "More than one case is open, so Unravis cannot tell which one to record the run in.", "Name one with --case ID ('unravis cases list' shows the ids)."},
	"busy":             {CodeOwnerBusy, "Code runs are already in progress, or an import is running on the case.", "Wait for them to finish, then run again."},
	"source_too_large": {CodeRequestTooLarge, "The script is larger than 256 KiB.", "Make the script shorter."},
	"syntax_error":     {CodeInvalidArgument, "The script could not be read.", "Check that it is plain UTF-8 text and valid TypeScript or JavaScript, then run again."},
	"bad_request":      {CodeInvalidArgument, "The request to run a script is not valid.", "Check the options and run again."},
	"import_not_allowed": {CodeInvalidArgument, "The script imports something that is not allowed.",
		"Scripts cannot import modules. Use the tools that 'unravis code types' lists."},
	"script_error": {CodeInvalidArgument, "The script stopped with an error.", "Read the error in the result, fix the script and run it again."},

	"sandbox_unavailable": {CodeEndpointUnavailable, "The safe runner that runs scripts is not available on this computer, so nothing was run.",
		"Update Unravis, or ask the person who installed it. 'unravis code types' still works."},
	"sandbox_degraded_refused": {CodeEndpointUnavailable, "The safe runner could only start with reduced isolation, which is not allowed.",
		"If you accept less isolation, allow it in the app's code-mode settings."},

	"call_budget":      {CodeBudgetExceeded, "The script made more tool calls than one run may make.", "Make fewer calls, or ask each call for more rows."},
	"identifier_limit": {CodeBudgetExceeded, "The script touched too many user identifiers to hide them all, so it was stopped.", "Make the script read less."},
	"ticks":            {CodeBudgetExceeded, "The script used too much processing.", "Simplify the loops or read less data."},
	"oom":              {CodeBudgetExceeded, "The script used too much memory. The app is unaffected.", "Read and keep less data."},
	"stack":            {CodeBudgetExceeded, "The script nested too deeply.", "Use a loop instead of deep recursion."},

	"output_too_large": {CodeResultTooLarge, "The result is bigger than 1 MB.", "Return a summary instead of the whole data."},
	"result_too_large": {CodeResultTooLarge, "A tool answer was bigger than 1 MB.", "Ask the tool for fewer rows."},

	"wall":    {CodeTimeout, "The script ran longer than its time limit.", "Make it faster, or raise --timeout-ms (at most 120000)."},
	"run_cap": {CodeTimeout, "The run reached the longest time one run may take.", "Split the work into smaller runs."},

	"cancelled": {CodeCanceled, "The run was cancelled.", ""},

	"worker_crashed": {CodeInternal, "The safe runner stopped. The app is unaffected.", "Run the script again. If it keeps happening, run 'unravis doctor'."},
	"bad_frame":      {CodeInternal, "The safe runner sent something the app does not accept, so the run was stopped.", "Run the script again."},
	"audit_failed":   {CodeInternal, "The run could not be recorded in the case, so it was stopped.", "Check that the case is open and not locked, then run again."},
	"internal_error": {CodeInternal, "The app hit an internal problem and stopped the run. The app is unaffected.", "Run the script again."},
}

// CodeModeError is the command line error for a code of code mode: a refusal before anything was written (code_mode_off, no_case, ...)
// or the code of a run that was accepted and failed. A code that is not known is INTERNAL, so the command line never exits 0 on a
// failure it does not understand. It is never marked as work-not-started except busy, which is refused before anything runs.
func CodeModeError(code string) *Error {
	t, ok := codeModeTexts[code]
	if !ok {
		return NewErrorAfterWork(CodeInternal, "The run ended in a way this command line does not understand.", "Update Unravis.")
	}
	e := NewErrorAfterWork(t.code, t.message, t.action)
	if code == "busy" {
		e.Retryable = true // refused before anything was written or run
	}
	return e
}
