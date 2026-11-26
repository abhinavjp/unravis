package blackbox

import (
	"sort"
	"sync"
	"time"
)

// The collector catalogue (B1): every source the recorder reads has one id, a kind, a plain title, a load tag and a
// rights need. The catalogue is data, written in code. blackbox.json can switch a catalogue item on or off and change
// how often it runs ("collectors"), but it can never add an item: a new technology still needs a release (PLAN 4.1).
//
// Ids are "<area>.<name>" in lower case. They are the names the CLI, the settings file, the status page and, later,
// the timeline use for the same source, so they are not renamed once released.

// Collector kinds (PLAN 4.1).
const (
	KindCounter = "counter" // a Windows performance counter path
	KindDMV     = "dmv"     // a fixed compiled SQL statement
	KindCommand = "command" // a fixed read-only protocol call, such as Redis INFO
	KindFile    = "file"    // reads a file with caps
	KindPath    = "path"    // records path, size and time only, never content
)

// Load tags say how much a collector asks of the server.
const (
	LoadLight = "light"
	LoadHeavy = "heavy"
)

// CollectorInfo describes one source.
type CollectorInfo struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Title string `json:"title"`
	// Group is the name of the recorder group that reads it ("" when a separate service reads it).
	Group string `json:"group,omitempty"`
	Load  string `json:"load"`
	// Needs says what the source needs beyond the recorder's own rights ("" when nothing).
	Needs string `json:"needs,omitempty"`
	// Always means it cannot be switched off: it is part of the recorder's own health picture.
	Always bool `json:"always,omitempty"`
}

var catalogue = []CollectorInfo{
	{ID: "host.diskcpu", Kind: KindCounter, Title: "Processor, disk and network", Group: "diskCpu", Load: LoadLight, Always: true},
	{ID: "host.mempressure", Kind: KindCounter, Title: "Memory pressure", Group: "memPressure", Load: LoadLight, Always: true},
	{ID: "host.tcp", Kind: KindCounter, Title: "TCP connections and resets", Group: "tcp", Load: LoadLight},
	{ID: "host.vm", Kind: KindCounter, Title: "Virtual machine counters", Group: "vm", Load: LoadLight},
	{ID: "host.events", Kind: KindCommand, Title: "Selected Windows events", Group: "events", Load: LoadLight},
	{ID: "host.dumps", Kind: KindPath, Title: "Crash dump list", Group: "dumps", Load: LoadLight},
	{ID: "proc.top10", Kind: KindCounter, Title: "Busiest programs", Group: "proc10", Load: LoadLight},
	{ID: "proc.io", Kind: KindCounter, Title: "Disk and memory use of watched programs", Group: "procIO", Load: LoadLight},
	{ID: "web.http", Kind: KindCounter, Title: "HTTP request queues", Group: "http", Load: LoadLight},
	{ID: "web.iis", Kind: KindCounter, Title: "IIS sites and app pools", Group: "iis", Load: LoadLight},
	{ID: "web.aspnet", Kind: KindCounter, Title: "ASP.NET requests and restarts", Group: "aspnet", Load: LoadLight},
	{ID: "web.clr", Kind: KindCounter, Title: ".NET memory and threads", Group: "clr", Load: LoadLight},
	{ID: "sql.host", Kind: KindCounter, Title: "SQL Server counters in Windows", Group: "sqlHost", Load: LoadLight},
	{ID: "sql.errlog", Kind: KindFile, Title: "SQL Server ERRORLOG", Group: "sqlLog", Load: LoadLight},
}

// Catalogue returns every known collector, sorted by id. The caller gets a copy.
func Catalogue() []CollectorInfo {
	out := append([]CollectorInfo(nil), catalogue...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// LookupCollector finds a collector by id.
func LookupCollector(id string) (CollectorInfo, bool) {
	for _, c := range catalogue {
		if c.ID == id {
			return c, true
		}
	}
	return CollectorInfo{}, false
}

// CollectorOfGroup finds the collector a recorder group reads.
func CollectorOfGroup(group string) (CollectorInfo, bool) {
	for _, c := range catalogue {
		if c.Group == group {
			return c, true
		}
	}
	return CollectorInfo{}, false
}

// CollectorSetting is one entry of the "collectors" map in blackbox.json. A missing On means the default (on).
type CollectorSetting struct {
	On *bool `json:"on,omitempty"`
	// EverySec runs the collector once per this many seconds. 0 means every sample.
	EverySec int `json:"everySec,omitempty"`
}

const maxCollectorSettings = 128

// allowedEvery are the choices for "everySec": whole minutes or the short intervals the sample interval allows.
var allowedEvery = map[int]bool{5: true, 15: true, 30: true, 60: true, 300: true, 900: true, 3600: true}

func (c Config) validateCollectors() error {
	if len(c.Collectors) > maxCollectorSettings {
		return invalidf(`"collectors" holds at most %d entries`, maxCollectorSettings)
	}
	for id, s := range c.Collectors {
		info, ok := LookupCollector(id)
		if !ok {
			return invalidf(`"collectors" names %q, which is not a known collector`, id)
		}
		if s.On != nil && !*s.On && info.Always {
			return invalidf(`"collectors.%s" cannot be switched off: the recorder needs it`, id)
		}
		if s.EverySec != 0 {
			if !allowedEvery[s.EverySec] {
				return invalidf(`"collectors.%s.everySec" must be 5, 15, 30, 60, 300, 900 or 3600`, id)
			}
			if s.EverySec < c.IntervalSeconds || s.EverySec%c.IntervalSeconds != 0 {
				return invalidf(`"collectors.%s.everySec" must be a whole multiple of "intervalSeconds" (%d)`, id, c.IntervalSeconds)
			}
		}
	}
	return nil
}

// CollectorOn says whether the collector with this id runs under this configuration.
func (c Config) CollectorOn(id string) bool {
	if s, ok := c.Collectors[id]; ok && s.On != nil {
		return *s.On
	}
	return true
}

// CollectorEvery is how often the collector runs: the sample interval unless "collectors" says otherwise.
func (c Config) CollectorEvery(id string) time.Duration {
	if s, ok := c.Collectors[id]; ok && s.EverySec > c.IntervalSeconds {
		return time.Duration(s.EverySec) * time.Second
	}
	return time.Duration(c.IntervalSeconds) * time.Second
}

// CollectorState is one line of "config show": the catalogue entry plus what this configuration says.
type CollectorState struct {
	CollectorInfo
	On       bool `json:"on"`
	EverySec int  `json:"everySec"`
}

// CollectorStates lists every collector with its state under c, sorted by id.
func (c Config) CollectorStates() []CollectorState {
	cat := Catalogue()
	out := make([]CollectorState, 0, len(cat))
	for _, info := range cat {
		out = append(out, CollectorState{CollectorInfo: info, On: c.CollectorOn(info.ID), EverySec: int(c.CollectorEvery(info.ID) / time.Second)})
	}
	return out
}

// everyGroup runs its inner group once per interval. In between it says "not applicable", which the tick treats as
// "leave no trace": no Missing entry, no timing, no budget strike. The first sample always runs.
type everyGroup struct {
	inner Group
	every time.Duration
	mu    sync.Mutex
	last  time.Time
}

func (g *everyGroup) Name() string   { return g.inner.Name() }
func (g *everyGroup) Optional() bool { return g.inner.Optional() }
func (g *everyGroup) Dormant() bool  { return dormantNow(g.inner) }

func (g *everyGroup) Collect(now time.Time, dt float64) (any, error) {
	g.mu.Lock()
	// Half a sample of slack: a tick that arrives a little early must not skip a whole period.
	due := g.last.IsZero() || now.Sub(g.last) >= g.every-g.every/20
	if due {
		g.last = now
	}
	g.mu.Unlock()
	if !due {
		return nil, errNotApplicable
	}
	return g.inner.Collect(now, dt)
}

func (g *everyGroup) Close() error {
	if cl, ok := g.inner.(interface{ Close() error }); ok {
		return cl.Close()
	}
	return nil
}
