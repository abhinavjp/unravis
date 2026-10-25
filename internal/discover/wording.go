package discover

import "sort"

// The sentences a scan row shows. They are fixed text: they name a kind of
// source and what it records, never a value from the host. They are held to the
// same bar as every on-screen sentence: plain words, short sentences (reading
// grade 8.5 or lower) and no word that names a cause. The golden file in
// testdata/wording.golden.json pins them, and the test lints them.
//
// A finding carries the key, not the sentence, so the client can show the same
// copy and the wording can change without touching a scan.

// shows say what a source records. Keys are "discover.<source>.shows".
var shows = map[string]string{
	"discover.iis.shows":            "It lists each request with its time, status and duration.",
	"discover.httperr.shows":        "It records connections and requests that failed before they reached the app.",
	"discover.freb.shows":           "It holds a step-by-step record of each request that IIS was set to trace.",
	"discover.ancm.shows":           "It holds what the app wrote to its console, with times.",
	"discover.log4net.shows":        "It records the warnings and errors the app wrote, with times.",
	"discover.nlog.shows":           "It records the warnings and errors the app wrote, with times.",
	"discover.serilog.shows":        "It records the warnings and errors the app wrote, with times.",
	"discover.mel.shows":            "It records the warnings and errors the app wrote, with times.",
	"discover.appinsights.shows":    "It holds the requests and errors the app sent to Application Insights.",
	"discover.hangfire.shows":       "It lists background jobs with their start and end times.",
	"discover.quartz.shows":         "It lists scheduled jobs and triggers as they fired, with times.",
	"discover.events.shows":         "They record app pool starts, stops and recycles, and service events, with times.",
	"discover.taskscheduler.shows":  "It lists when scheduled tasks started and finished.",
	"discover.redis.shows":          "It lists Redis server messages, such as restarts and saves, with times.",
	"discover.mongo.shows":          "It lists MongoDB server messages, such as slow queries and connections, with times.",
	"discover.sql.shows":            "It lists server messages, such as errors and restarts, with times.",
	"discover.sqlxe.shows":          "It records waits, blocking reports and deadlocks, with times.",
	"discover.sqlagent.shows":       "It lists SQL Server Agent jobs with their start and end times.",
	"discover.nginx.shows":          "It lists each request that nginx served, with its time, status and size.",
	"discover.apache.shows":         "It lists each request that Apache served, with its time, status and size.",
	"discover.live.shows":           "It records CPU and memory use by process while Unravis runs.",
	"discover.perfmon.shows":        "They show CPU, memory, disk and network use on this host, minute by minute.",
	"discover.dotnetcounters.shows": "They show garbage collection, thread pool and exception counts inside the app process, over time.",

	// Why a finding is shown but cannot be imported (see verify.go). They sit in
	// this table so the lint and the golden file cover them.
	"discover.cannotopen.shows":    "Unravis could not open this source, so it is not offered for import.",
	"discover.emptyfile.shows":     "The file is empty, so there is nothing to read yet.",
	"discover.wrongformat.shows":   "The file does not look like the kind of log it was named as, so Unravis cannot read it.",
	"discover.verifytimeout.shows": "Unravis ran out of time before it could check this source. Scan again to check it.",
}

// how give the exact step for a source Unravis does not read directly. Keys are
// "discover.<source>.how".
var how = map[string]string{
	"discover.sqlxe.how":          "Export the system_health events to a CSV file in SQL Server Management Studio. Leave out the columns that hold query text. Then import the file.",
	"discover.sqlagent.how":       "Run the job history query in SQL Server Management Studio. Save the result as a CSV file and import it.",
	"discover.hangfire.how":       "Export the job history from the Hangfire dashboard or its storage as a CSV file. Then import the file.",
	"discover.quartz.how":         "Quartz writes to the app's own log. Find that log file, then import it as a Quartz.NET scheduler log.",
	"discover.appinsights.how":    "Run a query in the Logs view of Application Insights for the same days. Export the result as a CSV file and import it.",
	"discover.perfmon.how":        "Run relog on the .blg file to save a CSV file. Then import the CSV file.",
	"discover.dotnetcounters.how": "Run dotnet-counters collect on the app process and save a CSV or JSON file. Import that file.",
}

// ShowsText returns the sentence for a Shows key.
func ShowsText(key string) (string, bool) {
	s, ok := shows[key]
	return s, ok
}

// HowText returns the sentence for a How key.
func HowText(key string) (string, bool) {
	s, ok := how[key]
	return s, ok
}

// WordingKeys lists every key, sorted, shows then how.
func WordingKeys() []string {
	keys := make([]string, 0, len(shows)+len(how))
	for k := range shows {
		keys = append(keys, k)
	}
	for k := range how {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// WordingText returns the sentence for any key.
func WordingText(key string) (string, bool) {
	if s, ok := shows[key]; ok {
		return s, true
	}
	s, ok := how[key]
	return s, ok
}
