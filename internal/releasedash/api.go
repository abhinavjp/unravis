package releasedash

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strconv"
	"time"
)

const maxLogPage = 5000

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// registerDetail adds the per-release routes. Every one of them is read only.
func (d *Dash) registerDetail(mux *http.ServeMux) {
	ver := func(w http.ResponseWriter, r *http.Request) (string, bool) {
		v := r.URL.Query().Get("v")
		if _, ok := chanOf(v); !ok || len(v) > 60 {
			fail(w, http.StatusBadRequest, "That is not a release version.")
			return "", false
		}
		return v, true
	}
	net := func(w http.ResponseWriter, r *http.Request, f func(ctx context.Context, v string) (any, bool, error)) {
		v, ok := ver(w, r)
		if !ok {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		out, stale, err := f(ctx, v)
		if err != nil {
			// GitHub being out of reach is a normal state for this page, not a server fault: answer 200 with the reason so the browser console stays quiet.
			writeJSON(w, map[string]any{"data": nil, "stale": false, "error": err.Error()})
			return
		}
		if stale {
			w.Header().Set("X-Stale", "1")
		}
		writeJSON(w, map[string]any{"data": out, "stale": stale})
	}
	mux.HandleFunc("/api/release", func(w http.ResponseWriter, r *http.Request) {
		v, ok := ver(w, r)
		if !ok {
			return
		}
		det, err := d.Release(v, time.Now())
		if err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, det)
	})
	mux.HandleFunc("/api/release/tests", func(w http.ResponseWriter, r *http.Request) {
		v, ok := ver(w, r)
		if !ok {
			return
		}
		rep, err := d.Tests(v)
		if err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, rep)
	})
	mux.HandleFunc("/api/release/artifacts", func(w http.ResponseWriter, r *http.Request) {
		v, ok := ver(w, r)
		if !ok {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		info, err := d.Artifacts(ctx, v)
		if err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, info)
	})
	mux.HandleFunc("/api/release/checks", func(w http.ResponseWriter, r *http.Request) {
		net(w, r, func(ctx context.Context, v string) (any, bool, error) { return d.Checks(ctx, v) })
	})
	mux.HandleFunc("/api/release/changes", func(w http.ResponseWriter, r *http.Request) {
		net(w, r, func(ctx context.Context, v string) (any, bool, error) { return d.Changes(ctx, v) })
	})
	mux.HandleFunc("/api/log", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		from, _ := strconv.Atoi(q.Get("from"))
		n, err := strconv.Atoi(q.Get("n"))
		if err != nil || n <= 0 || n > maxLogPage {
			n = maxLogPage
		}
		if from < 0 {
			from = 0
		}
		loc := ReadLocal(d.DataDir)
		lines, cut, err := ReadLog(d.DataDir, q.Get("file"), loc.Names)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				fail(w, http.StatusNotFound, "That log is not on this PC.")
			} else {
				fail(w, http.StatusInternalServerError, "The log could not be read.")
			}
			return
		}
		total := len(lines)
		if from > total {
			from = total
		}
		end := from + n
		if end > total {
			end = total
		}
		writeJSON(w, map[string]any{"lines": lines[from:end], "from": from, "total": total, "cut": cut, "rev": foldRev(lines)})
	})
}
