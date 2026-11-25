package releasedash

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
)

// registerCI adds the routes of the CI side. They are read only like every other route here: the outer handler already
// answers only GET and HEAD, only to a loopback host name and only with the session cookie, and sets the CSP.
func (d *Dash) registerCI(mux *http.ServeMux) {
	view := func(w http.ResponseWriter, r *http.Request) (*ciView, bool) {
		id := r.URL.Query().Get("project")
		if len(id) > 40 {
			fail(w, http.StatusNotFound, "That project is not in the project list.")
			return nil, false
		}
		v, ok := d.ci(id)
		if !ok {
			fail(w, http.StatusNotFound, "That project is not in the project list.")
			return nil, false
		}
		return v, true
	}
	answer := func(w http.ResponseWriter, err error) {
		switch {
		case errors.Is(err, errBadID):
			fail(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, errNoRun):
			fail(w, http.StatusNotFound, err.Error())
		default:
			fail(w, http.StatusInternalServerError, err.Error())
		}
	}
	d.registerMachines(mux, view)
	mux.HandleFunc("/api/projects", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"projects": d.Projects2()})
	})
	mux.HandleFunc("/api/overview", func(w http.ResponseWriter, r *http.Request) {
		if v, ok := view(w, r); ok {
			writeJSON(w, v.Overview())
		}
	})
	mux.HandleFunc("/api/runs", func(w http.ResponseWriter, r *http.Request) {
		v, ok := view(w, r)
		if !ok {
			return
		}
		q := r.URL.Query()
		var f CIFilter
		if s := q.Get("status"); s != "" {
			if _, known := statusWords[s]; !known {
				fail(w, http.StatusBadRequest, "That is not a run status.")
				return
			}
			f.Status = s
		}
		if s := q.Get("pr"); s != "" {
			n, err := strconv.Atoi(s)
			if err != nil || n < 1 || n > 1<<30 {
				fail(w, http.StatusBadRequest, "A pull request number is a whole number above 0.")
				return
			}
			f.PR = n
		}
		if s := q.Get("sha"); s != "" {
			if f.SHA = shaOnly(s); f.SHA == "" {
				fail(w, http.StatusBadRequest, "A commit is 4 to 64 letters and digits (0-9, a-f).")
				return
			}
		}
		if s := q.Get("branch"); s != "" {
			if len(s) > 120 || strings.ContainsAny(s, "\x00\r\n") {
				fail(w, http.StatusBadRequest, "That is not a branch name.")
				return
			}
			f.Branch = s
		}
		if s := q.Get("release"); s != "" {
			if len(s) > 60 {
				fail(w, http.StatusBadRequest, "That is not a release version.")
				return
			}
			f.Release = s
		}
		limit := 0
		if s := q.Get("limit"); s != "" {
			n, err := strconv.Atoi(s)
			if err != nil || n < 1 {
				fail(w, http.StatusBadRequest, "The limit is a whole number above 0.")
				return
			}
			limit = n
		}
		cur := q.Get("cursor")
		if len(cur) > 80 {
			cur = ""
		}
		writeJSON(w, v.Page(f, limit, cur))
	})
	mux.HandleFunc("/api/run", func(w http.ResponseWriter, r *http.Request) {
		v, ok := view(w, r)
		if !ok {
			return
		}
		det, err := v.Detail(r.URL.Query().Get("id"))
		if err != nil {
			answer(w, err)
			return
		}
		writeJSON(w, det)
	})
	mux.HandleFunc("/api/run/log", func(w http.ResponseWriter, r *http.Request) {
		v, ok := view(w, r)
		if !ok {
			return
		}
		q := r.URL.Query()
		step, from, n := 0, 0, 0
		var err error
		for _, p := range []struct {
			key string
			dst *int
		}{{"step", &step}, {"from", &from}, {"n", &n}} {
			if s := q.Get(p.key); s != "" {
				if *p.dst, err = strconv.Atoi(s); err != nil {
					fail(w, http.StatusBadRequest, "The "+p.key+" must be a whole number.")
					return
				}
			}
		}
		out, err := v.StepLog(q.Get("id"), q.Get("job"), step, from, n, q.Get("tail") == "1")
		if err != nil {
			answer(w, err)
			return
		}
		writeJSON(w, out)
	})
}
