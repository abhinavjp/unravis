package investigate

import (
	"fmt"
	"math/rand"
	"time"
)

// The scenario generator builds deterministic Inputs for the tests and the golden
// files. It is test code only: the engine never sees how a case was made.

var day = time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)

func at(h, m int) time.Time {
	return day.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute)
}

type mod struct {
	from, to time.Time
	routes   []string // empty means every route
	addA     float64
	addC     float64
	addO     float64
	countMul float64 // 0 means unchanged
}

type genCfg struct {
	seed    int64
	ref     Window
	inc     Window
	perMin  int
	routes  []string
	mods    []mod
	gaps    []Window
	iis     bool
	clients int
	// iisDrop removes IIS rows (never audit rows) inside these windows, to make the
	// audit share of IIS requests look high; auditDrop does the opposite for audit.
	auditDrop []Window
	// serverErrorFrom marks calls from this time on as server errors (zero: never).
	serverErrorFrom time.Time
	// retryEvery makes every n-th call in retryWindow repeat itself 2 s later.
	retryEvery  int
	retryWindow Window
}

func (g genCfg) active(m mod, t time.Time, route string) bool {
	if t.Before(m.from) || !t.Before(m.to) {
		return false
	}
	if len(m.routes) == 0 {
		return true
	}
	for _, r := range m.routes {
		if r == route {
			return true
		}
	}
	return false
}

func inAny(ws []Window, t time.Time) bool {
	for _, w := range ws {
		if w.Contains(t) {
			return true
		}
	}
	return false
}

func gen(cfg genCfg) Input {
	rng := rand.New(rand.NewSource(cfg.seed))
	if len(cfg.routes) == 0 {
		cfg.routes = []string{"Orders.Get", "Orders.Save", "Users.List"}
	}
	if cfg.clients == 0 {
		cfg.clients = 400
	}
	in := Input{Incident: cfg.inc, Reference: cfg.ref, Audit: AuditPresent, IISPresent: cfg.iis}
	var id int
	emit := func(route string, start time.Time, a, c float64, o float64, client string) {
		p := 5.0
		d := a + c
		end := start.Add(time.Duration(d * float64(time.Millisecond)))
		id++
		outcome := "success"
		if !cfg.serverErrorFrom.IsZero() && !start.Before(cfg.serverErrorFrom) {
			outcome = "server_error"
		}
		req := Request{
			ID: fmt.Sprintf("r%05d", id), Route: route, Start: start, End: &end,
			A: &a, C: &c, P: &p, Path: NormalizePath("/api/" + route), Client: client, Outcome: outcome,
		}
		if !inAny(cfg.auditDrop, start) {
			in.Requests = append(in.Requests, req)
		}
		if cfg.iis {
			iend := end.Add(time.Duration(p * float64(time.Millisecond)))
			taken := d + p + o
			in.IIS = append(in.IIS, IISRow{
				Start: iend.Add(-time.Duration(taken * float64(time.Millisecond))), End: iend, TakenMs: taken, Path: req.Path,
			})
		}
	}
	for _, w := range []Window{cfg.ref, cfg.inc} {
		for m := minuteOf(w.Start); m.Before(w.End); m = m.Add(time.Minute) {
			if inAny(cfg.gaps, m) {
				continue
			}
			n := cfg.perMin
			for _, md := range cfg.mods {
				if md.countMul > 0 && cfg.active(md, m, "") {
					n = int(float64(n) * md.countMul)
				}
			}
			for k := 0; k < n; k++ {
				route := cfg.routes[k%len(cfg.routes)]
				start := m.Add(time.Duration(rng.Intn(59000)) * time.Millisecond)
				a := 8 + rng.Float64()*4
				c := 60 + rng.Float64()*40
				o := 2 + rng.Float64()*2
				for _, md := range cfg.mods {
					if cfg.active(md, m, route) {
						a += md.addA
						c += md.addC
						o += md.addO
					}
				}
				client := fmt.Sprintf("u%d", rng.Intn(cfg.clients))
				emit(route, start, a, c, o, client)
				if cfg.retryEvery > 0 && cfg.retryWindow.Contains(start) && k%cfg.retryEvery == 0 {
					emit(route, start.Add(2*time.Second), a, c, o, client)
				}
			}
		}
	}
	return in
}

// stdWindows are a one-hour reference at 09:00 and a ninety-minute incident at 10:00.
func stdWindows() (Window, Window) {
	return Window{at(9, 0), at(10, 0)}, Window{at(10, 0), at(11, 30)}
}
