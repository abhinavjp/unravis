package main

import (
	"context"
	"errors"
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
	options   []server.Option
	store     *sqlite.Store
	recoverer *casestore.Recoverer
	erased    server.ErasedLog // the Erased lines on the start screen
}

// recoverAtStart runs the case recovery hooks once at start-up. It never stops
// the dashboard starting: a locked store or a hook that fails is logged, and the
// marked case keeps answering "being freed" or "being erased" until the next try
// (at unlock, or at the next call that touches it).
func recoverAtStart(r *casestore.Recoverer) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rep, err := r.RecoverAll(ctx)
	switch {
	case errors.Is(err, casestore.ErrLocked):
		// Nothing to read until the master key is unlocked; the unlock path runs it.
	case err != nil:
		log.Printf("Case recovery did not run: %v", err)
	case rep.Marked > 0:
		log.Printf("Case recovery: %d marked, %d finished, %d need another try", rep.Marked, rep.Recovered, len(rep.Failed))
	}
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

// withErasedLine runs next, then records the erase on the Erased lines. A failure to record is
// logged and never fails the erase: the case is gone either way.
func withErasedLine(next func(context.Context, casestore.CaseID) error, erased server.ErasedLog) func(context.Context, casestore.CaseID) error {
	return func(ctx context.Context, id casestore.CaseID) error {
		if next != nil {
			if err := next(ctx, id); err != nil {
				return err
			}
		}
		if err := erased.Add(id, time.Now()); err != nil {
			log.Printf("The erased line of a case could not be saved: %v", err)
		}
		return nil
	}
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
	return openCasesWith(getenv, hypotheses, caseSeams{})
}

// wireCases decides which case wiring the server gets. Live mode opens the
// real store and carries on without it when that fails. Plain --demo gets
// nothing and touches no file. --demo with UNRAVIS_DEMO_CASES=1 gets the seeded
// capture store, and a failure there stops start-up (see openDemoCases).
func wireCases(demo bool, cfg demoSettings, getenv func(string) string, hypotheses ledger.Store) ([]server.Option, func(), error) {
	switch {
	case !demo:
		wiring, err := openCases(getenv, hypotheses)
		if err != nil {
			log.Printf("Imported cases unavailable: %v", err)
			return nil, func() {}, nil
		}
		return wiring.options, wiring.close, nil
	case cfg.cases:
		wiring, err := openDemoCases(getenv, hypotheses, cfg.now)
		if err != nil {
			return nil, func() {}, err
		}
		return wiring.options, wiring.close, nil
	}
	return nil, func() {}, nil
}

// caseSeams are the few things the demo's capture-only cases change about the
// store: the clock, the IDs of new cases, and which case counts as untrusted.
// Live mode passes the zero value and gets exactly what it had before. When
// now is set the start-up sweep is skipped, so a made-up clock can never erase a
// case.
type caseSeams struct {
	now        func() time.Time
	newID      func() (casestore.CaseID, error)
	wrapLegacy func(inner func(casestore.CaseID) error) func(casestore.CaseID) error
	keySalt    func() []byte // a fixed key salt for the demo's keyed case; nil in live mode
}

func openCasesWith(getenv func(string) string, hypotheses ledger.Store, seams caseSeams) (*casesWiring, error) {
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
	// An erase leaves one line on the start screen (id and time, nothing else). The line is
	// added by the store's erase hook, so an erase that start-up recovery or the retention
	// sweep finishes leaves it too.
	erasedLog := server.NewErasedFile(filepath.Join(base, "erased.json"))
	var onErase func(context.Context, casestore.CaseID) error
	if hypotheses != nil {
		onErase = eraseHook(hypotheses)
	}
	onErase = withErasedLine(onErase, erasedLog)
	// The master-key session starts locked when masterkey.dat exists in base and
	// "none" otherwise. The store and the server share it, so unlock, lock and
	// the 423 gate act on the one Keyring the store opens cases with.
	session := server.NewMasterKeySession(base)
	legacyCheck := session.LegacyCheck
	if seams.wrapLegacy != nil {
		legacyCheck = seams.wrapLegacy(legacyCheck)
	}
	store, err := sqlite.New(sqlite.Options{
		Root: root, Protector: protector, OnErase: onErase,
		Protectors: session.Lookup, NewCaseKind: session.NewCaseKind,
		LegacyCheck: legacyCheck, LegacyDone: session.LegacyDone,
		Now: seams.now, NewID: seams.newID, NewKeySalt: seams.keySalt,
	})
	if err != nil {
		return nil, err
	}
	recoverer, err := casestore.NewRecoverer(store)
	if err != nil {
		return nil, err
	}
	wiring := &casesWiring{
		store:     store,
		recoverer: recoverer,
		erased:    erasedLog,
		options: []server.Option{
			server.WithCases(store, sealedHosts.Map(), all.Registry()),
			server.WithHostsSaver(sealedHosts.Save),
			server.WithSystemDiscover(),
			server.WithMasterKey(session),
			server.WithRecoverer(recoverer),
			server.WithErasedLog(erasedLog),
		},
	}
	// An Erasing mark (a case whose erase was marked and not finished) is finished by the
	// store's own Erase, which is idempotent and needs no case key.
	if err := recoverer.Register(casestore.MarkErasing, func(ctx context.Context, id casestore.CaseID) error {
		return store.Erase(ctx, id)
	}); err != nil {
		return nil, err
	}
	// Finish any move that a stop interrupted (a case still carrying an Archiving,
	// Freeing or Erasing mark). With the master key locked the list cannot be read
	// yet; the server runs the same recovery again at unlock. The hooks are
	// registered by the code that makes the marks (P3 and P4), through
	// wiring.recoverer, before the server starts taking calls.
	recoverAtStart(recoverer)
	if seams.now != nil {
		return wiring, nil
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
	return wiring, nil
}
