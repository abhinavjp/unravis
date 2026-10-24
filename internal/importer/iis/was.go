package iis

import "strings"

// wasProvider is the System log provider for WAS events.
const wasProvider = "Microsoft-Windows-WAS"

// wasRecycleReasons maps WAS event ids to the recycle reason, per the public
// IIS application pool recycling event documentation (2008-era pages, event
// text "has requested a recycle because ..."). 5074 is the periodic time
// interval, NOT CPU.
var wasRecycleReasons = map[int]string{
	5074: "time-interval",
	5075: "request-limit",
	5076: "scheduled-time",
	5077: "virtual-memory",
	5079: "admin-requested",
	5080: "config-change",
	5117: "private-memory",
}

// ClassifyWAS classifies a Microsoft-Windows-WAS event. Recycle ids give kind
// "apppool-recycle" with a "reason" attribute; 5186 (worker process shut down
// after inactivity) gives "apppool-idle-shutdown". Anything else returns
// ok=false. Message text is never read here.
func ClassifyWAS(provider string, id int) (string, map[string]string, bool) {
	if !strings.EqualFold(provider, wasProvider) {
		return "", nil, false
	}
	if reason, ok := wasRecycleReasons[id]; ok {
		return "apppool-recycle", map[string]string{"reason": reason}, true
	}
	if id == 5186 {
		return "apppool-idle-shutdown", nil, true
	}
	return "", nil, false
}

const recycleGapText = "Application pool recycle events are logged only for the Time, Memory and PrivateMemory triggers before IIS 10; other recycle causes may have left no event."

// RecycleGap returns the logEventOnRecycle coverage gap for a runtime such as
// "iis-10" or "iis-8.5". Before IIS 10, or with an unknown runtime, only Time,
// Memory and PrivateMemory recycles are logged by default, so absence of an
// event is not evidence of no recycle. IIS 10 and later return "".
func RecycleGap(runtime string) string {
	s := strings.ToLower(strings.TrimSpace(runtime))
	if !strings.HasPrefix(s, "iis-") {
		return recycleGapText
	}
	maj, _, _ := strings.Cut(s[len("iis-"):], ".")
	n, ok := parseUint(maj, 99)
	if !ok || n < 10 {
		return recycleGapText
	}
	return ""
}
