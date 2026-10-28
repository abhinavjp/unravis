package main

import (
	"log"
	"os"
	"path/filepath"

	"api-audit-explorer/internal/bbsaved"
	"api-audit-explorer/internal/blackbox"
	"api-audit-explorer/internal/server"
)

// wireBlackbox attaches the Blackbox pages. Demo mode gets the in-memory world
// (nine Blackboxes, a pretend this PC) and touches nothing real. Live mode gets
// the sealed list of Blackboxes, the real service scan and, when
// unravis-blackbox.exe sits next to this program, the elevated helper and the pack
// builder. A list that cannot be opened leaves the pages off with a log line; it
// never stops Unravis from starting.
//
// kept is the key keeper's Blackbox list in the shared layout (P14): the read keys stay in the keeper, which does the keyed reads. Nil means
// the per-user sealed list, as before.
func wireBlackbox(demo bool, cfg demoSettings, getenv func(string) string, kept server.BlackboxStore) []server.Option {
	if demo {
		return []server.Option{server.WithBlackboxDemo(cfg.now)}
	}
	var saved server.BlackboxStore = kept
	if saved == nil {
		list, err := bbsaved.NewDefault(getenv)
		if err != nil {
			log.Printf("Blackbox list unavailable: %v", err)
			return nil
		}
		saved = list
	}
	deps := server.BlackboxDeps{
		Store: saved,
		Scan: func() (blackbox.LocalFound, error) {
			return blackbox.ScanThisPC(blackbox.WinService{}, blackbox.OSEnv{}, blackbox.LoadConfig)
		},
	}
	var dirs []string
	if exe, exeErr := os.Executable(); exeErr == nil {
		dir := filepath.Dir(exe)
		dirs = []string{dir, filepath.Join(dir, "blackbox")}
	}
	if program, ok := blackbox.FindProgram(dirs...); ok {
		deps.Program = program
		deps.ThisPC = &blackbox.ThisPC{Elev: blackbox.OSElevator{}, Program: program.Path}
	}
	return []server.Option{server.WithBlackbox(deps)}
}
