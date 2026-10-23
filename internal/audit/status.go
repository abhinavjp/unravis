package audit

import (
	"sort"
	"strings"
)

// Status classes. Every audit row falls into exactly one. Only the 2xx class is
// "success": a missing, unrecognised or incomplete status is never a success.
const (
	StatusSuccess        = "success"
	StatusRedirect       = "redirect"
	StatusClientError    = "client_error"
	StatusServerError    = "server_error"
	StatusHangfireFailed = "hangfire_failed"
	StatusPending        = "pending"
	StatusUnknown        = "unknown"
)

// statusCodes maps the upper-cased System.Net.HttpStatusCode names, including
// the obsolete aliases and the names newer .NET versions added, to their number.
// The audit writers store the enum name, so the pipeline and the legacy reader
// both match on these names after upper-casing.
var statusCodes = map[string]int{
	"CONTINUE": 100, "SWITCHINGPROTOCOLS": 101, "PROCESSING": 102, "EARLYHINTS": 103,
	"OK": 200, "CREATED": 201, "ACCEPTED": 202, "NONAUTHORITATIVEINFORMATION": 203, "NOCONTENT": 204,
	"RESETCONTENT": 205, "PARTIALCONTENT": 206, "MULTISTATUS": 207, "ALREADYREPORTED": 208, "IMUSED": 226,
	"MULTIPLECHOICES": 300, "AMBIGUOUS": 300,
	"MOVEDPERMANENTLY": 301, "MOVED": 301,
	"FOUND": 302, "REDIRECT": 302,
	"SEEOTHER": 303, "REDIRECTMETHOD": 303,
	"NOTMODIFIED": 304, "USEPROXY": 305, "UNUSED": 306,
	"TEMPORARYREDIRECT": 307, "REDIRECTKEEPVERB": 307, "PERMANENTREDIRECT": 308,
	"BADREQUEST": 400, "UNAUTHORIZED": 401, "PAYMENTREQUIRED": 402, "FORBIDDEN": 403, "NOTFOUND": 404,
	"METHODNOTALLOWED": 405, "NOTACCEPTABLE": 406, "PROXYAUTHENTICATIONREQUIRED": 407, "REQUESTTIMEOUT": 408,
	"CONFLICT": 409, "GONE": 410, "LENGTHREQUIRED": 411, "PRECONDITIONFAILED": 412,
	"REQUESTENTITYTOOLARGE": 413, "PAYLOADTOOLARGE": 413, "CONTENTTOOLARGE": 413,
	"REQUESTURITOOLONG": 414, "URITOOLONG": 414,
	"UNSUPPORTEDMEDIATYPE": 415,
	"REQUESTEDRANGENOTSATISFIABLE": 416, "RANGENOTSATISFIABLE": 416,
	"EXPECTATIONFAILED": 417, "IMATEAPOT": 418, "MISDIRECTEDREQUEST": 421,
	"UNPROCESSABLEENTITY": 422, "UNPROCESSABLECONTENT": 422,
	"LOCKED": 423, "FAILEDDEPENDENCY": 424, "TOOEARLY": 425, "UPGRADEREQUIRED": 426,
	"PRECONDITIONREQUIRED": 428, "TOOMANYREQUESTS": 429, "REQUESTHEADERFIELDSTOOLARGE": 431, "UNAVAILABLEFORLEGALREASONS": 451,
	"INTERNALSERVERERROR": 500, "NOTIMPLEMENTED": 501, "BADGATEWAY": 502, "SERVICEUNAVAILABLE": 503,
	"GATEWAYTIMEOUT": 504, "HTTPVERSIONNOTSUPPORTED": 505, "VARIANTALSONEGOTIATES": 506,
	"INSUFFICIENTSTORAGE": 507, "LOOPDETECTED": 508, "NOTEXTENDED": 510, "NETWORKAUTHENTICATIONREQUIRED": 511,
}

// StatusCodeNumber returns the HTTP status number for a .NET HttpStatusCode
// name (case-insensitive, aliases included) or for an integer string between 100
// and 599. Like the server's $convert to int, a numeric string may carry one
// leading plus sign and leading zeros ("+404", "0404"). Anything else, including
// empty text, decimals, a minus sign and surrounding whitespace, reports false.
func StatusCodeNumber(name string) (int, bool) {
	if name == "" {
		return 0, false
	}
	if code, ok := statusCodes[strings.ToUpper(name)]; ok {
		return code, true
	}
	digits := strings.TrimPrefix(name, "+")
	if digits == "" {
		return 0, false
	}
	code := 0
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return 0, false
		}
		code = code*10 + int(digits[i]-'0')
		if code > 599 {
			return 0, false
		}
	}
	if code < 100 {
		return 0, false
	}
	return code, true
}

// StatusClass classifies one audit row by its cohort kind and StatusCode text.
// An asynchronous request that answered Accepted has only been queued, so it is
// pending. A hangfire job reports Succeeded or Failed. Otherwise the number
// decides: 2xx success, 3xx redirect, 4xx client_error, 5xx server_error. The
// class depends only on kind and status; state is accepted so call sites can
// pass the bucket state through, and is not used.
func StatusClass(kind, state, status string) string {
	upper := strings.ToUpper(status)
	if kind == "asynchronous" && upper == "ACCEPTED" {
		return StatusPending
	}
	if kind == "hangfire" {
		switch upper {
		case "SUCCEEDED":
			return StatusSuccess
		case "FAILED":
			return StatusHangfireFailed
		}
	}
	code, ok := StatusCodeNumber(status)
	if !ok {
		return StatusUnknown
	}
	switch code / 100 {
	case 2:
		return StatusSuccess
	case 3:
		return StatusRedirect
	case 4:
		return StatusClientError
	case 5:
		return StatusServerError
	}
	return StatusUnknown
}

// ClassOutcome folds a status class into the coarse outcome that keys a cohort:
// success (2xx and 3xx), failure (4xx, 5xx and failed hangfire jobs) or unknown
// (pending, unrecognised and 1xx).
func ClassOutcome(class string) string {
	switch class {
	case StatusSuccess, StatusRedirect:
		return "success"
	case StatusClientError, StatusServerError, StatusHangfireFailed:
		return "failure"
	}
	return "unknown"
}

// StatusNames returns the upper-cased status names of one numeric class
// (success, redirect, client_error or server_error), ordered by status number
// and then name. Other classes have no names.
func StatusNames(class string) []string {
	var names []string
	for name, code := range statusCodes {
		var codeClass string
		switch code / 100 {
		case 2:
			codeClass = StatusSuccess
		case 3:
			codeClass = StatusRedirect
		case 4:
			codeClass = StatusClientError
		case 5:
			codeClass = StatusServerError
		}
		if codeClass == class {
			names = append(names, name)
		}
	}
	sort.Slice(names, func(i, j int) bool {
		if statusCodes[names[i]] != statusCodes[names[j]] {
			return statusCodes[names[i]] < statusCodes[names[j]]
		}
		return names[i] < names[j]
	})
	return names
}

// IsStatusClassOutcome reports whether an outcome filter selects a status class
// (client_error or server_error) rather than a coarse outcome.
func IsStatusClassOutcome(outcome string) bool {
	return outcome == StatusClientError || outcome == StatusServerError
}

// OutcomeFilterMatches applies a query outcome filter to one row that has both
// its coarse outcome and its status class. client_error selects 4xx only;
// server_error selects 5xx and failed hangfire jobs; success and failure
// compare the coarse outcome.
func OutcomeFilterMatches(filter, outcome, class string) bool {
	switch filter {
	case "", "all":
		return true
	case StatusClientError:
		return class == StatusClientError
	case StatusServerError:
		return class == StatusServerError || class == StatusHangfireFailed
	}
	return outcome == filter
}

// BucketOutcomeMatches applies a query outcome filter to a bucket or cohort
// outcome. The sources have already narrowed client_error and server_error
// buckets to the requested class, and both sit inside the failure outcome.
func BucketOutcomeMatches(filter, outcome string) bool {
	switch filter {
	case "", "all":
		return true
	case StatusClientError, StatusServerError:
		return outcome == "failure"
	}
	return outcome == filter
}
