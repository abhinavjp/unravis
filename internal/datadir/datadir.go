// Package datadir decides where Unravis keeps its files on this PC (P14).
//
// There are two layouts:
//
//   - Shared (the installed product, owner ruling: "everything shared"): one data folder for every person on the PC,
//     C:\ProgramData\Unravis by default. Cases sit in <root>\cases; the host map, the erased lines, the master-key check and the hypothesis
//     ledger sit in <root>, sealed for this PC (machine-scope DPAPI); the folder ACL set by the installer decides who may read them. Saved
//     passwords, saved MongoDB links and Blackbox read keys are NOT here: they live in the key keeper's own folder (<root>\keeper), sealed
//     for the keeper's service account, and only the keeper opens them.
//   - Per user (developer builds, tests, and a PC where the installer has not created the shared folder): what every earlier phase used,
//     %APPDATA%\APIAuditExplorer and %LOCALAPPDATA%\APIAuditExplorer\cases, sealed for the current user.
//
// Unravis never creates the shared folder itself: the installer does, with its ACL. A folder Unravis made would carry default
// permissions, so the absence of the folder means per-user mode.
package datadir

import (
	"os"
	"path/filepath"
)

// Environment variables. UNRAVIS_DATA_DIR forces shared mode rooted at that folder (tests and the screenshot harness);
// AUDIT_CONFIG_DIR keeps the per-user layout inside one folder, as before.
const (
	EnvDataDir    = "UNRAVIS_DATA_DIR"
	EnvKeeperDir  = "UNRAVIS_KEEPER_DIR"
	EnvKeeperPipe = "UNRAVIS_KEEPER_PIPE"
	EnvConfigDir  = "AUDIT_CONFIG_DIR"
)

// DefaultPipe is the key keeper's named pipe.
const DefaultPipe = `\\.\pipe\UnravisKeeper`

// Mode is the layout in use.
type Mode string

const (
	ModeUser   Mode = "user"
	ModeShared Mode = "shared"
)

// Layout is where each kind of file lives.
type Layout struct {
	Mode Mode
	// Root is the shared data folder ("" in per-user mode).
	Root string
	// Base holds hosts.dat, erased.json, masterkey.dat and the ledger.
	Base string
	// Cases is the case folder root ("" means the store's own default, %LOCALAPPDATA%\APIAuditExplorer\cases).
	Cases string
	// UserDir is the per-user folder: the session file always, and in per-user mode the saved connections too. In shared mode the old
	// per-user saved files found here are what the migration offers to move into the keeper.
	UserDir string
	// KeeperDir is the key keeper's folder (shared mode only).
	KeeperDir string
	// Pipe is the key keeper's pipe name.
	Pipe string
}

// Shared reports whether the shared layout (and so the key keeper) is in use.
func (l Layout) Shared() bool { return l.Mode == ModeShared }

// Resolve picks the layout. programData and userConfig are injected so tests never look at the real folders; exists reports whether a
// folder exists.
func Resolve(getenv func(string) string, programData, userConfig string, exists func(string) bool) Layout {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	pipe := getenv(EnvKeeperPipe)
	if pipe == "" {
		pipe = DefaultPipe
	}
	userDir := ""
	if userConfig != "" {
		userDir = filepath.Join(userConfig, "APIAuditExplorer")
	}
	if cfg := getenv(EnvConfigDir); cfg != "" {
		userDir = cfg
	}
	root := getenv(EnvDataDir)
	if root == "" && getenv(EnvConfigDir) == "" && programData != "" {
		candidate := filepath.Join(programData, "Unravis")
		if exists != nil && exists(candidate) {
			root = candidate
		}
	}
	if root == "" {
		l := Layout{Mode: ModeUser, Base: userDir, UserDir: userDir, Pipe: pipe}
		if cfg := getenv(EnvConfigDir); cfg != "" {
			l.Cases = filepath.Join(cfg, "cases")
		}
		return l
	}
	keeper := getenv(EnvKeeperDir)
	if keeper == "" {
		keeper = filepath.Join(root, "keeper")
	}
	return Layout{Mode: ModeShared, Root: root, Base: root, Cases: filepath.Join(root, "cases"), UserDir: userDir, KeeperDir: keeper, Pipe: pipe}
}

// Default resolves the layout from the real environment.
func Default(getenv func(string) string) Layout {
	userConfig, _ := os.UserConfigDir()
	return Resolve(getenv, os.Getenv("ProgramData"), userConfig, func(p string) bool {
		info, err := os.Stat(p)
		return err == nil && info.IsDir()
	})
}

// KeeperDefault is the keeper's own view: its folder, from UNRAVIS_KEEPER_DIR, else <data folder>\keeper, else
// C:\ProgramData\Unravis\keeper.
func KeeperDefault(getenv func(string) string) (dir, pipe string) {
	if getenv == nil {
		getenv = os.Getenv
	}
	pipe = getenv(EnvKeeperPipe)
	if pipe == "" {
		pipe = DefaultPipe
	}
	if dir = getenv(EnvKeeperDir); dir != "" {
		return dir, pipe
	}
	root := getenv(EnvDataDir)
	if root == "" {
		pd := os.Getenv("ProgramData")
		if pd == "" {
			pd = `C:\ProgramData`
		}
		root = filepath.Join(pd, "Unravis")
	}
	return filepath.Join(root, "keeper"), pipe
}
