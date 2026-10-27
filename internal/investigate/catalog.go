package investigate

// The hypothesis catalog (research/investigation-walkthrough.md, section 4). Each
// entry states what must hold, what is expected, the cheapest check and what would
// overturn it. Nothing here names a cause: an entry is a suspect, and the card says
// what the data shows and what it does not.
//
// Phase A reads the audit (and IIS) only. An entry that needs another lane carries
// LaneRequired: it is "Cannot test yet" until Input.Lanes says what that lane's own
// data shows. M3 and M4 do not apply in production (standalone Mongo) and are kept
// apart. H2, M2 and H3 are grey in Release 1 (decision A26).

// Group letters.
const (
	GroupQ = "Q" // data quality
	GroupL = "L" // load and capacity
	GroupS = "S" // queueing and saturation, app tier
	GroupD = "D" // SQL Server
	GroupM = "M" // MongoDB
	GroupH = "H" // host, runtime and infrastructure
	GroupC = "C" // change
)

// CatalogEntry is one suspect.
type CatalogEntry struct {
	ID    string `json:"id"`
	Group string `json:"group"`
	Title string `json:"title"`
	// Tier is whose onset orders this suspect: load, a, c, o, sql, mongo or host.
	// Empty means the entry has no tier of its own (its onset is unknown).
	Tier string `json:"tier,omitempty"`
	// Must are signals that must be yes. Expect are signals that normally hold; one
	// that reads no leaves the suspect only partly consistent.
	Must   []string `json:"must,omitempty"`
	Expect []string `json:"expect,omitempty"`
	// LaneRequired: the entry cannot be tested from the audit. Phase B (or a lane
	// seam) supplies Input.Lanes[ID].
	LaneRequired bool `json:"laneRequired,omitempty"`
	// GreyR1 forces grey in Release 1 (A26).
	GreyR1 bool `json:"greyR1,omitempty"`
	// NotInProduction entries are listed apart and never coloured.
	NotInProduction bool   `json:"notInProduction,omitempty"`
	Check           string `json:"check"`
	Overturn        string `json:"overturn"`
	// Needs is what to import or enable to test it; empty when the audit suffices.
	Needs string `json:"needs,omitempty"`
}

// Catalog returns the entries in catalog order.
func Catalog() []CatalogEntry {
	lane := func(id, group, title, tier, check, overturn, needs string, must ...string) CatalogEntry {
		return CatalogEntry{ID: id, Group: group, Title: title, Tier: tier, Must: must, LaneRequired: true, Check: check, Overturn: overturn, Needs: needs}
	}
	sqlNeeds := "SQL Server wait, blocking or Query Store snapshots for the incident window."
	mongoNeeds := "MongoDB slow-operation lines or server status for the incident window."
	hostNeeds := "Host performance counters for the incident window."
	out := []CatalogEntry{
		{ID: "Q1", Group: GroupQ, Title: "Clock, time-zone or DST error", Tier: "",
			Check:    "Compare the time-zone rule of each import and count calls that end before they start.",
			Overturn: "All sources validated to UTC and consistent with each other.",
			Needs:    "A second source with its own clock, to compare against the audit."},
		{ID: "Q2", Group: GroupQ, Title: "Audit coverage gap or survivorship", Tier: "",
			Check:    "Audit count per minute divided by the IIS request count for the same paths.",
			Overturn: "Audit and IIS counts agree.",
			Needs:    "IIS log rows for the incident and reference windows."},

		{ID: "L1", Group: GroupL, Title: "Traffic surge", Tier: "load", Must: []string{SigArrivalsUp},
			Check:    "Calls per minute against the reference; a per-call service time that did not move early separates a surge from a slowdown.",
			Overturn: "Calls per second flat or down."},
		{ID: "L2", Group: GroupL, Title: "Mix shift: one route or client dominates", Tier: "c",
			Must: []string{SigP95Up, SigScopeNarrow}, Expect: []string{SigP50Flat},
			Check:    "Group the slow calls by route, client and body size.",
			Overturn: "The rise is spread across routes."},
		{ID: "L3", Group: GroupL, Title: "Retry storm or client loop (amplifier)", Tier: "load",
			Must: []string{SigRetriesUp}, Expect: []string{SigErrorsPrecede},
			Check:    "Group by client and route; compare the first failure with the start of the surge.",
			Overturn: "No failures before the surge."},

		{ID: "S1", Group: GroupS, Title: "HTTP.sys or IIS queue backup", Tier: "o", Must: []string{SigOLarge},
			Check:    "HTTP.sys counters and the HTTPERR log against the audit duration.",
			Overturn: "The queue length stays at zero.",
			Needs:    "IIS log rows; HTTPERR and queue counters for a direct reading."},
		{ID: "S2", Group: GroupS, Title: ".NET thread-pool starvation", Tier: "o", Must: []string{SigOOrALarge},
			Check:    "Thread-pool queue length and thread count; latency of a trivial route.",
			Overturn: "Thread-pool queue length flat at zero.",
			Needs:    "IIS log rows; .NET counters for a direct reading."},
		{ID: "S3", Group: GroupS, Title: "SqlClient connection-pool exhaustion", Tier: "c",
			Must:     []string{SigCLarge, SigLAtLimit},
			Check:    "Pool counters and pool-timeout log lines against the running-request count on SQL Server.",
			Overturn: "Free connections remain and no pool timeouts appear.",
			Needs:    "The SqlClient pool limit (entered), and pool counters for a direct reading."},
		lane("S4", GroupS, "Mongo driver pool or wait-queue exhaustion", "a",
			"Driver check-out wait against the command duration.", "Check-out wait is normal.",
			"Driver pool events or a pool-wait log classifier.", SigALarge),
		{ID: "S5", Group: GroupS, Title: "Audit-write coupling", Tier: "a",
			Must: []string{SigALarge}, Expect: []string{SigScopeAll},
			Check:    "Server-side latency of inserts on the audit collection only; confirm the write is awaited inside the request.",
			Overturn: "A is small.",
			Needs:    "MongoDB slow-operation lines to separate set-up from the insert."},
		lane("S6", GroupS, "Slow downstream HTTP dependency", "c",
			"Timing by route and by outbound call.", "SQL or Mongo time accounts for C.",
			"Timings of outbound calls.", SigCLarge),
		lane("S7", GroupS, "Slow permission check", "c",
			"The permission filter's own timing from PreActionFilterTimings, then the service behind it.",
			"The filter takes under a millisecond or is missing from the timings.",
			"PreActionFilterTimings in the audit documents.", SigCLarge),
	}
	// SQL Server
	d := []struct{ id, title, check, overturn string }{
		{"D1", "Blocking chain (head blocker or open transaction)", "Head blocker in the blocking snapshot or blocked-process report.", "No lock waits."},
		{"D2", "Deadlock storm and retries", "system_health deadlock events.", "No deadlocks logged."},
		{"D3", "Plan regression or parameter sniffing", "Plan history of the affected query.", "The plan did not change."},
		{"D4", "Statistics change or auto-update recompile", "Statistics last-updated time against t0.", "No statistics update near t0."},
		{"D5", "Cold cache after restart, failover or plan-cache clear", "SQL Server start time and the error log.", "Page life expectancy steady and no restart."},
		{"D6", "Memory pressure or grant waits", "Memory grants with a null grant time.", "No grant waits."},
		{"D7", "tempdb contention or spills", "Waiting-task resource descriptions starting 2:.", "No latch waits on tempdb."},
		{"D8", "Log flush stall, autogrow or large VLF count", "Log-file write stall per IO.", "WRITELOG wait flat."},
		{"D9", "Disk IO latency", "Per-file IO stall deltas.", "Normal IO latency."},
		{"D10", "SQL CPU saturation or parallelism", "SQL CPU against OS CPU.", "CPU is low."},
		{"D11", "Maintenance job overlap", "Job history and backup history against t0, in UTC.", "No job overlaps t0."},
		{"D12", "Availability-group synchronous-commit delay", "Wait delta and availability-group health.", "No availability group."},
		{"D13", "Worker-thread exhaustion", "THREADPOOL wait delta.", "Zero THREADPOOL wait."},
		{"D14", "Query Store itself (recently enabled)", "Onset against the time Query Store was enabled; its state and size.", "Store healthy and onset unrelated to the enable time."},
		{"D15", "Data growth crossing a threshold, or a missing index", "Multi-day reads-per-execution trend.", "A sudden step with no history of growth."},
	}
	for _, e := range d {
		out = append(out, lane(e.id, GroupD, e.title, "sql", e.check, e.overturn, sqlNeeds, SigCLarge))
	}
	m := []struct{ id, title, check, overturn, must string }{
		{"M1", "WiredTiger cache pressure or application-thread eviction", "Cache and eviction series.", "Cache steady.", SigALarge},
		{"M2", "Disk IO saturation or checkpoint stall", "Disk latency series.", "No regular cadence.", SigALarge},
		{"M5", "Ticket or lock queue", "Duration minus working time; global lock queue.", "Both equal.", SigALarge},
		{"M6", "Collection scan or missing index on the audit collection", "Plan summary in the slow lines.", "Index scan in use.", SigALarge},
		{"M7", "TTL delete burst, index build or large delete", "Log lines for TTL and index builds.", "None present.", SigALarge},
		{"M8", "Large payloads or document growth", "Correlation of A or P with body size.", "Uniform sizes.", SigPLarge},
		{"M9", "Connection storm or churn", "Connections created, per minute.", "Steady.", SigALarge},
		{"M10", "Shared mongod: another tenant or maintenance job", "Slow lines grouped by database; current operations by namespace.", "Other databases flat while this one is slow.", SigALarge},
	}
	for _, e := range m {
		out = append(out, lane(e.id, GroupM, e.title, "mongo", e.check, e.overturn, mongoNeeds, e.must))
	}
	h := []struct{ id, title, check, overturn string }{
		{"H1", "Host CPU contention", "CPU by process.", "Process CPU accounts for it."},
		{"H2", "GC pause (gen 2 or large object heap)", "GC pause counters.", "GC time flat."},
		{"H3", "App-pool recycle, cold start, JIT", "Recycle event times.", "No recycle."},
		{"H4", "Network (retransmits, DNS, TLS, port exhaustion)", "Retransmit counter and port-exhaustion events.", "Retransmits flat."},
		{"H5", "App-host disk or certificate-check delay", "Timing mode of the first call.", "No bimodal slowness."},
		{"H6", "Shared effect across tiers (VM, hypervisor, storage, network)", "Whether trivial, unrelated operations slow at the same moment on every tier.", "Only one tier is slow."},
	}
	for _, e := range h {
		out = append(out, lane(e.id, GroupH, e.title, "host", e.check, e.overturn, hostNeeds))
	}
	out = append(out, CatalogEntry{ID: "C1", Group: GroupC, Title: "Deploy, configuration or infrastructure change", Tier: "",
		Must:     []string{SigChangesLead},
		Check:    "Change calendar against t0: a lead, not an overlap.",
		Overturn: "No change listed within the lead window before t0.",
		Needs:    "The list of releases and changes, typed in."})
	for i := range out {
		switch out[i].ID {
		case "H2", "M2", "H3":
			out[i].GreyR1 = true
		}
	}
	return out
}

// NotApplicable lists the entries that do not apply in production. They are shown
// apart and never coloured.
func NotApplicable() []CatalogEntry {
	return []CatalogEntry{
		{ID: "M3", Group: GroupM, Title: "Write concern, replication lag or flow control", NotInProduction: true,
			Check: "Replica-set lag against local write time.", Overturn: "Production Mongo is standalone."},
		{ID: "M4", Group: GroupM, Title: "Election or failover", NotInProduction: true,
			Check: "The process start time and the Windows service log.", Overturn: "Production Mongo is standalone."},
	}
}

// catalogIndex finds an entry by id.
func catalogIndex() map[string]CatalogEntry {
	m := map[string]CatalogEntry{}
	for _, e := range Catalog() {
		m[e.ID] = e
	}
	return m
}
