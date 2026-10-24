package iis

import "strings"

// wasProvider is the System log provider for WAS events.
const wasProvider = "Microsoft-Windows-WAS"

// wasRecycleReasons maps WAS event ids to the recycle reason, per the public
// IIS application pool recycling event documentation (2008-era pages, event
// text "has requested a recycle because ..."). 5074 is the periodic time
// interval, NOT CPU. 5078 is the ISAPI unhealthy trigger.
var wasRecycleReasons = map[int]string{
	5074: "time-interval",
	5075: "request-limit",
	5076: "scheduled-time",
	5077: "virtual-memory",
	5078: "isapi-unhealthy",
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

// recycleGapText applies from IIS 8.5, where the logEventOnRecycle default
// covers more triggers. It is deliberately weak: whether a given recycle
// trigger left an event depends on the pool's logEventOnRecycle setting.
const recycleGapText = "Application pool recycle events are logged only for the triggers enabled in logEventOnRecycle; other recycle triggers may have left no event."

// recycleGapTextLegacy applies before IIS 8.5 and for an unknown runtime.
const recycleGapTextLegacy = "Application pool recycle events are logged by default only for the Time, Memory and PrivateMemory triggers before IIS 8.5, and logEventOnRecycle may have been changed; other recycle triggers may have left no event."

// RecycleGap returns the logEventOnRecycle coverage gap for a runtime such as
// "iis-10" or "iis-8.5". It never returns "": absence of a recycle event is
// not evidence of no recycle on any version, because the logged triggers
// depend on the pool's logEventOnRecycle setting. IIS 8.5 and later get the
// weak wording; earlier versions and unknown runtimes get the legacy wording.
func RecycleGap(runtime string) string {
	s := strings.ToLower(strings.TrimSpace(runtime))
	if !strings.HasPrefix(s, "iis-") {
		return recycleGapTextLegacy
	}
	majS, minS, _ := strings.Cut(s[len("iis-"):], ".")
	maj, ok := parseUint(majS, 99)
	if !ok {
		return recycleGapTextLegacy
	}
	min := uint64(0)
	if minS != "" {
		m, ok := parseUint(minS, 99)
		if !ok {
			return recycleGapTextLegacy
		}
		min = m
	}
	if maj > 8 || (maj == 8 && min >= 5) {
		return recycleGapText
	}
	return recycleGapTextLegacy
}
