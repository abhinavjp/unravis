package rules

import (
	"sort"
	"strconv"
	"strings"
	"unicode"

	"api-audit-explorer/internal/resource"
)

// Process is one process ranked on a host for the resource panel. Sel maps a
// process measure id (proc.cpu, proc.ws, proc.private, proc.handles,
// proc.threads) to a selector the series answer accepts, which reads the raw
// events of that measure for this process. A measure whose events cannot be
// named exactly (a legacy PDH Process instance whose pid came from the ID
// Process counter) is left out.
type Process struct {
	Name string
	Sel  map[string]string
}

// processMetrics are the measures a Process may carry, in display order.
var processMetrics = []string{resource.ProcCPU, resource.ProcWS, resource.ProcPrivate, resource.ProcHandles, resource.ProcThreads}

// selectorValueMax is the longest selector value written, the bound the
// resource panel also keeps.
const selectorValueMax = 64

// processes ranks the processes of each host, busiest first: by mean process
// CPU in the window, then peak private bytes, then peak working set, then
// name. At most TopN per host; a process with no selector is left out.
func (e *eval) processes() map[string][]Process {
	type rank struct {
		key           string
		cpu, priv, ws float64
		sel           map[string]string
		selSource     map[string]float64 // coverage of the series behind each selector
	}
	byHost := map[string]map[string]*rank{}
	get := func(host, key string) *rank {
		m := byHost[host]
		if m == nil {
			m = map[string]*rank{}
			byHost[host] = m
		}
		r := m[key]
		if r == nil {
			r = &rank{key: key, cpu: -1, priv: -1, ws: -1, sel: map[string]string{}, selSource: map[string]float64{}}
			m[key] = r
		}
		return r
	}
	for _, id := range processMetrics {
		for _, s := range e.idx.Series(id, resource.Filter{}) {
			key := s.Proc.Key()
			if key == "" || s.Host == "" {
				continue
			}
			r := get(s.Host, key)
			switch id {
			case resource.ProcCPU:
				if m, ok := e.mean(s); ok && m > r.cpu {
					r.cpu = m
				}
			case resource.ProcPrivate:
				if sm, ok := e.peak(s); ok && sm.Value > r.priv {
					r.priv = sm.Value
				}
			case resource.ProcWS:
				if sm, ok := e.peak(s); ok && sm.Value > r.ws {
					r.ws = sm.Value
				}
			}
			sel, ok := seriesSelector(s)
			if !ok {
				continue
			}
			cov := e.idx.Coverage(s)
			if old, seen := r.selSource[id]; !seen || cov > old {
				r.sel[id] = sel
				r.selSource[id] = cov
			}
		}
	}
	out := map[string][]Process{}
	for host, m := range byHost {
		list := make([]*rank, 0, len(m))
		for _, r := range m {
			if len(r.sel) > 0 {
				list = append(list, r)
			}
		}
		sort.Slice(list, func(i, j int) bool {
			a, b := list[i], list[j]
			switch {
			case a.cpu != b.cpu:
				return a.cpu > b.cpu
			case a.priv != b.priv:
				return a.priv > b.priv
			case a.ws != b.ws:
				return a.ws > b.ws
			}
			return a.key < b.key
		})
		if len(list) > e.th.TopN {
			list = list[:e.th.TopN]
		}
		for _, r := range list {
			out[host] = append(out[host], Process{Name: r.key, Sel: r.sel})
		}
	}
	return out
}

// seriesSelector writes the series selector that reads the raw events of a
// process series: kind, source, host, the attributes that name the measure and
// the process, and field=value.
func seriesSelector(s resource.Series) (string, bool) {
	var kind string
	var attrs [][2]string
	switch s.Source {
	case "win-counters", "sql-pdh":
		object, counter, ok := strings.Cut(s.Label, `\`)
		if !ok {
			return "", false
		}
		inst := s.Proc.Instance
		switch {
		case inst != "":
		case s.Proc.PID > 0 && strings.EqualFold(object, "Process V2") && s.Proc.Name != "":
			inst = s.Proc.Name + ":" + strconv.Itoa(s.Proc.PID)
		case s.Proc.PID > 0 && strings.EqualFold(object, "W3SVC_W3WP"):
			inst = "pid-" + strconv.Itoa(s.Proc.PID)
		default:
			return "", false // a legacy instance: its name at each sample is not kept
		}
		kind = "counter"
		attrs = [][2]string{{"object", object}, {"instance", inst}, {"counter", counter}}
	case "win-procsnap", "win-live", "linux-pidstat", "dotnet-counters":
		name := "metric"
		if s.Source == "dotnet-counters" {
			name = "counter"
		}
		if strings.Contains(s.Label, "[") {
			return "", false // tagged counters carry attributes the label folds in
		}
		kind = "metric"
		attrs = [][2]string{{name, s.Label}}
		switch {
		case s.Proc.PID > 0:
			attrs = append(attrs, [2]string{"pid", strconv.Itoa(s.Proc.PID)})
		case s.Proc.Name != "":
			attrs = append(attrs, [2]string{"process", s.Proc.Name})
		default:
			return "", false
		}
	default:
		return "", false
	}
	parts := []string{"kind=" + kind, "source=" + s.Source, "host=" + s.Host}
	for _, a := range attrs {
		parts = append(parts, "attr."+a[0]+"="+a[1])
	}
	for _, p := range parts {
		_, v, _ := strings.Cut(p, "=")
		if !selectorValueOK(v) {
			return "", false
		}
	}
	return strings.Join(append(parts, "field=value"), ";"), true
}

// selectorValueOK reports whether v can stand as one selector value: not
// empty, at most selectorValueMax bytes, no ";" or "=", no control or format
// characters.
func selectorValueOK(v string) bool {
	if v == "" || len(v) > selectorValueMax || strings.ContainsAny(v, ";=") {
		return false
	}
	for _, r := range v {
		if r == unicode.ReplacementChar || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) {
			return false
		}
	}
	return true
}
