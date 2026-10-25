// Package collect says which further logs an owner could collect from the
// servers, judged only from the files already imported into a case. It is pure:
// no store, no clock, no network. The caller turns the case's import records
// into SourceCoverage values and gets a short ranked list back.
//
// A suggestion is a gap in what a host already has, never a claim about what
// went wrong. Every sentence is a constant in wording.go: it names a kind of
// file and what that kind of file records, and no suggestion repeats a file
// name, a value or a count from the case.
package collect

import (
	"sort"
	"strings"
)

// MaxItems is the most suggestions Suggest returns.
const MaxItems = 12

// SourceCoverage says that one importer has read at least one file for one
// host. An empty Host means the file was not tied to a host, so it counts for
// every host in the case.
type SourceCoverage struct {
	Importer string
	Host     string
}

// Item is one suggestion.
type Item struct {
	// ID is a stable slug for the kind of log, for example "host-counters".
	ID string `json:"id"`
	// Host is the host the gap is on. Empty means the whole case: the files
	// were not tied to any host.
	Host  string `json:"host"`
	Title string `json:"title"`
	// Why says what the log would show, in plain words.
	Why string `json:"why"`
	// How says how the owner exports it and what kind of file to send.
	How string `json:"how"`
	// Importer is the registered importer name that reads the file. A log kind
	// with several formats lists each name, separated by ", ".
	Importer string `json:"importer"`
	// Rank is the position in the list, 1 for the log that would settle most.
	Rank int `json:"rank"`
}

// Importer names, as registered. Each is checked against the real registry in
// the tests, so a rename cannot leave this package pointing at nothing.
const (
	iisW3C      = "iis-w3c"
	iisHTTPERR  = "iis-httperr"
	iisFREB     = "iis-freb"
	winEvents   = "win-events"
	winCounters = "win-counters"
	winProcsnap = "win-procsnap"
	winLive     = "win-live"
	dotnetCnt  = "dotnet-counters"
	ancm        = "ancm-stdout"
	appInsights = "appinsights"
	hangfire    = "schedule-hangfire"
	sqlAgent    = "schedule-agent"
	sqlErrorlog = "sql-errorlog"
	sqlPDH      = "sql-pdh"
	sqlXE       = "sql-xe"
	redisInfo   = "redis-info"
	redisLat    = "redis-latency"
	redisLog    = "redis-log"
	redisSlow   = "redis-slowlog"
	mongodJSON  = "mongod-json"
	mongoStatus = "mongo-serverstatus"
	mongoProf   = "mongo-profile"
	mongoCurOp  = "mongo-currentop"
	mongostat   = "mongostat-json"
)

// appLogImporters are the application log formats. ancm-stdout is the .NET
// console capture, so it counts as an application log.
var appLogImporters = []string{"log4net-text", "log4net-xml", "serilog-clef", "serilog-text", "nlog-text", "mel-console", "mel-json", ancm}

// Which importers say what kind of server a host is. A host can be several
// kinds at once; a file whose importer is in no list says nothing about it.
var (
	webSignals   = append([]string{iisW3C, iisHTTPERR, iisFREB, dotnetCnt, hangfire, appInsights}, appLogImporters...)
	sqlSignals   = []string{sqlErrorlog, sqlPDH, sqlXE, sqlAgent}
	redisSignals = []string{redisInfo, redisLat, redisLog, redisSlow}
	mongoSignals = []string{mongodJSON, mongoStatus, mongoProf, mongoCurOp, mongostat}
	// machineSignals are files that say the host is a plain Windows machine
	// the counters and process list can be taken from.
	// The live sampler's batches (win-live) read the host's own CPU and memory,
	// so they count as a machine signal and stand in for process snapshots.
	machineSignals = []string{winCounters, winProcsnap, winEvents, winLive}
	// accessLogSignals are nginx and Apache access logs. They are read, so a
	// host holding one is recognised, but they say nothing about a Windows
	// web host: they are in no signal list above, so they never make or
	// remove a Windows-only suggestion.
	accessLogSignals = []string{"nginx-access", "apache-combined"}
	// linuxSignals are the Linux host CPU, memory and process samples. They
	// are known to the package but make no suggestion: the rules above ask for
	// Windows logs, and a Linux host has no list of its own yet.
	linuxSignals = []string{"linux-sadf", "linux-pidstat", "linux-vmstat"}
	// schedulerLogSignals are Quartz.NET scheduler logs. Like the access logs
	// they are read and recognised but say nothing about the kind of host, so
	// they make and remove no suggestion.
	schedulerLogSignals = []string{"schedule-quartz"}
)

// profile is what the case already holds for one host.
type profile struct {
	has map[string]bool
}

func (p profile) any(names ...string) bool {
	for _, n := range names {
		if p.has[n] {
			return true
		}
	}
	return false
}

func (p profile) none(names ...string) bool { return !p.any(names...) }

// known is every importer name the rules read. A file from any other importer
// is ignored: it neither makes a suggestion nor removes one.
var known = func() map[string]bool {
	m := map[string]bool{}
	for _, list := range [][]string{webSignals, sqlSignals, redisSignals, mongoSignals, machineSignals, accessLogSignals, linuxSignals, schedulerLogSignals} {
		for _, n := range list {
			m[n] = true
		}
	}
	return m
}()

// Suggest returns up to MaxItems logs worth collecting, best first.
//
// have lists what the case already holds. hosts lists the hosts to judge; a
// host in have is judged even when hosts omits it. A host with no recognised
// file at all gets nothing, because its kind of server cannot be told. When
// no file names a host, the whole case is judged as one scope with Host "".
//
// The order is fixed: by how much the log would settle (the rule's weight),
// then host name, then ID, so the same case always gives the same list.
func Suggest(have []SourceCoverage, hosts []string) []Item {
	byHost := map[string]map[string]bool{} // host -> importer set
	caseWide := map[string]bool{}          // files with no host
	union := map[string]bool{}
	for _, h := range have {
		if !known[h.Importer] {
			continue
		}
		union[h.Importer] = true
		host := strings.TrimSpace(h.Host)
		if host == "" {
			caseWide[h.Importer] = true
			continue
		}
		if byHost[host] == nil {
			byHost[host] = map[string]bool{}
		}
		byHost[host][h.Importer] = true
	}

	scopes := map[string]bool{}
	for host := range byHost {
		scopes[host] = true
	}
	for _, host := range hosts {
		if host = strings.TrimSpace(host); host != "" {
			scopes[host] = true
		}
	}

	type scored struct {
		item   Item
		weight int
	}
	var found []scored
	judge := func(host string, set map[string]bool) {
		p := profile{has: map[string]bool{}}
		for n := range set {
			p.has[n] = true
		}
		for _, r := range rules {
			if r.applies(p) {
				found = append(found, scored{Item{ID: r.id, Host: host, Title: r.title, Why: r.why, How: r.how, Importer: r.importer}, r.weight})
			}
		}
	}
	if len(scopes) == 0 {
		// Nothing names a host: one whole-case scope over every file.
		if len(union) > 0 {
			judge("", union)
		}
	} else {
		for host := range scopes {
			set := map[string]bool{}
			for n := range caseWide {
				set[n] = true
			}
			for n := range byHost[host] {
				set[n] = true
			}
			if len(set) > 0 {
				judge(host, set)
			}
		}
	}

	sort.SliceStable(found, func(i, j int) bool {
		a, b := found[i], found[j]
		if a.weight != b.weight {
			return a.weight < b.weight
		}
		if a.item.Host != b.item.Host {
			return a.item.Host < b.item.Host
		}
		return a.item.ID < b.item.ID
	})
	if len(found) > MaxItems {
		found = found[:MaxItems]
	}
	out := make([]Item, len(found))
	for i, f := range found {
		out[i] = f.item
		out[i].Rank = i + 1
	}
	return out
}
