package resource

import (
	"strconv"
	"strings"
	"time"

	"api-audit-explorer/internal/evidence"
)

// GapLegacyInstance is the gap a process series carries when no process id was
// found for it, so it stays on its counter instance name.
const GapLegacyInstance = "legacy instance numbers can move between processes"

// Proc is the identity of a process series.
//
//   - Process V2 "w3wp:1240" gives Name w3wp and PID 1240.
//   - A legacy instance "w3wp#1" is re-keyed per sample through the ID Process
//     counter (Process) or the Process ID counter (.NET CLR Memory) of the same
//     instance at the same sample time.
//   - W3SVC_W3WP "pid-1240" gives Name w3wp and PID 1240, so it joins the
//     Process V2 series of the same pid.
//   - win-procsnap (and linux-pidstat) give Name and PID; Start is the latest
//     process-start at or before the sample for that host, name and pid, so a
//     reused pid gives a separate series.
//   - dotnet-counters gives a PID only; Name is filled from another series of
//     the same host with that pid when exactly one name is seen for it.
//
// Without a pid, Instance holds the counter instance name.
type Proc struct {
	Name     string
	PID      int
	Start    time.Time // zero when unknown
	Instance string    // counter instance, set only when PID is 0
}

// Key is "name:pid", "pid-<pid>" when only the pid is known, or the instance
// name when no pid is known. It is "" for a host series.
func (p Proc) Key() string {
	switch {
	case p.PID > 0 && p.Name != "":
		return p.Name + ":" + strconv.Itoa(p.PID)
	case p.PID > 0:
		return "pid-" + strconv.Itoa(p.PID)
	default:
		return p.Instance
	}
}

// IsZero reports whether p names no process.
func (p Proc) IsZero() bool { return p.Name == "" && p.PID == 0 && p.Instance == "" }

// pidClass groups the counter objects whose legacy instances share one pid
// counter: Process (ID Process) and the .NET CLR objects (.NET CLR Memory
// Process ID). The CLR numbers its instances on its own, so the two never mix.
type pidClass string

const (
	classNone    pidClass = ""
	classProcess pidClass = "process"
	classCLR     pidClass = "clr"
)

// objectClass returns the pid class of a PDH object, and whether its instances
// name processes at all.
func objectClass(object string) (pidClass, bool) {
	lo := strings.ToLower(object)
	switch {
	case lo == "process":
		return classProcess, true
	case strings.HasPrefix(lo, ".net clr"):
		return classCLR, true
	case lo == "process v2", lo == "w3svc_w3wp":
		return classNone, true
	}
	return classNone, false
}

// aggregateInstance reports whether a counter instance sums many processes
// rather than naming one.
func aggregateInstance(inst string) bool {
	switch strings.ToLower(inst) {
	case "_total", "__total__", "_global_":
		return true
	}
	return false
}

// imageOf strips the "#N" a legacy instance name carries for the second and
// later process of one image.
func imageOf(inst string) string {
	if i := strings.LastIndexByte(inst, '#'); i > 0 {
		if _, err := strconv.Atoi(inst[i+1:]); err == nil {
			return inst[:i]
		}
	}
	return inst
}

// parsePID reads a positive process id.
func parsePID(s string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// pdhIdentity is the identity a PDH counter instance gives on its own. legacy
// is true when the pid must come from the pid counter of class at the sample
// time. skip is true for an aggregate or idle instance, which names no process.
func pdhIdentity(object, inst string) (p Proc, class pidClass, legacy, skip bool) {
	class, _ = objectClass(object)
	if inst == "" || aggregateInstance(inst) {
		return Proc{}, class, false, true
	}
	switch strings.ToLower(object) {
	case "process v2":
		name, pidS, ok := cutLast(inst, ':')
		if pid, good := parsePID(pidS); ok && good {
			return Proc{Name: name, PID: pid}, class, false, false
		}
		if strings.EqualFold(name, "Idle") || strings.EqualFold(name, "_Total") {
			return Proc{}, class, false, true
		}
		return Proc{Name: imageOf(inst), Instance: inst}, class, false, false
	case "w3svc_w3wp":
		if rest, ok := strings.CutPrefix(inst, "pid-"); ok {
			if pid, good := parsePID(rest); good {
				return Proc{Name: "w3wp", PID: pid}, class, false, false
			}
		}
		return Proc{Instance: inst}, class, false, false
	}
	if strings.EqualFold(inst, "Idle") {
		return Proc{}, class, false, true
	}
	return Proc{Name: imageOf(inst), Instance: inst}, class, true, false
}

func cutLast(s string, sep byte) (before, after string, ok bool) {
	i := strings.LastIndexByte(s, sep)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+1:], true
}

// attrIdentity is the identity of a win-procsnap, linux-pidstat or
// dotnet-counters event, from its process and pid attributes.
func attrIdentity(ev evidence.Event) Proc {
	pid, _ := parsePID(ev.Attrs["pid"])
	name := ev.Attrs["process"]
	if pid == 0 && name == "" {
		return Proc{}
	}
	if pid == 0 {
		return Proc{Name: name, Instance: name}
	}
	return Proc{Name: name, PID: pid}
}

// isPIDCounter reports whether a PDH counter holds the pid of its instance.
func isPIDCounter(class pidClass, object, counter string) bool {
	switch class {
	case classProcess:
		return strings.EqualFold(counter, "ID Process")
	case classCLR:
		return strings.EqualFold(object, ".NET CLR Memory") && strings.EqualFold(counter, "Process ID")
	}
	return false
}

// uptimeCounter reports whether an event is an uptime-like value whose drop is
// a restart: Elapsed Time of a process, System Up Time, the mongod uptime and
// the Redis uptime_in_seconds.
func uptimeCounter(ev evidence.Event) (procScoped, ok bool) {
	switch ev.Source {
	case sourceWinCounters, sourceSQLPDH:
		if ev.Kind != "counter" {
			return false, false
		}
		o, c := ev.Attrs["object"], ev.Attrs["counter"]
		if strings.EqualFold(c, "Elapsed Time") && (strings.EqualFold(o, "Process") || strings.EqualFold(o, "Process V2")) {
			return true, true
		}
		if strings.EqualFold(o, "System") && strings.EqualFold(c, "System Up Time") {
			return false, true
		}
	case sourceMongoSS:
		return false, ev.Kind == "metric" && ev.Attrs["metric"] == "uptime"
	case sourceRedisInfo:
		return false, ev.Kind == "metric" && ev.Attrs["metric"] == "uptime_in_seconds"
	}
	return false, false
}
