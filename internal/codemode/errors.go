package codemode

import "fmt"

// Error codes of a run (spec 04 section 3.4, stable text). identifier_limit and audit_failed are the two the broker adds: the privacy
// Guard could not keep track of every identifier (BL-365), and the audit record could not be written (BL-369).
const (
	CodeModeOff             = "code_mode_off"
	CodeDenied              = "denied"
	CodeNoCase              = "no_case"
	CodeBusy                = "busy"
	CodeSourceTooLarge      = "source_too_large"
	CodeSyntaxError         = "syntax_error"
	CodeImportNotAllowed    = "import_not_allowed"
	CodeSandboxUnavailable  = "sandbox_unavailable"
	CodeSandboxDegradedNope = "sandbox_degraded_refused"
	CodeCallBudget          = "call_budget"
	CodeResultTooLarge      = "result_too_large"
	CodeOutputTooLarge      = "output_too_large"
	CodeScriptError         = "script_error"
	CodeTicks               = "ticks"
	CodeWall                = "wall"
	CodeRunCap              = "run_cap"
	CodeOOM                 = "oom"
	CodeStack               = "stack"
	CodeWorkerCrashed       = "worker_crashed"
	CodeBadFrame            = "bad_frame"
	CodeCancelled           = "cancelled"
	CodeIdentifierLimit     = "identifier_limit"
	CodeAuditFailed         = "audit_failed"
	CodeBadRequest          = "bad_request"
)

// Refusal is a request the broker refuses before it writes anything to the audit record (spec 3.5 step 1). A run that was accepted
// never returns a Refusal: its failure is in RunResult.
type Refusal struct {
	Code    string
	Message string
}

func (r *Refusal) Error() string { return fmt.Sprintf("code mode: %s: %s", r.Code, r.Message) }

func refuse(code, msg string) *Refusal { return &Refusal{Code: code, Message: msg} }

// The plain words of each failure (spec 3.8). They are fixed text and never repeat the request.
var plain = map[string]string{
	CodeModeOff:             "Code mode is switched off. Turn it on in Settings or run `unravis code enable`.",
	CodeNoCase:              "Open a case first. Code runs are recorded in the case.",
	CodeSourceTooLarge:      "The script is too big (limit 256 KB).",
	CodeSandboxUnavailable:  "The safe runner is missing or cannot start here. Run `unravis code doctor`.",
	CodeSandboxDegradedNope: "The safe runner could only start with reduced isolation, which is not allowed.",
	CodeCallBudget:          "The script made more than 50 tool calls.",
	CodeTicks:               "The script used too much processing.",
	CodeOOM:                 "The script used too much memory. The app is unaffected.",
	CodeOutputTooLarge:      "The result is bigger than 1 MB. Return a summary.",
	CodeResultTooLarge:      "A tool answer was bigger than 1 MB. Ask for fewer rows.",
	CodeWall:                "The script ran longer than its time limit.",
	CodeRunCap:              "The run reached the 280 second limit.",
	CodeStack:               "The script nested too deeply.",
	CodeWorkerCrashed:       "The safe runner stopped. The app is unaffected.",
	CodeBadFrame:            "The safe runner sent something the app does not accept, so the run was stopped.",
	CodeCancelled:           "The run was cancelled.",
	CodeIdentifierLimit:     "The run touched too many user identifiers to hide them all, so it was stopped.",
	CodeAuditFailed:         "The run could not be recorded in the case, so it was stopped.",
	CodeBusy:                "Two code runs are already in progress. Try again when one has finished.",
	CodeDenied:              "This request is not allowed.",
	CodeBadRequest:          "The request is not valid.",
}
