package server

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"time"

	"api-audit-explorer/internal/update"
	"api-audit-explorer/internal/version"
)

// The auto-updater's checker (docs/auto-updater/SPEC.md sections 6 and 7). It only finds out whether a newer, signed release
// exists. It downloads nothing and installs nothing; those are later slices and always wait for a click.

const (
	updateDefaultMinutes = 30
	updateMinMinutes     = 5
	updateMaxMinutes     = 7 * 24 * 60
	updateSettingsLimit  = 4 * 1024
)

// updateFirstDelay is the wait before the first check after start; a variable only so a test can shorten it.
var updateFirstDelay = 60 * time.Second

// UpdaterSettings is what the person can change. A new install checks automatically every 30 minutes.
type UpdaterSettings struct {
	Auto            bool `json:"auto"`
	IntervalMinutes int  `json:"intervalMinutes"`
}

// Updater owns the background check, its settings file and the last answer.
type Updater struct {
	dir     string
	fetcher *update.Fetcher
	channel string // channel of this build; "" for a development build

	mu       sync.Mutex
	settings UpdaterSettings
	last     update.Result
	checked  time.Time
	haveLast bool
	failures int
	wake     chan struct{}
	checkMu  sync.Mutex // one check at a time
	saveMu   sync.Mutex // one settings save at a time
	rnd      *rand.Rand
}

// NewUpdater reads the saved settings from dir (created on first save). keys are the public keys this program trusts.
func NewUpdater(dir string, keys []update.Key) *Updater {
	u := &Updater{
		dir:      dir,
		settings: UpdaterSettings{Auto: true, IntervalMinutes: updateDefaultMinutes},
		wake:     make(chan struct{}, 1),
		rnd:      rand.New(rand.NewSource(time.Now().UnixNano())),
	}
	if v, err := update.ParseVersion(version.Version); err == nil {
		u.channel = update.ChannelOf(v)
	}
	u.fetcher = &update.Fetcher{
		Keys: keys, Build: u.channel, Current: version.Version,
		StatePath: filepath.Join(dir, "state.json"),
	}
	u.loadSettings()
	return u
}

func (u *Updater) settingsPath() string { return filepath.Join(u.dir, "settings.json") }

func (u *Updater) loadSettings() {
	fi, err := os.Stat(u.settingsPath())
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > updateSettingsLimit {
		return
	}
	b, err := os.ReadFile(u.settingsPath())
	if err != nil {
		return
	}
	s := UpdaterSettings{Auto: true, IntervalMinutes: updateDefaultMinutes}
	if json.Unmarshal(b, &s) != nil {
		return
	}
	if cleaned, err := cleanUpdaterSettings(s); err == nil {
		u.settings = cleaned
	}
}

func cleanUpdaterSettings(s UpdaterSettings) (UpdaterSettings, error) {
	if s.IntervalMinutes == 0 {
		s.IntervalMinutes = updateDefaultMinutes
	}
	if s.IntervalMinutes < updateMinMinutes || s.IntervalMinutes > updateMaxMinutes {
		return s, errors.New("the check interval must be between 5 minutes and 7 days")
	}
	return s, nil
}

// SetSettings validates, saves and applies new settings. A failed save is an error: the person sees that the choice was not kept.
func (u *Updater) SetSettings(s UpdaterSettings) (UpdaterSettings, error) {
	s, err := cleanUpdaterSettings(s)
	if err != nil {
		return s, err
	}
	u.saveMu.Lock()
	defer u.saveMu.Unlock()
	b, _ := json.Marshal(s)
	if err := os.MkdirAll(u.dir, 0o700); err != nil {
		return s, errors.New("could not save the update settings")
	}
	tmp, err := os.CreateTemp(u.dir, ".settings-*")
	if err != nil {
		return s, errors.New("could not save the update settings")
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return s, errors.New("could not save the update settings")
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return s, errors.New("could not save the update settings")
	}
	if err := tmp.Close(); err != nil {
		return s, errors.New("could not save the update settings")
	}
	if err := os.Rename(tmp.Name(), u.settingsPath()); err != nil {
		return s, errors.New("could not save the update settings")
	}
	u.mu.Lock()
	u.settings = s
	u.mu.Unlock()
	select { // wake the loop so a new interval or an off switch applies now
	case u.wake <- struct{}{}:
	default:
	}
	return s, nil
}

// Settings returns the current settings.
func (u *Updater) Settings() UpdaterSettings {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.settings
}

// UpdateStatus is what the screens show.
type UpdateStatus struct {
	Status          string `json:"status"` // latest, available, unreachable, unverified, dev or none (no check has run yet)
	Current         string `json:"current"`
	Latest          string `json:"latest"`
	URL             string `json:"url"`
	Channel         string `json:"channel"`
	CheckedAt       string `json:"checkedAt"`
	Auto            bool   `json:"auto"`
	IntervalMinutes int    `json:"intervalMinutes"`
}

// Status is the last known answer plus the settings. It makes no network call.
func (u *Updater) Status() UpdateStatus {
	u.mu.Lock()
	defer u.mu.Unlock()
	st := UpdateStatus{Status: "none", Current: version.Version, Channel: u.channel, Auto: u.settings.Auto, IntervalMinutes: u.settings.IntervalMinutes}
	if u.channel == "" {
		st.Status = "dev"
		return st
	}
	if !u.haveLast {
		return st
	}
	st.CheckedAt = u.checked.UTC().Format(time.RFC3339)
	switch u.last.Status {
	case update.StatusAvailable:
		st.Status = "available"
		if m := u.last.Manifest; m != nil {
			st.Latest = m.Version
			// Built here from the signed version, never taken from the feed.
			st.URL = "https://github.com/abhinavjp/unravis/releases/tag/v" + m.Version
		}
	case update.StatusLatest:
		st.Status = "latest"
	case update.StatusRejected:
		st.Status = "unverified"
	case update.StatusDev:
		st.Status = "dev"
	default:
		st.Status = "unreachable"
	}
	return st
}

// checkOnce runs one check and stores the answer. Only one runs at a time, so the timer and the button never overlap.
// ok is false when ctx ended first (nothing is stored then).
func (u *Updater) checkOnce(ctx context.Context) (res update.Result, ok bool) {
	u.checkMu.Lock()
	defer u.checkMu.Unlock()
	res = u.fetcher.Check(ctx)
	if ctx.Err() != nil {
		return res, false
	}
	u.mu.Lock()
	u.last, u.checked, u.haveLast = res, time.Now(), true
	if res.Backoff {
		u.failures++
	} else {
		u.failures = 0
	}
	u.mu.Unlock()
	return res, true
}

// CheckNow runs one check at once (the "Check for updates" button) and returns the new status.
func (u *Updater) CheckNow(ctx context.Context) UpdateStatus {
	u.checkOnce(ctx)
	return u.Status()
}

// Run is the background loop. It does nothing for a development build and while checking is switched off. It stops when ctx ends.
func (u *Updater) Run(ctx context.Context) {
	if u.channel == "" {
		return
	}
	delay := updateFirstDelay
	var nextAt time.Time // when the next check was planned; zero while off
	if u.currentSettings().Auto {
		nextAt = time.Now().Add(delay)
	}
	for {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-u.wake:
			timer.Stop()
			// The settings changed. Keep a wait the server asked for; otherwise follow the new interval.
			st := u.currentSettings()
			u.mu.Lock()
			failures := u.failures
			u.mu.Unlock()
			switch {
			case !st.Auto:
				delay, nextAt = 24*time.Hour, time.Time{}
			case nextAt.IsZero():
				delay = updateFirstDelay // just turned on
				nextAt = time.Now().Add(delay)
			default:
				remaining := time.Until(nextAt)
				if remaining < time.Second {
					remaining = time.Second
				}
				if iv := time.Duration(st.IntervalMinutes) * time.Minute; failures == 0 && iv < remaining {
					remaining = iv
				}
				delay = remaining
				nextAt = time.Now().Add(delay)
			}
			continue
		case <-timer.C:
		}
		st := u.currentSettings()
		if !st.Auto {
			delay, nextAt = 24*time.Hour, time.Time{}
			continue
		}
		res, ok := u.checkOnce(ctx)
		if !ok {
			return
		}
		u.mu.Lock()
		failures := u.failures
		u.mu.Unlock()
		delay = update.NextDelay(time.Duration(st.IntervalMinutes)*time.Minute, failures, res.RetryIn, u.rnd)
		nextAt = time.Now().Add(delay)
	}
}

func (u *Updater) currentSettings() UpdaterSettings {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.settings
}

func (u *Updater) hasChecked() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.haveLast
}
