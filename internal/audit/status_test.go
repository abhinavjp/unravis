package audit

import (
	"strconv"
	"testing"
)

// dotNetStatuses lists every System.Net.HttpStatusCode name, including the
// obsolete aliases, with its number, plus the extra names the audit writers
// have been seen to emit.
var dotNetStatuses = []struct {
	name string
	code int
}{
	{"Continue", 100}, {"SwitchingProtocols", 101}, {"Processing", 102}, {"EarlyHints", 103},
	{"OK", 200}, {"Created", 201}, {"Accepted", 202}, {"NonAuthoritativeInformation", 203}, {"NoContent", 204},
	{"ResetContent", 205}, {"PartialContent", 206}, {"MultiStatus", 207}, {"AlreadyReported", 208}, {"IMUsed", 226},
	{"MultipleChoices", 300}, {"Ambiguous", 300},
	{"MovedPermanently", 301}, {"Moved", 301},
	{"Found", 302}, {"Redirect", 302},
	{"SeeOther", 303}, {"RedirectMethod", 303},
	{"NotModified", 304}, {"UseProxy", 305}, {"Unused", 306},
	{"TemporaryRedirect", 307}, {"RedirectKeepVerb", 307}, {"PermanentRedirect", 308},
	{"BadRequest", 400}, {"Unauthorized", 401}, {"PaymentRequired", 402}, {"Forbidden", 403}, {"NotFound", 404},
	{"MethodNotAllowed", 405}, {"NotAcceptable", 406}, {"ProxyAuthenticationRequired", 407}, {"RequestTimeout", 408},
	{"Conflict", 409}, {"Gone", 410}, {"LengthRequired", 411}, {"PreconditionFailed", 412},
	{"RequestEntityTooLarge", 413}, {"PayloadTooLarge", 413}, {"ContentTooLarge", 413},
	{"RequestUriTooLong", 414}, {"UriTooLong", 414},
	{"UnsupportedMediaType", 415},
	{"RequestedRangeNotSatisfiable", 416}, {"RangeNotSatisfiable", 416},
	{"ExpectationFailed", 417}, {"ImATeapot", 418}, {"MisdirectedRequest", 421},
	{"UnprocessableEntity", 422}, {"UnprocessableContent", 422},
	{"Locked", 423}, {"FailedDependency", 424}, {"TooEarly", 425}, {"UpgradeRequired", 426},
	{"PreconditionRequired", 428}, {"TooManyRequests", 429}, {"RequestHeaderFieldsTooLarge", 431}, {"UnavailableForLegalReasons", 451},
	{"InternalServerError", 500}, {"NotImplemented", 501}, {"BadGateway", 502}, {"ServiceUnavailable", 503},
	{"GatewayTimeout", 504}, {"HttpVersionNotSupported", 505}, {"VariantAlsoNegotiates", 506},
	{"InsufficientStorage", 507}, {"LoopDetected", 508}, {"NotExtended", 510}, {"NetworkAuthenticationRequired", 511},
}

func TestStatusCodeNumberCoversDotNetEnum(t *testing.T) {
	for _, status := range dotNetStatuses {
		got, ok := StatusCodeNumber(status.name)
		if !ok || got != status.code {
			t.Errorf("StatusCodeNumber(%q) = %d, %v; want %d, true", status.name, got, ok, status.code)
		}
		if upper, ok := StatusCodeNumber(toUpperASCII(status.name)); !ok || upper != status.code {
			t.Errorf("StatusCodeNumber(%q) = %d, %v; want %d, true (the pipeline upper-cases names)", toUpperASCII(status.name), upper, ok, status.code)
		}
		numeric, ok := StatusCodeNumber(strconv.Itoa(status.code))
		if !ok || numeric != status.code {
			t.Errorf("StatusCodeNumber(%d) = %d, %v; want the same number back", status.code, numeric, ok)
		}
	}
	for _, name := range []string{"", "Weird", "OKAY", "99", "600", "20x", "-200", "2000", "200.5", " 200", "200 ", "+", "++200", "+-200", "000099", "0600", "99999999999999999999"} {
		if got, ok := StatusCodeNumber(name); ok {
			t.Errorf("StatusCodeNumber(%q) = %d, true; want no match", name, got)
		}
	}
	// The server's $convert to int accepts a leading plus and leading zeros, so
	// the Go classifier must too or the two paths would disagree.
	for name, want := range map[string]int{"+200": 200, "0404": 404, "+404": 404, "00500": 500, "+0503": 503} {
		if got, ok := StatusCodeNumber(name); !ok || got != want {
			t.Errorf("StatusCodeNumber(%q) = %d, %v; want %d, true", name, got, ok, want)
		}
	}
}

func toUpperASCII(s string) string {
	out := []byte(s)
	for i, c := range out {
		if c >= 'a' && c <= 'z' {
			out[i] = c - 32
		}
	}
	return string(out)
}

func TestStatusClassByNumberRange(t *testing.T) {
	for _, status := range dotNetStatuses {
		want := map[int]string{1: "unknown", 2: "success", 3: "redirect", 4: "client_error", 5: "server_error"}[status.code/100]
		if got := StatusClass("synchronous", "valid", status.name); got != want {
			t.Errorf("StatusClass(synchronous, %q) = %q, want %q", status.name, got, want)
		}
		if got := StatusClass("synchronous", "valid", strconv.Itoa(status.code)); got != want {
			t.Errorf("StatusClass(synchronous, %d) = %q, want %q", status.code, got, want)
		}
	}
	// A 4xx must never fold into the 5xx class, and the reverse.
	if StatusClass("synchronous", "valid", "NotFound") == StatusClass("synchronous", "valid", "BadGateway") {
		t.Fatal("4xx and 5xx share a class")
	}
}

func TestStatusClassHangfireStatuses(t *testing.T) {
	cases := []struct{ kind, status, want string }{
		{"hangfire", "Succeeded", "success"},
		{"hangfire", "SUCCEEDED", "success"},
		{"hangfire", "Failed", "hangfire_failed"},
		{"hangfire", "OK", "success"},
		{"hangfire", "Accepted", "success"},
		{"synchronous", "Succeeded", "unknown"},
		{"asynchronous", "Failed", "unknown"},
		{"unknown", "Failed", "unknown"},
	}
	for _, c := range cases {
		if got := StatusClass(c.kind, "valid", c.status); got != c.want {
			t.Errorf("StatusClass(%s, %q) = %q, want %q", c.kind, c.status, got, c.want)
		}
	}
}

func TestStatusClassNeverSuccessForUnknownOrPending(t *testing.T) {
	for _, kind := range []string{"synchronous", "asynchronous", "hangfire", "unknown", ""} {
		for _, status := range []string{"", "Weird", "OKAY", "99", "600", "201.5"} {
			if got := StatusClass(kind, "valid", status); got != "unknown" {
				t.Errorf("StatusClass(%q, %q) = %q, want unknown", kind, status, got)
			}
		}
	}
	for _, status := range []string{"Accepted", "ACCEPTED", "accepted"} {
		if got := StatusClass("asynchronous", "incomplete", status); got != "pending" {
			t.Errorf("async %q = %q, want pending", status, got)
		}
		if got := StatusClass("synchronous", "valid", status); got != "success" {
			t.Errorf("sync %q = %q, want success (an inline 202)", status, got)
		}
	}
	// A pending request is incomplete, so its class never maps to success or failure.
	if got := ClassOutcome(StatusClass("asynchronous", "incomplete", "Accepted")); got != "unknown" {
		t.Errorf("pending outcome = %q, want unknown", got)
	}
}

func TestClassOutcomeKeepsFailureAsFourXXPlusFiveXXPlusHangfire(t *testing.T) {
	want := map[string]string{
		"success": "success", "redirect": "success",
		"client_error": "failure", "server_error": "failure", "hangfire_failed": "failure",
		"pending": "unknown", "unknown": "unknown", "bogus": "unknown",
	}
	for class, outcome := range want {
		if got := ClassOutcome(class); got != outcome {
			t.Errorf("ClassOutcome(%q) = %q, want %q", class, got, outcome)
		}
	}
}

func TestStatusNamesPerClass(t *testing.T) {
	for class, bounds := range map[string][2]int{"success": {200, 299}, "redirect": {300, 399}, "client_error": {400, 499}, "server_error": {500, 599}} {
		names := StatusNames(class)
		if len(names) == 0 {
			t.Fatalf("StatusNames(%q) is empty", class)
		}
		for _, name := range names {
			code, ok := StatusCodeNumber(name)
			if !ok || code < bounds[0] || code > bounds[1] {
				t.Errorf("StatusNames(%q) has %q = %d, outside %d-%d", class, name, code, bounds[0], bounds[1])
			}
			if name != toUpperASCII(name) {
				t.Errorf("StatusNames(%q) has %q, want upper case", class, name)
			}
		}
	}
	if names := StatusNames("unknown"); len(names) != 0 {
		t.Errorf("StatusNames(unknown) = %v, want none", names)
	}
}

func TestOutcomeFilterMatches(t *testing.T) {
	cases := []struct {
		filter, outcome, class string
		want                   bool
	}{
		{"all", "unknown", "unknown", true},
		{"success", "success", "redirect", true},
		{"success", "failure", "client_error", false},
		{"failure", "failure", "client_error", true},
		{"failure", "failure", "hangfire_failed", true},
		{"client_error", "failure", "client_error", true},
		{"client_error", "failure", "server_error", false},
		{"client_error", "failure", "hangfire_failed", false},
		{"server_error", "failure", "server_error", true},
		{"server_error", "failure", "hangfire_failed", true},
		{"server_error", "failure", "client_error", false},
		{"server_error", "unknown", "unknown", false},
	}
	for _, c := range cases {
		if got := OutcomeFilterMatches(c.filter, c.outcome, c.class); got != c.want {
			t.Errorf("OutcomeFilterMatches(%q, %q, %q) = %v, want %v", c.filter, c.outcome, c.class, got, c.want)
		}
	}
	bucketCases := []struct {
		filter, outcome string
		want            bool
	}{
		{"all", "unknown", true}, {"success", "success", true}, {"success", "failure", false},
		{"failure", "failure", true}, {"client_error", "failure", true}, {"server_error", "failure", true},
		{"client_error", "success", false}, {"server_error", "unknown", false},
	}
	for _, c := range bucketCases {
		if got := BucketOutcomeMatches(c.filter, c.outcome); got != c.want {
			t.Errorf("BucketOutcomeMatches(%q, %q) = %v, want %v", c.filter, c.outcome, got, c.want)
		}
	}
}
