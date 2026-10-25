package main

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"time"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/casestore/sqlite"
	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/hosts"
	"api-audit-explorer/internal/importer/all"
	"api-audit-explorer/internal/ledger"
	"api-audit-explorer/internal/mcp"
	"api-audit-explorer/internal/sealedfile"
	"api-audit-explorer/internal/server"
)

// casesWiring is what live mode adds for the importer wave: the encrypted
// per-case store, the sealed host map, and the server options that expose them.
type casesWiring struct {
	options []server.Option
	store   *sqlite.Store
}

// close closes every case handle the store still holds (zeroing their keys).
func (c *casesWiring) close() {
	if c != nil && c.store != nil {
		_ = c.store.Close()
	}
}

// casesRoot is where the case folders live. With AUDIT_CONFIG_DIR set they sit
// beside the other application files (so a test or a portable setup keeps
// everything in one folder). Otherwise they use the local, non-roaming
// application folder, because one case can hold gigabytes that must never roam
// with the Windows profile (the config folder does).
func casesRoot(getenv func(string) string) (string, error) {
	if dir := getenv("AUDIT_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "cases"), nil
	}
	return sqlite.DefaultRoot()
}

// eraseHook returns the OnErase hook of the case store. It removes every ledger
// reference to the erased case's evidence ("erased-case-evidence"). Export
// previews are held in memory only and the server drops them in the erase
// handler, so there are no export files to delete here. The hook is idempotent,
// as the store requires (Erase retries it and Sweep re-runs it). Without a
// ledger there is nothing to strip.
func eraseHook(hypotheses ledger.Store) func(context.Context, casestore.CaseID) error {
	return func(_ context.Context, id casestore.CaseID) error {
		stripper, ok := hypotheses.(ledger.Stripper)
		if !ok {
			return nil
		}
		_, err := stripper.StripEvidence(evidence.CasePrefixes(string(id)), "erased-case-evidence")
		return err
	}
}

// openCases opens the case store and the sealed host map. It fails closed: when
// encrypted storage is unavailable, or a file cannot be opened, the caller
// leaves the case and host routes unwired (they answer 503) and the rest of the
// dashboard keeps working. Nothing is ever written in plaintext.
func openCases(getenv func(string) string, hypotheses ledger.Store) (*casesWiring, error) {
	base, err := mcp.SessionDir(getenv)
	if err != nil {
		return nil, err
	}
	root, err := casesRoot(getenv)
	if err != nil {
		return nil, err
	}
	protector, err := sealedfile.CurrentUser()
	if err != nil {
		return nil, fmt.Errorf("encrypted storage is unavailable on this platform")
	}
	sealedHosts, err := hosts.OpenSealed(filepath.Join(base, "hosts.dat"), protector)
	if err != nil {
		return nil, err
	}
	var onErase func(context.Context, casestore.CaseID) error
	if hypotheses != nil {
		onErase = eraseHook(hypotheses)
	}
	// The master-key session starts locked when masterkey.dat exists in base and
	// "none" otherwise. The store and the server share it, so unlock, lock and
	// the 423 gate act on the one Keyring the store opens cases with.
	session := server.NewMasterKeySession(base)
	store, err := sqlite.New(sqlite.Options{
		Root: root, Protector: protector, OnErase: onErase,
		Protectors: session.Lookup, NewCaseKind: session.NewCaseKind,
	})
	if err != nil {
		return nil, err
	}
	// Finish any erase that was interrupted and erase cases closed over 30 days
	// ago. A failure is logged by case; it never stops the dashboard starting.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if erased, sweepErr := store.Sweep(ctx, time.Now()); sweepErr != nil {
		log.Printf("Case cleanup did not finish: %v", sweepErr)
	} else if len(erased) > 0 {
		log.Printf("Erased %d expired case(s)", len(erased))
	}
	return &casesWiring{
		store: store,
		options: []server.Option{
			server.WithCases(store, sealedHosts.Map(), all.Registry()),
			server.WithHostsSaver(sealedHosts.Save),
			server.WithSystemDiscover(),
			server.WithMasterKey(session),
		},
	}, nil
}
