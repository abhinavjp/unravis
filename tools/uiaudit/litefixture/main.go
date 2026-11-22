// Command litefixture is the UI audit's stand-in for a server running Unravis Lite. It records a Blackbox folder with the
// real recorder (internal/blackbox) from a made-up but steady set of readings, then serves that folder through the same
// handlers Lite uses (internal/lite and internal/server) and the built Lite page. The real unravis-lite program only accepts
// a Windows folder, so on other systems this is the way to see the Lite page; the program itself is covered by its Go tests.
//
//	go run ./tools/uiaudit/litefixture --ui cmd/unravis-lite/ui --now 2026-10-20T09:00:00Z
//
// It prints one line: "listening at http://127.0.0.1:PORT token=TOKEN". Open http://127.0.0.1:PORT/#t=TOKEN.
package main

import (
	"flag"
	"fmt"
	"io/fs"
	"math"
	"net"
	"net/http"
	"os"
	"os/signal"
	"time"

	"api-audit-explorer/internal/blackbox"
	"api-audit-explorer/internal/lite"
	"api-audit-explorer/internal/server"
)

const token = "uiaudit-lite-fixture-token-0123456789"

func main() {
	uiDir := flag.String("ui", "cmd/unravis-lite/ui", "the built Lite page")
	nowText := flag.String("now", "2026-10-20T09:00:00Z", "the clock of the fixture (RFC 3339)")
	hours := flag.Int("hours", 3, "hours of readings to record")
	stopped := flag.Bool("stopped", false, "end the recording 40 minutes before now and close it (shows a Blackbox that stopped)")
	flag.Parse()
	now, err := time.Parse(time.RFC3339, *nowText)
	if err != nil {
		fail(err)
	}
	dir, err := os.MkdirTemp("", "litefixture-")
	if err != nil {
		fail(err)
	}
	defer os.RemoveAll(dir)
	end := now.Add(-20 * time.Second)
	if *stopped {
		end = now.Add(-40 * time.Minute)
	}
	if err := record(dir, end.Add(-time.Duration(*hours)*time.Hour), end, *stopped); err != nil {
		fail(err)
	}
	ui, err := fs.Sub(os.DirFS(*uiDir), ".")
	if err != nil {
		fail(err)
	}
	loc := blackbox.Location{Kind: blackbox.LocFolder, Path: `D:\Blackbox`}
	inner := server.New(lite.NoData{}, ui, token, false,
		server.WithBlackbox(server.BlackboxDeps{
			Store: lite.NewStore("Web server 01", loc, now),
			Now:   func() time.Time { return now },
			Open: func(l blackbox.Location, _ string) blackbox.Source {
				return &blackbox.FolderSource{Loc: l, Access: blackbox.DirAccess{Dir: dir}}
			},
		}))
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		fail(err)
	}
	fmt.Printf("listening at http://%s token=%s\n", ln.Addr().String(), token)
	srv := &http.Server{Handler: lite.Wrap(inner), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt)
		<-c
		_ = srv.Close()
	}()
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "litefixture: "+err.Error())
	os.Exit(1)
}

// shape gives each tick a reading that rises and falls the way a busy server's does, with one busy spell.
type shape struct{ n int }

func (s *shape) Collect(time.Time) (blackbox.Sample, error) {
	s.n++
	x := float64(s.n)
	cpu := 28 + 12*math.Sin(x/40) + 4*math.Sin(x/7)
	if s.n > 300 && s.n < 340 {
		cpu = 92 - float64(s.n-300)/5
	}
	avail := uint64(6.5e9 - 1.2e9*math.Sin(x/90))
	commit := uint64(9.1e9 + 0.6e9*math.Sin(x/120))
	queue := math.Max(0, 0.4+0.5*math.Sin(x/23))
	ms := 3 + 2*math.Sin(x/31)
	in, out := uint64(s.n)*1_300_000, uint64(s.n)*480_000
	return blackbox.Sample{CPU: &cpu, MemAvail: &avail, MemCommit: &commit, DiskQueue: &queue, DiskMs: &ms, NetIn: &in, NetOut: &out}, nil
}
func (*shape) Close() error { return nil }

type okDisk struct{}

func (okDisk) Free(string) (blackbox.DiskInfo, error) {
	return blackbox.DiskInfo{Free: 62, Total: 100}, nil
}

// record writes the folder with the real recorder, one tick every 15 seconds, with one gap of four minutes.
func record(dir string, start, end time.Time, close bool) error {
	cfg := blackbox.Default()
	cfg.Name = "Web server 01"
	cfg.Folder = dir
	cfg.Record.LogsCatalogue = false
	now := start
	var mono time.Duration
	store, err := blackbox.OpenStore(dir, blackbox.StoreOptions{Name: cfg.Name, Version: "uiaudit", Host: "WEB01", IntervalSeconds: 15, RunID: "r1", Now: func() time.Time { return now }})
	if err != nil {
		return err
	}
	rec := blackbox.NewRecorder(blackbox.RecorderOptions{Config: cfg, Store: store, Collector: &shape{}, Disk: okDisk{}, RunID: "r1",
		Now: func() time.Time { return now }, Mono: func() time.Duration { return mono }})
	rec.Tick(now)
	gapAt := start.Add(end.Sub(start) / 2)
	for now.Before(end) {
		step := 15 * time.Second
		if now.Before(gapAt) && !now.Add(step).Before(gapAt) {
			step = 4 * time.Minute // nothing is recorded for four minutes
		}
		now = now.Add(step)
		mono += step
		rec.Tick(now)
	}
	if close {
		return store.Close("uiaudit")
	}
	return nil // left open: the Blackbox looks like it is still recording
}
