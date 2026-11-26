package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"api-audit-explorer/internal/server"
	"api-audit-explorer/internal/tray"
	"api-audit-explorer/internal/version"
)

// What the tray says about updates: the tooltip, the menu lines, the attention dot and the pop-ups. The rules are plain functions of the
// last update status, so each state can be tested without a window. Design: docs/desktop/TRAY-AND-UPDATE-NOTICES.md.

// trayRefreshEvery is how often the cached update status is re-read (Status reads a small file, so it is kept off the tray thread; 15 s keeps idle wakeups low).
const trayRefreshEvery = 15 * time.Second

// trayAct is what pressing the update line does.
type trayAct int

const (
	actNone    trayAct = iota // the line is only information
	actInstall                // restart into the downloaded update
	actUpdates                // open the Updates page
)

// updateView is the update part of the tray for one status.
type updateView struct {
	Line      string // the menu line
	Act       trayAct
	Attention bool   // draw the dot on the icon
	Tip       string // the tooltip
}

// clipRunes shortens s to at most max characters, ending with "..." when it had to cut.
func clipRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max-3]) + "..."
}

// agoWords says how long ago t was, in words a first-time person understands.
func agoWords(now, t time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return plural(int(d/time.Minute), "minute") + " ago"
	case d < 48*time.Hour:
		return plural(int(d/time.Hour), "hour") + " ago"
	}
	return plural(int(d/(24*time.Hour)), "day") + " ago"
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

// versionLine is the greyed first line of the menu.
func versionLine(st server.UpdateStatus) string {
	v := st.Current
	if v == "" {
		v = version.Version
	}
	if st.Channel != "" {
		return clipRunes("Unravis "+v+" ("+st.Channel+" channel)", tray.MaxTextRunes)
	}
	return clipRunes("Unravis "+v, tray.MaxTextRunes)
}

// updateFailed reports whether the last update, unattended or not, did not work.
func updateFailed(st server.UpdateStatus) bool {
	if r := st.Result; r != nil && r.Outcome != "" && r.Outcome != "done" {
		return true
	}
	return st.Install != nil && st.Install.State == "failed"
}

// viewUpdate decides the update line, its action, the dot and the tooltip. checking is true while the person's own check runs.
func viewUpdate(st server.UpdateStatus, checking bool, now time.Time) updateView {
	base := "Unravis " + st.Current
	if st.Current == "" {
		base = "Unravis " + version.Version
	}
	v := updateView{Tip: base}
	inst := st.Install
	switch {
	case checking:
		v.Line = "Checking for updates..."
	case inst != nil && inst.State == "launched":
		v.Line = "Updating to " + inst.Version + "..."
		v.Tip = "Unravis is updating to " + inst.Version
	case inst != nil && inst.State == "downloading":
		v.Line = "Downloading " + inst.Version + "..."
		v.Tip = base + " · downloading " + inst.Version
	case updateFailed(st):
		v.Line = "Update failed. Still on " + st.Current + ": see Updates"
		v.Act = actUpdates
		v.Attention = true
		v.Tip = base + " · the last update did not work"
	case inst != nil && inst.State == "ready" && st.CanInstall:
		v.Line = "Update " + inst.Version + " is ready: Restart now"
		v.Act = actInstall
		v.Attention = true
		v.Tip = base + " · update ready, restart to finish"
	case st.Status == "available":
		v.Line = "Update " + st.Latest + " is available: see Updates"
		v.Act = actUpdates
		v.Tip = base + " · update " + st.Latest + " available"
	case st.Status == "latest":
		v.Line = "Up to date"
		if t, err := time.Parse(time.RFC3339, st.CheckedAt); err == nil {
			v.Line += ", checked " + agoWords(now, t)
		}
		v.Tip = base + " · up to date"
	case st.Status == "unreachable":
		v.Line = "Could not check for updates"
		v.Tip = base + " · could not check for updates"
	case st.Status == "unverified":
		v.Line = "Update check failed its safety check: see Updates"
		v.Act = actUpdates
	case st.Status == "dev":
		v.Line = "Development build: no update checks"
	default:
		v.Line = "No update check has run yet"
	}
	v.Line = clipRunes(v.Line, tray.MaxTextRunes)
	v.Tip = clipRunes(v.Tip, tray.MaxTooltipRunes)
	return v
}

// ---- cached status ----

// updateCache keeps the last update status for the tray thread, which must never wait on disk or network.
type updateCache struct {
	read     func() server.UpdateStatus
	mu       sync.RWMutex
	st       server.UpdateStatus
	checking atomic.Bool
	byPerson atomic.Bool // the person pressed Restart now, so no "updating" pop-up
}

func newUpdateCache(read func() server.UpdateStatus) *updateCache {
	c := &updateCache{read: read}
	c.refresh()
	return c
}

func (c *updateCache) refresh() server.UpdateStatus {
	st := c.read()
	c.mu.Lock()
	c.st = st
	c.mu.Unlock()
	return st
}

func (c *updateCache) get() server.UpdateStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.st
}

// check runs one person-started check. A second press while one runs does nothing.
func (c *updateCache) check(run func(ctx context.Context) server.UpdateStatus) {
	if !c.checking.CompareAndSwap(false, true) {
		return
	}
	defer c.checking.Store(false)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	run(ctx)
	c.refresh()
}

// run refreshes the cache every `every` until ctx ends, handing each fresh status to onStatus.
func (c *updateCache) run(ctx context.Context, every time.Duration, onStatus func(server.UpdateStatus)) {
	// idle-ok: one small file read every 15 s so the tray tooltip, dot and notices follow an unattended update; the tray app is the only caller.
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		onStatus(c.get())
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.refresh()
		}
	}
}

// ---- notices ----

// trayNotice is one pop-up chosen from the status. to names the screen a click opens ("" for none).
type trayNotice struct {
	key   string // remembered once told, so a restart does not repeat it
	title string
	text  string
	to    string
}

// pickNotice chooses the one pop-up the status calls for, or false. The states are checked from the most important down.
func pickNotice(st server.UpdateStatus, byPerson bool) (trayNotice, bool) {
	if r := st.Result; r != nil && r.Outcome != "" {
		key := r.Outcome + ":" + r.From + ">" + r.To
		if r.Outcome == "done" {
			if r.To != "" && st.Current == r.To {
				return trayNotice{key, "Unravis has been updated to " + r.To, "Click to see what is new.", "whatsnew"}, true
			}
		} else {
			title := "Unravis could not update"
			if r.To != "" {
				title += " to " + r.To
			}
			return trayNotice{key, title, "You are still on " + st.Current + ". Nothing was lost.", "updates"}, true
		}
	}
	if in := st.Install; in != nil {
		switch {
		case in.State == "ready" && st.CanInstall && !st.ApplyAuto:
			return trayNotice{"ready:" + in.Version, "An update is ready", in.Version + " will install when you restart Unravis.", "updates"}, true
		case in.State == "launched" && !byPerson:
			return trayNotice{"launching:" + in.Version, "Unravis is updating", "It will restart in a moment.", ""}, true
		}
	}
	return trayNotice{}, false
}

// toldStore remembers which notices were shown, in a small file, so a restart does not show them again.
type toldStore struct {
	path string
	mu   sync.Mutex
	keys []string
}

const toldKeep = 12

func openToldStore(path string) *toldStore {
	s := &toldStore{path: path}
	if b, err := os.ReadFile(path); err == nil && len(b) < 8<<10 { //nolint:gosec // the path is built from the user cache folder, not from input
		var f struct {
			Keys []string `json:"keys"`
		}
		if json.Unmarshal(b, &f) == nil {
			s.keys = f.Keys
		}
	}
	return s
}

func (s *toldStore) has(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range s.keys {
		if k == key {
			return true
		}
	}
	return false
}

// add records key and saves the list. A save that fails is ignored: the worst case is one repeated pop-up.
func (s *toldStore) add(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keys = append(s.keys, key)
	if len(s.keys) > toldKeep {
		s.keys = s.keys[len(s.keys)-toldKeep:]
	}
	if s.path == "" {
		return
	}
	b, err := json.Marshal(struct {
		Keys []string `json:"keys"`
	}{s.keys})
	if err != nil || os.MkdirAll(filepath.Dir(s.path), 0o700) != nil {
		return
	}
	tmp := s.path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil && os.Rename(tmp, s.path) != nil {
		_ = os.Remove(tmp)
	}
}

// noticer shows the pop-ups the status calls for, each once.
type noticer struct {
	told     *toldStore
	notify   func(tray.Notice) error
	openTo   func(to string)
	byPerson func() bool
	logf     func(format string, args ...any)
}

// onStatus is called with every fresh status. A notice is remembered only after it was handed to the tray, so one asked for before the
// icon is up is tried again on the next refresh.
func (n *noticer) onStatus(st server.UpdateStatus) {
	pick, ok := pickNotice(st, n.byPerson != nil && n.byPerson())
	if !ok || n.told.has(pick.key) {
		return
	}
	note := tray.Notice{Title: clipRunes(pick.title, tray.MaxNoticeTitleRunes), Text: clipRunes(pick.text, tray.MaxNoticeTextRunes)}
	if pick.to != "" && n.openTo != nil {
		to := pick.to
		note.OnClick = func() { n.openTo(to) }
	}
	switch err := n.notify(note); {
	case err == nil:
		n.told.add(pick.key)
	case errors.Is(err, tray.ErrNotRunning), errors.Is(err, tray.ErrUnsupported):
		// no icon yet; the next refresh tries again
	default:
		if n.logf != nil {
			n.logf("tray: pop-up not shown: %v", err)
		}
		n.told.add(pick.key) // a notice the tray refuses will not be accepted later either
	}
}

// toldPath is where the pop-up memory lives: the local cache folder, so it stays on this PC.
func toldPath() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "Unravis", "tray-notices.json")
}

// wireTrayUpdates connects the tray to the update checker: the cached status, the check and install actions, and the pop-ups. With no
// updater (a demo run) it changes nothing. The returned function waits for the refresh loop, which ends with ctx.
func wireTrayUpdates(ctx context.Context, d *trayDeps, updater *server.Updater, notify func(tray.Notice) error, logf func(string, ...any)) (stop func()) {
	if updater == nil {
		return func() {}
	}
	cache := newUpdateCache(updater.Status)
	d.update = cache.get
	d.checking = cache.checking.Load
	d.checkUpdates = func() { cache.check(updater.CheckNow) }
	d.installUpdate = func() error {
		cache.byPerson.Store(true)
		err := updater.Install()
		if err != nil {
			cache.byPerson.Store(false)
		}
		return err
	}
	n := &noticer{told: openToldStore(toldPath()), notify: notify, openTo: d.openTo, byPerson: cache.byPerson.Load, logf: logf}
	done := make(chan struct{})
	go func() {
		defer close(done)
		cache.run(ctx, trayRefreshEvery, n.onStatus)
	}()
	return func() { <-done }
}
