package main

import (
	"errors"
	"log"
	"os"
	"path/filepath"

	"api-audit-explorer/internal/bbsaved"
	"api-audit-explorer/internal/remote/pull"
	"api-audit-explorer/internal/remote/registry"
	"api-audit-explorer/internal/remote/service"
	"api-audit-explorer/internal/server"
)

// wireServers attaches the Servers pages: the sealed list of servers (servers.dat beside blackboxes.dat), the service that signs in and reads,
// and the folder pulled files land in. Demo mode leaves them off, since nothing there may reach a real server. A list that cannot be opened
// leaves the pages off with a log line; it never stops Unravis from starting.
//
// bb is the Blackbox list, used only to look up the read key of a server's Blackbox. Nil means the per-user sealed list.
func wireServers(demo bool, getenv func(string) string, bb server.BlackboxStore) []server.Option {
	if demo {
		return nil
	}
	reg, err := registry.NewDefault(getenv)
	if err != nil {
		log.Printf("Servers list unavailable: %v", err)
		return nil
	}
	if bb == nil {
		if list, lerr := bbsaved.NewDefault(getenv); lerr == nil {
			bb = list
		}
	}
	root, err := stagingRoot(getenv)
	if err != nil {
		log.Printf("Servers unavailable, no folder for pulled files: %v", err)
		return nil
	}
	svc, err := service.New(service.Deps{
		Reg: reg,
		BBKey: func(id string) (string, error) {
			if bb == nil {
				return "", errors.New("the Blackbox list is not available")
			}
			return bb.Key(id)
		},
		StagingRoot: root,
		Limits:      pull.DefaultLimits(),
	})
	if err != nil {
		log.Printf("Servers unavailable: %v", err)
		return nil
	}
	return []server.Option{server.WithServers(server.ServersDeps{Reg: reg, Svc: svc, StagingRoot: root})}
}

// stagingRoot is where pulled files land. It is bulk data, so it sits in the cache folder, not beside the sealed lists. AUDIT_CONFIG_DIR moves it for tests.
func stagingRoot(getenv func(string) string) (string, error) {
	base := ""
	if getenv != nil {
		base = getenv("AUDIT_CONFIG_DIR")
	}
	if base == "" {
		var err error
		if base, err = os.UserCacheDir(); err != nil {
			return "", err
		}
		base = filepath.Join(base, "APIAuditExplorer")
	}
	return filepath.Join(base, "pulled"), nil
}
