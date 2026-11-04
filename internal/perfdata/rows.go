package perfdata

import (
	"errors"
	"math"
	"sort"
	"strconv"
	"time"
)

var errDayRange = errors.New("day outside the window")

// endpoints is the URL set of the dataset: a Zipf-weighted list, a few of which
// carry a numeric id in the path (high-cardinality stems, like /orders/48211).
type endpoints struct {
	stems []string
	hasID []bool
	cum   []float64
}

var epResources = []string{"orders", "customers", "invoices", "payments", "shipments", "products", "inventory", "users", "sessions", "reports", "search", "auth", "carts", "coupons", "returns", "refunds", "catalog", "pricing", "tax", "notifications", "audit", "settings", "files", "jobs", "health"}
var epActions = []string{"list", "get", "create", "update", "cancel", "export", "summary", "status", "history", "validate", "lookup", "batch"}

func newEndpoints(n int) *endpoints {
	e := &endpoints{stems: make([]string, n), hasID: make([]bool, n), cum: make([]float64, n)}
	ids := 0
	var total float64
	for i := 0; i < n; i++ {
		res, act := epResources[i%len(epResources)], epActions[(i/len(epResources))%len(epActions)]
		e.stems[i] = "/api/v" + strconv.Itoa(1+i%3) + "/" + res + "/" + act
		if i%7 == 3 && ids < 40 {
			e.hasID[i] = true
			ids++
		}
		total += 1 / math.Pow(float64(i+1), 1.05)
		e.cum[i] = total
	}
	for i := range e.cum {
		e.cum[i] /= total
	}
	return e
}

func (e *endpoints) pick(u float64) int {
	i := sort.SearchFloat64s(e.cum, u)
	if i >= len(e.cum) {
		i = len(e.cum) - 1
	}
	return i
}

type iisRow struct {
	ts      time.Time
	method  string
	stem    string
	status  int
	ms      int
	clientI string
}

var statusOK = []struct {
	code int
	cum  float64
}{{200, 0.88}, {201, 0.91}, {204, 0.95}, {304, 0.99}, {302, 1}}
var status4xx = []struct {
	code int
	cum  float64
}{{400, 0.25}, {401, 0.45}, {403, 0.55}, {404, 0.95}, {429, 1}}
var status5xx = []struct {
	code int
	cum  float64
}{{500, 0.60}, {502, 0.75}, {503, 0.95}, {504, 1}}

func pickStatus(tbl []struct {
	code int
	cum  float64
}, u float64) int {
	for _, s := range tbl {
		if u < s.cum {
			return s.code
		}
	}
	return tbl[len(tbl)-1].code
}

var methodCum = []struct {
	m   string
	cum float64
}{{"GET", 0.74}, {"POST", 0.91}, {"PUT", 0.96}, {"DELETE", 0.98}, {"PATCH", 1}}

const (
	p5xxBase  = 0.004
	p4xxBase  = 0.03
	p5xxBurst = 0.30
)

// offsets returns n sorted random millisecond offsets in one minute.
func offsets(r interface{ IntN(int) int }, n int, buf []int) []int {
	buf = buf[:0]
	for i := 0; i < n; i++ {
		buf = append(buf, r.IntN(60000))
	}
	sort.Ints(buf)
	return buf
}

// iisDay yields the IIS rows of one day in time order.
func iisDay(cfg Config, eps *endpoints, dp *DayPlan, yield func(iisRow) error) error {
	r := newRand(cfg.Seed, streamIIS, epochDay(dp.Day))
	var buf []int
	for m := 0; m < minutesPerDay; m++ {
		n := int(dp.IISMin[m])
		if n == 0 {
			continue
		}
		burst := dp.BurstMin[m]
		buf = offsets(r, n, buf)
		base := dp.Day.Add(time.Duration(m) * time.Minute)
		for _, off := range buf {
			row := iisRow{ts: base.Add(time.Duration(off) * time.Millisecond)}
			u := r.Float64()
			switch {
			case burst && u < p5xxBurst, !burst && u < p5xxBase:
				row.status = pickStatus(status5xx, r.Float64())
			case !burst && u < p5xxBase+p4xxBase:
				row.status = pickStatus(status4xx, r.Float64())
			default:
				row.status = pickStatus(statusOK, r.Float64())
			}
			ep := eps.pick(r.Float64())
			row.stem = eps.stems[ep]
			if eps.hasID[ep] {
				row.stem += "/" + strconv.Itoa(1+r.IntN(900000))
			}
			mu := r.Float64()
			for _, mc := range methodCum {
				if mu < mc.cum {
					row.method = mc.m
					break
				}
			}
			ms := math.Exp(math.Log(45) + 1.63*r.NormFloat64())
			if burst {
				ms *= 2.5
			}
			row.ms = int(math.Min(120000, math.Max(1, math.Round(ms))))
			row.clientI = "10.20." + strconv.Itoa(r.IntN(8)) + "." + strconv.Itoa(1+r.IntN(250))
			if err := yield(row); err != nil {
				return err
			}
		}
	}
	return nil
}

// SQL event kinds as the sql-xe importer names them.
const (
	sqlWait        = "wait"
	sqlCall        = "call"
	sqlError       = "error"
	sqlDeadlock    = "deadlock"
	sqlNonYielding = "non-yielding"
)

type sqlRow struct {
	ts       time.Time
	kind     string
	event    string
	waitType string
	class    string
	errNo    int
	sqlSev   int
	ms       float64
	severity string
}

var waitTypes = []string{"LCK_M_S", "LCK_M_X", "LCK_M_U", "LCK_M_IX", "PAGEIOLATCH_SH", "PAGELATCH_EX", "WRITELOG", "ASYNC_NETWORK_IO", "CXPACKET", "RESOURCE_SEMAPHORE", "PREEMPTIVE_OS_WRITEFILE"}
var errNumbers = []int{2627, 515, 547, 1205, 8152, 18456, 4060, 233}

func waitClass(w string) string {
	switch {
	case len(w) > 4 && w[:4] == "LCK_":
		return "lock"
	case len(w) > 5 && (w[:5] == "PAGEI" || w[:5] == "PAGEL"):
		return "latch"
	case len(w) > 11 && w[:11] == "PREEMPTIVE_":
		return "preemptive"
	}
	return "other"
}

// sqlDay yields the SQL rows of one day in time order.
func sqlDay(cfg Config, dp *DayPlan, yield func(sqlRow) error) error {
	r := newRand(cfg.Seed, streamSQL, epochDay(dp.Day))
	var buf []int
	for m := 0; m < minutesPerDay; m++ {
		n := int(dp.SQLMin[m])
		if n == 0 {
			continue
		}
		burst := dp.BurstMin[m]
		buf = offsets(r, n, buf)
		base := dp.Day.Add(time.Duration(m) * time.Minute)
		for _, off := range buf {
			row := sqlRow{ts: base.Add(time.Duration(off) * time.Millisecond)}
			u := r.Float64()
			deadlockP, waitP := 0.015, 0.55
			if burst {
				deadlockP, waitP = 0.08, 0.70
			}
			switch {
			case u < 0.005:
				row.kind, row.severity = sqlNonYielding, "error"
				row.event = "scheduler_monitor_non_yielding_ring_buffer_recorded"
				row.ms = 5000 + r.Float64()*55000
			case u < 0.005+deadlockP:
				row.kind, row.severity = sqlDeadlock, "warning"
				row.event = "xml_deadlock_report"
			case u < 0.005+deadlockP+0.08:
				row.kind, row.severity = sqlError, "error"
				row.event = "error_reported"
				row.errNo = errNumbers[r.IntN(len(errNumbers))]
				row.sqlSev = 11 + r.IntN(10)
			case u < 0.005+deadlockP+0.08+waitP*(1-0.005-deadlockP-0.08):
				row.kind, row.severity = sqlWait, "warning"
				row.event = "wait_info"
				if burst && r.Float64() < 0.6 {
					row.waitType = waitTypes[r.IntN(4)]
				} else {
					row.waitType = waitTypes[r.IntN(len(waitTypes))]
				}
				row.class = waitClass(row.waitType)
				row.ms = math.Round(math.Exp(math.Log(400) + 1.2*r.NormFloat64()))
			default:
				row.kind, row.severity = sqlCall, "info"
				row.event = "rpc_completed"
				if r.IntN(3) == 0 {
					row.event = "sql_batch_completed"
				}
				row.ms = math.Round(math.Exp(math.Log(1500) + 1.0*r.NormFloat64()))
			}
			if err := yield(row); err != nil {
				return err
			}
		}
	}
	return nil
}
