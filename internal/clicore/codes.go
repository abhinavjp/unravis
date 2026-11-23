// Package clicore is the framework-free core of the command line (spec docs/desktop/spec/05-cli.md, BL-384): the error codes and the
// exit map, the output envelope, 7-bit JSON, the output-mode peek, and a table writer that cannot carry terminal control text. It
// imports no command framework and touches no file, registry, pipe or network, so every command (also the offline ones) can use it.
package clicore

// Code is a stable error code of the command line. Callers and scripts match on it; the text next to it is for people.
type Code string

const (
	CodeInternal Code = "INTERNAL"

	CodeInvalidArgument   Code = "INVALID_ARGUMENT"
	CodeUnknownOperation  Code = "UNKNOWN_OPERATION"
	CodeUnsupportedFormat Code = "UNSUPPORTED_FORMAT"
	CodeRequestTooLarge   Code = "REQUEST_TOO_LARGE"
	CodeLegacyOption      Code = "LEGACY_OPTION"
	CodeLiveCaseRefused   Code = "LIVE_CASE_REFUSED"

	CodeAppNotRunning       Code = "APP_NOT_RUNNING"
	CodeEndpointUnavailable Code = "ENDPOINT_UNAVAILABLE"
	CodeEndpointUntrusted   Code = "ENDPOINT_UNTRUSTED"
	CodeStaleLocator        Code = "STALE_LOCATOR"
	CodeOwnerBusy           Code = "OWNER_BUSY"
	CodeUpdateInProgress    Code = "UPDATE_IN_PROGRESS"
	CodeUpstreamUnavailable Code = "UPSTREAM_UNAVAILABLE"
	CodeBudgetExceeded      Code = "BUDGET_EXCEEDED"

	CodePermissionDenied    Code = "PERMISSION_DENIED"
	CodeApprovalRequired    Code = "APPROVAL_REQUIRED"
	CodeCaseLocked          Code = "CASE_LOCKED"
	CodeElevationRefused    Code = "ELEVATION_REFUSED"
	CodeResultExpired       Code = "RESULT_EXPIRED"
	CodeNotFound            Code = "NOT_FOUND"
	CodeResultTooLarge      Code = "RESULT_TOO_LARGE"
	CodeVersionIncompatible Code = "VERSION_INCOMPATIBLE"

	CodeCanceled Code = "CANCELED"
	CodeTimeout  Code = "TIMEOUT"

	CodeIncomplete Code = "INCOMPLETE"
)

// Exit codes (spec 3.4). 0 is success; 8 and above are not used (10 stays reserved, D26).
const (
	ExitOK         = 0
	ExitInternal   = 1
	ExitUsage      = 2
	ExitDependency = 3
	ExitDenied     = 4
	ExitGone       = 5
	ExitStopped    = 6
	ExitIncomplete = 7
)

// codeExit is the one table that says which exit code each error code gives.
var codeExit = map[Code]int{
	CodeInternal: ExitInternal,

	CodeInvalidArgument: ExitUsage, CodeUnknownOperation: ExitUsage, CodeUnsupportedFormat: ExitUsage,
	CodeRequestTooLarge: ExitUsage, CodeLegacyOption: ExitUsage, CodeLiveCaseRefused: ExitUsage,

	CodeAppNotRunning: ExitDependency, CodeEndpointUnavailable: ExitDependency, CodeEndpointUntrusted: ExitDependency,
	CodeStaleLocator: ExitDependency, CodeOwnerBusy: ExitDependency, CodeUpdateInProgress: ExitDependency,
	CodeUpstreamUnavailable: ExitDependency, CodeBudgetExceeded: ExitDependency,

	CodePermissionDenied: ExitDenied, CodeApprovalRequired: ExitDenied, CodeCaseLocked: ExitDenied, CodeElevationRefused: ExitDenied,

	CodeResultExpired: ExitGone, CodeNotFound: ExitGone, CodeResultTooLarge: ExitGone, CodeVersionIncompatible: ExitGone,

	CodeCanceled: ExitStopped, CodeTimeout: ExitStopped,

	CodeIncomplete: ExitIncomplete,
}

// Codes lists every code, in the order of the table in the spec.
func Codes() []Code {
	return []Code{
		CodeInternal,
		CodeInvalidArgument, CodeUnknownOperation, CodeUnsupportedFormat, CodeRequestTooLarge, CodeLegacyOption, CodeLiveCaseRefused,
		CodeAppNotRunning, CodeEndpointUnavailable, CodeEndpointUntrusted, CodeStaleLocator, CodeOwnerBusy, CodeUpdateInProgress,
		CodeUpstreamUnavailable, CodeBudgetExceeded,
		CodePermissionDenied, CodeApprovalRequired, CodeCaseLocked, CodeElevationRefused,
		CodeResultExpired, CodeNotFound, CodeResultTooLarge, CodeVersionIncompatible,
		CodeCanceled, CodeTimeout,
		CodeIncomplete,
	}
}

// ExitCode returns the exit code of an error code. A code that is not in the table is an internal error (exit 1): the command line
// never exits 0 on a failure it does not know.
func ExitCode(c Code) int {
	if n, ok := codeExit[c]; ok {
		return n
	}
	return ExitInternal
}

// Retryable says whether repeating the same request is safe (spec D27). Only these codes can be: APP_NOT_RUNNING, OWNER_BUSY,
// UPDATE_IN_PROGRESS, BUDGET_EXCEEDED and UPSTREAM_UNAVAILABLE. workStarted says the owner may have begun the work of the request (a
// live query ran or may have run): then a repeat could run the query again, so nothing is retryable. A refusal that the owner sent
// before it did any work (busy, updating, over budget) and an app that is not running are retryable with workStarted false, even
// though the request body was sent. An owner that dies mid-call is UPSTREAM_UNAVAILABLE with workStarted true.
func Retryable(c Code, workStarted bool) bool {
	switch c {
	case CodeAppNotRunning, CodeOwnerBusy, CodeUpdateInProgress, CodeBudgetExceeded, CodeUpstreamUnavailable:
		return !workStarted
	}
	return false
}

// FromMCP maps an error code of the MCP server (area 03) to a code of the command line. pending says the calling profile is waiting
// for approval (permission_required then means APPROVAL_REQUIRED, otherwise PERMISSION_DENIED). backend says why the owner could not
// be reached when the MCP code is backend_unavailable ("", "stale" or "untrusted"). A code that is not known maps to INTERNAL.
func FromMCP(code string, pending bool, backend string) Code {
	switch code {
	case "invalid_arguments":
		return CodeInvalidArgument
	case "permission_required":
		if pending {
			return CodeApprovalRequired
		}
		return CodePermissionDenied
	case "result_expired":
		return CodeResultExpired
	case "query_budget_exceeded":
		return CodeBudgetExceeded
	case "source_unavailable":
		return CodeUpstreamUnavailable
	case "cancelled":
		return CodeCanceled
	case "deadline_exceeded":
		return CodeTimeout
	case "result_too_large":
		return CodeResultTooLarge
	case "backend_unavailable":
		switch backend {
		case "stale":
			return CodeStaleLocator
		case "untrusted":
			return CodeEndpointUntrusted
		}
		return CodeAppNotRunning
	}
	return CodeInternal
}

// FromHTTPStatus maps a failing answer of the owner's HTTP routes. 423 (locked) is CASE_LOCKED; 404 is NOT_FOUND; 403 is
// PERMISSION_DENIED; 400 is INVALID_ARGUMENT; 413 is REQUEST_TOO_LARGE; 429 is OWNER_BUSY (a rate limit, retryable); 503 is
// ENDPOINT_UNAVAILABLE (the server uses it for lasting states, so it is not retryable). Anything else that is not a success is INTERNAL.
func FromHTTPStatus(status int) (Code, bool) {
	switch status {
	case 423:
		return CodeCaseLocked, true
	case 404:
		return CodeNotFound, true
	case 403:
		return CodePermissionDenied, true
	case 400:
		return CodeInvalidArgument, true
	case 413:
		return CodeRequestTooLarge, true
	case 429:
		return CodeOwnerBusy, true
	case 503:
		return CodeEndpointUnavailable, true
	}
	if status >= 200 && status < 300 {
		return "", false
	}
	return CodeInternal, true
}

// FromSidecarExit maps an exit code of `unravis.exe --pipe-server` (spec 01): 11 elevation refused, 12 another owner exists, 13 the
// pipe could not start. Any other non-zero code is INTERNAL.
func FromSidecarExit(code int) Code {
	switch code {
	case 11:
		return CodeElevationRefused
	case 12:
		return CodeOwnerBusy
	case 13:
		return CodeUpstreamUnavailable
	}
	return CodeInternal
}
