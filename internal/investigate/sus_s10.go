package investigate

import "fmt"

// appPoolReasons are HTTPERR reasons that show the application pool down or
// its worker gone.
var appPoolReasons = []string{"AppOffline", "AppShutdown", "AppPoolTimer", "Connection_Abandoned_By_AppPool"}

// sigAppPoolFail reads HTTPERR rows that show the app pool unavailable (S10).
// One row in the incident is enough: a healthy pool writes none.
func sigAppPoolFail(in Input, f Facts, th Thresholds, fl map[string]Signal) Signal {
	if !in.HTTPErrPresent {
		return missing("httperr", "No HTTPERR log was imported for these windows.")
	}
	if s, bad := windowMissing(in, false); bad {
		return s
	}
	b := CountBurst(httpErrTimes(in, appPoolReasons...), in.Incident, in.Reference)
	if b.Inc >= 1 {
		return sig(Yes, fmt.Sprintf("%d HTTPERR rows show the application pool unavailable in the incident.", b.Inc))
	}
	return sig(No, "No HTTPERR row shows the application pool unavailable in the incident.")
}
