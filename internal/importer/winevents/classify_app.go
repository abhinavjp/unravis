package winevents

import (
	"regexp"
	"strings"
)

// ClassifyApp classifies the W2-g Application, System and ASP.NET event ids by
// provider and id. Message text and properties are never read here. An id that
// is not in the table below returns ok false and stays a generic "win-event".
// INT2 adds ClassifyApp to the New classifier list; it is not registered here.
//
// Every id cites the public page it comes from. The pages are Microsoft Learn
// pages (some are Microsoft Q&A threads, which describe the event but are not
// a reference table). The ids have not been checked against a real export.
//
//   - app-crash: "Application Error" 1000 and "Windows Error Reporting" 1001
//     (https://learn.microsoft.com/en-us/answers/questions/5830558/what-is-application-error-event-id-1000-applicatio);
//     ".NET Runtime" 1026 (named beside 1000 as the faulting-module event in
//     https://learn.microsoft.com/en-us/answers/questions/5672434/how-do-i-analyze-the-event-microsoft-windows-was-i).
//   - app-hang: "Application Hang" 1002 (the application stopped responding;
//     paired with a 1001 fault bucket; same Q&A page as 1000 above).
//   - service-crash: "Service Control Manager" 7031 (service terminated
//     unexpectedly, recovery action taken) and 7034 (service terminated
//     unexpectedly), as in
//     https://learn.microsoft.com/en-ie/answers/questions/5973125/ngcctnrsvc-crashes-3-times-in-ucrtbase-dll-0xc0000
//     (both ids logged for one crashing service).
//   - host-unexpected-shutdown: "EventLog" 6008 and
//     "Microsoft-Windows-Kernel-Power" 41, listed as the markers of an unplanned
//     restart in
//     https://learn.microsoft.com/en-us/troubleshoot/windows-server/performance/troubleshoot-unexpected-reboots-system-event-logs
//   - low-memory: "Microsoft-Windows-Resource-Exhaustion-Detector" 2004
//     (resource exhaustion on virtual memory space), in
//     https://learn.microsoft.com/en-us/troubleshoot/windows-server/performance/troubleshoot-application-service-memory-leaks
//   - aspnet-unhandled: "ASP.NET 4.0.30319.0" 1309 (unhandled exception web
//     event, event code 3005), in
//     https://learn.microsoft.com/en-us/troubleshoot/exchange/client-connectivity/event-1309-code-3005-cannot-access-owa-ecp
//   - ancm-start-failure: "IIS AspNetCore Module V2" 1010 (failed to start the
//     application, with an error code), in "Troubleshoot ASP.NET Core on IIS",
//     https://learn.microsoft.com/en-us/aspnet/core/test/troubleshoot-azure-iis
func ClassifyApp(provider string, id int) (string, map[string]string, bool) {
	kind, ok := appEvents[appKey{strings.ToLower(strings.TrimSpace(provider)), id}]
	if !ok {
		return "", nil, false
	}
	return kind, nil, true
}

type appKey struct {
	provider string // lower case
	id       int
}

var appEvents = map[appKey]string{
	{"application error", 1000}:                              "app-crash",
	{".net runtime", 1026}:                                   "app-crash",
	{"windows error reporting", 1001}:                        "app-crash",
	{"application hang", 1002}:                               "app-hang",
	{"service control manager", 7031}:                        "service-crash",
	{"service control manager", 7034}:                        "service-crash",
	{"eventlog", 6008}:                                       "host-unexpected-shutdown",
	{"microsoft-windows-kernel-power", 41}:                   "host-unexpected-shutdown",
	{"microsoft-windows-resource-exhaustion-detector", 2004}: "low-memory",
	{"asp.net 4.0.30319.0", 1309}:                            "aspnet-unhandled",
	{"iis aspnetcore module v2", 1010}:                       "ancm-start-failure",
}

var faultingAppRE = regexp.MustCompile(`(?i)^(w3wp|dotnet|node|redis-server|memurai|mongod|sqlservr)\.exe$`)

// FaultingAppLabel reduces a FaultingApp cell to a fixed label: the canonical
// lower-case image name for the listed server processes, else "other". A path,
// a command line or any other name never survives.
func FaultingAppLabel(cell string) string {
	s := strings.TrimSpace(cell)
	if faultingAppRE.MatchString(s) {
		return strings.ToLower(s)
	}
	return "other"
}
