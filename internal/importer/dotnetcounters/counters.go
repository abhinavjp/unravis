package dotnetcounters

import (
	"math"
	"regexp"
	"strconv"
	"strings"
)

// fold maps the lower-case spelling of each word to its canonical spelling.
func fold(v ...string) map[string]string {
	m := make(map[string]string, len(v))
	for _, s := range v {
		m[strings.ToLower(s)] = s
	}
	return m
}

// The vocabularies below copy the export lists in internal/evidence
// (caseexport_enums.go, exportPolicy["dotnet-counters"]). An event carries the
// canonical spelling, so the export shows it and never "[removed]". A value
// that is not in a list is dropped (provider, counter, generation, method) or
// stored as the fixed word "app-exception" (error type).
var (
	providers = fold("System.Runtime", "Microsoft.AspNetCore.Hosting", "Microsoft-AspNetCore-Server-Kestrel",
		"Microsoft.AspNetCore.Server.Kestrel", "System.Net.Http", "System.Net.NameResolution", "System.Net.Sockets")

	// counters are the EventCounter display names without their unit suffix,
	// and the Meter instrument names.
	counters = fold("CPU Usage", "Working Set", "GC Heap Size", "Gen 0 GC Count", "Gen 1 GC Count",
		"Gen 2 GC Count", "% Time in GC", "% Time in GC since last GC", "Allocation Rate", "ThreadPool Thread Count",
		"ThreadPool Queue Length", "ThreadPool Completed Work Item Count", "Monitor Lock Contention Count",
		"Exception Count", "Time paused by GC", "LOH Size", "GC Fragmentation", "Connection Queue Length",
		"Request Queue Length", "Current Connections", "Request Rate", "Current Requests", "Failed Requests",
		"Total Requests",
		"dotnet.process.cpu.time", "dotnet.process.memory.working_set", "dotnet.gc.collections", "dotnet.gc.pause.time",
		"dotnet.gc.last_collection.heap.size", "dotnet.thread_pool.thread.count", "dotnet.thread_pool.queue.length",
		"dotnet.thread_pool.work_item.count", "dotnet.monitor.lock_contentions", "dotnet.exceptions",
		"http.server.request.duration", "http.server.active_requests", "kestrel.active_connections",
		"kestrel.queued_connections", "kestrel.queued_requests", "http.client.request.duration",
		"http.client.open_connections", "http.client.request.time_in_queue")

	generations = fold("gen0", "gen1", "gen2", "loh", "poh")

	methods = fold("GET", "POST", "PUT", "DELETE", "HEAD", "OPTIONS", "PATCH", "TRACE", "CONNECT", "PROPFIND",
		"PROPPATCH", "MKCOL", "COPY", "MOVE", "LOCK", "UNLOCK", "SEARCH", "REPORT")

	exceptionTypes = fold("app-exception",
		"System.Exception", "System.SystemException", "System.TimeoutException", "System.OutOfMemoryException",
		"System.InsufficientMemoryException", "System.StackOverflowException", "System.InsufficientExecutionStackException",
		"System.Threading.ThreadAbortException", "System.Threading.ThreadInterruptedException",
		"System.Threading.SynchronizationLockException", "System.Threading.Tasks.TaskCanceledException",
		"System.OperationCanceledException", "System.AggregateException", "System.InvalidOperationException",
		"System.ArgumentException", "System.ArgumentNullException", "System.ArgumentOutOfRangeException",
		"System.NullReferenceException", "System.ObjectDisposedException", "System.FormatException",
		"System.InvalidCastException", "System.IndexOutOfRangeException", "System.NotSupportedException",
		"System.NotImplementedException", "System.UnauthorizedAccessException", "System.DivideByZeroException",
		"System.OverflowException", "System.Collections.Generic.KeyNotFoundException", "System.IO.IOException",
		"System.IO.FileNotFoundException", "System.IO.DirectoryNotFoundException", "System.IO.EndOfStreamException",
		"System.Net.Sockets.SocketException", "System.Net.WebException", "System.Net.Http.HttpRequestException",
		"System.Net.HttpListenerException", "System.ComponentModel.Win32Exception",
		"System.Data.SqlClient.SqlException", "System.Data.DataException", "System.Data.DBConcurrencyException",
		"System.Transactions.TransactionAbortedException", "System.Text.Json.JsonException")
)

// Unit words the events carry (the export list in internal/evidence).
const (
	unitMs       = "ms"
	unitCount    = "count"
	unitBytes    = "bytes"
	unitPercent  = "percent"
	unitHost     = "percent-host"
	unitPerSec   = "per-sec"
	counterCPU   = "CPU Usage"
	counterPause = "Time paused by GC"
)

var (
	// rateSuffix matches the unit of a Rate counter: "Count / 1 sec", "B / 5 sec".
	rateSuffix = regexp.MustCompile(`^(.*?)\s*/\s*([0-9]+(?:\.[0-9]+)?)\s*sec$`)
	percentile = regexp.MustCompile(`^[0-9]{1,3}(?:\.[0-9]{1,3})?$`)
	statusCode = regexp.MustCompile(`^[1-5][0-9]{2}$`)
	pidValue   = regexp.MustCompile(`^[0-9]{1,10}$`)
)

// tagKeys are the tags an event keeps, mapped to the attribute they become.
// Every other tag (http.route, server.address, server.port, url.*, anything
// else) is dropped when the line is read.
const (
	tagPercentile = "percentile"
	tagGeneration = "gc.heap.generation"
	tagMethod     = "http.request.method"
	tagStatus     = "http.response.status_code"
	tagErrorType  = "error.type"
)

// sample is one counter reading from either format, before normalisation.
type sample struct {
	provider string
	name     string // the counter name cell: "Name (unit)[tags]" in the CSV, "Name (unit)" in the JSON
	tags     string // JSON "tags" field; the CSV carries its tags inside name
	ctype    string
}

// norm is the result of normalising one sample.
type norm struct {
	provider, counter string
	unit              string
	scale             float64 // multiply the value by this, after dividing by interval
	interval          float64 // > 0: a rate over this many seconds
	attrs             map[string]string
}

// Why a sample is dropped without counting as a rejected row.
type dropWhy int

const (
	keep dropWhy = iota
	dropProvider
	dropCounter
	dropUnit
)

// splitNameCell cuts "Name (unit)[k=v;k=v]" into name, unit and the tag text.
// The name part holds no '[', so the first '[' starts the tags; their end is the
// last ']' (a tag value may hold brackets of its own).
func splitNameCell(cell string) (name, unit, tags string) {
	cell = strings.TrimSpace(cell)
	if i := strings.IndexByte(cell, '['); i >= 0 {
		tags = cell[i+1:]
		if j := strings.LastIndexByte(tags, ']'); j >= 0 {
			tags = tags[:j]
		}
		cell = strings.TrimSpace(cell[:i])
	}
	name = cell
	if strings.HasSuffix(cell, ")") {
		if i := strings.LastIndex(cell, " ("); i > 0 {
			name, unit = strings.TrimSpace(cell[:i]), cell[i+2:len(cell)-1]
		}
	}
	return name, strings.TrimSpace(unit), tags
}

// baseUnit maps a unit word to an event unit and the factor to the event unit.
func baseUnit(u string) (unit string, scale float64, ok bool) {
	lu := strings.ToLower(strings.TrimSpace(u))
	switch {
	case lu == "" || lu == "count":
		return unitCount, 1, true
	case strings.HasPrefix(lu, "{") && strings.HasSuffix(lu, "}") && !strings.ContainsAny(lu[1:len(lu)-1], "{}"):
		return unitCount, 1, true // UCUM annotation such as {work_item}
	case lu == "%":
		return unitPercent, 1, true
	case lu == "mb":
		return unitBytes, 1e6, true // decimal megabytes, as the runtime reports them
	case lu == "b" || lu == "by" || lu == "bytes":
		return unitBytes, 1, true
	case lu == "s":
		return unitMs, 1000, true
	case lu == "ms":
		return unitMs, 1, true
	}
	return "", 0, false
}

// normalise resolves a sample against the fixed lists. Provider, counter and
// unit are all allowlisted; anything else drops the sample. Attributes are
// built from the allowlisted tags only, with canonical spellings.
func normalise(s sample) (norm, dropWhy) {
	prov, ok := providers[strings.ToLower(strings.TrimSpace(s.provider))]
	if !ok {
		return norm{}, dropProvider
	}
	name, unit, tags := splitNameCell(s.name)
	if s.tags != "" {
		tags = s.tags
	}
	counter, ok := counters[strings.ToLower(name)]
	if !ok {
		return norm{}, dropCounter
	}
	n := norm{provider: prov, counter: counter, scale: 1}
	rate := false
	if m := rateSuffix.FindStringSubmatch(unit); m != nil {
		iv, err := strconv.ParseFloat(m[2], 64)
		if err != nil || iv <= 0 || iv > 86400 {
			return norm{}, dropUnit
		}
		n.interval, rate, unit = iv, true, m[1]
	}
	if !rate && strings.EqualFold(strings.TrimSpace(s.ctype), "rate") {
		return norm{}, dropUnit // an increment with no stated interval cannot become a rate
	}
	u, scale, ok := baseUnit(unit)
	if !ok {
		return norm{}, dropUnit
	}
	n.unit, n.scale = u, scale
	switch {
	case rate && u == unitMs:
		// A time counter such as "Time paused by GC (ms / 1 sec)": milliseconds
		// per second, kept in ms.
	case rate && (u == unitCount || u == unitBytes):
		n.unit, n.scale = unitPerSec, 1
	case rate:
		return norm{}, dropUnit // a percentage "per second" has no event unit
	case counter == counterCPU && u == unitPercent:
		n.unit = unitHost
	}
	n.attrs = map[string]string{"provider": prov, "counter": counter}
	for _, kv := range splitTags(tags) {
		switch kv[0] {
		case tagPercentile:
			if percentile.MatchString(kv[1]) {
				setOnce(n.attrs, "percentile", kv[1])
			}
		case tagGeneration:
			if g, ok := generations[strings.ToLower(kv[1])]; ok {
				setOnce(n.attrs, "generation", g)
			}
		case tagMethod:
			if m, ok := methods[strings.ToLower(kv[1])]; ok {
				setOnce(n.attrs, "method", m)
			}
		case tagStatus:
			if statusCode.MatchString(kv[1]) {
				setOnce(n.attrs, "status", kv[1])
			}
		case tagErrorType:
			e := "app-exception"
			if t, ok := exceptionTypes[strings.ToLower(kv[1])]; ok {
				e = t
			}
			setOnce(n.attrs, "error-type", e)
		}
	}
	return n, keep
}

func setOnce(m map[string]string, k, v string) {
	if _, ok := m[k]; !ok {
		m[k] = v
	}
}

// splitTags reads "k=v;k=v" (CSV) or "k=v,k=v" (JSON) into pairs. A piece with no
// '=' (the tail of a value that held a separator) is ignored; keys are matched
// exactly, case-insensitively, after trimming.
func splitTags(s string) [][2]string {
	if s == "" {
		return nil
	}
	var out [][2]string
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == ';' || r == ',' }) {
		i := strings.IndexByte(p, '=')
		if i <= 0 {
			continue
		}
		out = append(out, [2]string{strings.ToLower(strings.TrimSpace(p[:i])), strings.TrimSpace(p[i+1:])})
	}
	return out
}

// numberCell reads an invariant-culture number. ok is false for a cell that is
// not a number; blank is true for an empty cell or one that spells a value the
// tool could not measure (NaN, Infinity, null): those samples are dropped
// without counting as a rejected row.
func numberCell(s string) (v float64, ok, blank bool) {
	s = strings.TrimSpace(s)
	switch strings.ToLower(s) {
	case "", "null", "nan", "infinity", "-infinity", "+infinity", "inf", "-inf", "+inf":
		return 0, true, true
	}
	if strings.ContainsAny(s, "xXpP_") {
		return 0, false, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		if ne, isNum := err.(*strconv.NumError); isNum && ne.Err == strconv.ErrRange {
			return 0, true, true // out of range: dropped like Infinity
		}
		return 0, false, false
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, true, true
	}
	return v, true, false
}
