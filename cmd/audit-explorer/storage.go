package main

import (
	"fmt"
	"log"
	"os"
	"regexp"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/bbsaved"
	"api-audit-explorer/internal/datadir"
	"api-audit-explorer/internal/history"
	"api-audit-explorer/internal/keeper"
	"api-audit-explorer/internal/mcp"
	"api-audit-explorer/internal/sealedfile"
	"api-audit-explorer/internal/server"
	"api-audit-explorer/internal/sqlconn"
)

// storage is where this run keeps its files and how it seals them (P14, see internal/datadir).
//
//   - Per user: the folders and the per-user sealing every earlier phase used.
//   - Shared (C:\ProgramData\Unravis made by the installer, or UNRAVIS_DATA_DIR): cases, the host map, the erased lines, the master-key
//     check and the ledger sit in the shared folder, sealed for this PC. Saved passwords, saved MongoDB links and Blackbox read keys are
//     not opened by Unravis at all: they are asked of the key keeper by name.
type storage struct {
	layout datadir.Layout
	seal   func() (sealedfile.Protector, error)
}

// resolveLayout is the layout seam; tests leave it alone and pick the layout with UNRAVIS_DATA_DIR or AUDIT_CONFIG_DIR.
var resolveLayout = datadir.Default

// userStorage is the per-user layout whatever the PC has (the demo's capture store).
func userStorage() storage {
	return storage{layout: datadir.Layout{Mode: datadir.ModeUser}, seal: sealedfile.CurrentUser}
}

func storageFor(getenv func(string) string) storage {
	l := resolveLayout(getenv)
	if l.SharedRefused != "" {
		log.Printf("The shared data folder is not used, so Unravis keeps your files in your own folder: %s", l.SharedRefused)
	}
	if l.Shared() {
		// Several people may be signed in at once: their locks on shared files must be machine-wide.
		sealedfile.SetSharedRoot(l.Root)
		return storage{layout: l, seal: sealedfile.LocalMachine}
	}
	sealedfile.SetSharedRoot("")
	return storage{layout: l, seal: sealedfile.CurrentUser}
}

// base is the folder for hosts.dat, erased.json, masterkey.dat and the ledger.
func (s storage) base(getenv func(string) string) (string, error) {
	if s.layout.Shared() {
		return s.layout.Base, nil
	}
	// Same folder as saved connections and the session file; see history.NewDefault.
	return mcp.SessionDir(getenv)
}

// casesRoot is the case folder root.
func (s storage) casesRoot(getenv func(string) string) (string, error) {
	if s.layout.Shared() {
		return s.layout.Cases, nil
	}
	return casesRoot(getenv)
}

func (s storage) protector() (sealedfile.Protector, error) {
	p, err := s.seal()
	if err != nil {
		return nil, fmt.Errorf("encrypted storage is unavailable on this platform")
	}
	return p, nil
}

// savedWiring is the saved-connection side: the server options and the Blackbox list.
type savedWiring struct {
	options []server.Option
	// bb is the Blackbox list to use; nil means open the per-user list as before.
	bb server.BlackboxStore
	// keeper is the key keeper client of the shared layout (nil per user): audit sources opened from a saved MongoDB link whose
	// password it holds are opened and read in the keeper (P14b, see auditRepo).
	keeper *keeper.Client
}

// auditRepo is the audit repository Unravis serves: its own one per user, and in the shared layout one that sends the sources opened
// from keeper-held saved links to the keeper (the keeper's password never reaches this process).
func (w savedWiring) auditRepo(local audit.Repository) audit.Repository {
	if w.keeper == nil {
		return local
	}
	return keeper.NewAuditRouter(local, w.keeper)
}

// wireSaved attaches the saved SQL and MongoDB connections. Per user, they are the sealed per-user files as before. Shared, every saved
// secret is the key keeper's: Unravis gets stores that ask the keeper by name, and the person's old per-user files are offered for the move
// (they stay on disk until the person confirms the move worked).
func wireSaved(getenv func(string) string, s storage) savedWiring {
	if !s.layout.Shared() {
		var opts []server.Option
		if saved, err := history.NewDefault(getenv); err == nil {
			opts = append(opts, server.WithHistory(saved))
		}
		if saved, err := sqlconn.NewDefault(getenv); err == nil {
			opts = append(opts, server.WithSQLConnections(saved))
		}
		return savedWiring{options: opts}
	}
	c := keeper.NewClient(keeper.NewPipeDialer(s.layout.Pipe))
	legacy := legacyStores(getenv)
	mongo := keeper.NewMongoStore(c)
	return savedWiring{
		keeper: c,
		options: []server.Option{
			server.WithSQLConnections(keeper.NewSQLStore(c)),
			server.WithHistory(mongo),
			server.WithKeeper(&server.KeeperHub{Client: c, Migration: &keeper.Migration{Legacy: legacy, Client: c}, OldCases: oldCaseCounter(getenv)}),
		},
		bb: keeper.NewBBStore(c),
	}
}

// caseFolderName is a case's folder in a case root: its id.
var caseFolderName = regexp.MustCompile(`^[a-z2-7]{26}$`)

// oldCaseCounter counts the person's cases in their own (per-user) case folder, which the shared layout does not open. It only lists
// folder names: nothing is opened or unsealed.
func oldCaseCounter(getenv func(string) string) func() int {
	root, err := casesRoot(getenv)
	if err != nil {
		return nil
	}
	return func() int {
		entries, err := os.ReadDir(root)
		if err != nil {
			return 0
		}
		n := 0
		for _, e := range entries {
			if e.IsDir() && caseFolderName.MatchString(e.Name()) {
				n++
			}
		}
		return n
	}
}

// legacyStores opens the person's old per-user saved files, which only the move reads. A file that cannot be opened is left out.
func legacyStores(getenv func(string) string) keeper.Legacy {
	var l keeper.Legacy
	if s, err := sqlconn.NewDefault(getenv); err == nil {
		l.SQL = s
	} else {
		log.Printf("Old saved SQL connections not offered for the move: %v", err)
	}
	if s, err := history.NewDefault(getenv); err == nil {
		l.Mongo = s
	} else {
		log.Printf("Old saved MongoDB connections not offered for the move: %v", err)
	}
	if s, err := bbsaved.NewDefault(getenv); err == nil {
		l.BB = s
	} else {
		log.Printf("Old saved Blackbox list not offered for the move: %v", err)
	}
	return l
}
