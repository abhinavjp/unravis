// Command unravis-blackbox is the Unravis Blackbox recorder. It samples CPU,
// memory, disk, network and process load every few seconds and keeps the last
// days on this server, so the evidence exists when something goes wrong. It only
// reads the machine it runs on and never changes anything but its own folder.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"api-audit-explorer/internal/blackbox"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const (
	exitOK    = 0
	exitFail  = 1
	exitUsage = 2

	defaultConfigName = "blackbox.json"
	defaultKeyName    = "readkey.sha256"
)

const usageText = `Unravis Blackbox ` + "%s" + `

Usage: unravis-blackbox <command> [options]

Commands that change the service (run as administrator):
  install     Install and start the recorder as the UnravisBlackbox service.
              Prints the read key once. Options: --name --folder --port --bind
  start       Start the service.
  stop        Stop the service. The current run is closed cleanly.
  apply       Read blackbox.json again and restart the service.
  remove      Stop and remove the service. The recordings stay.
              --delete-recordings --confirm-name <name>  also deletes them.
  install-sql Install and start the SQL service (UnravisBlackboxSql) under its
              own virtual account, then print the grant to run in SSMS.
              The Blackbox never runs the grant for you.
  remove-sql  Stop and remove the SQL service, then print the revoke script.
              --confirm-name <name>  the Blackbox name, required.

Other commands:
  snapshot    Copy the recordings into <folder>\snapshots\<name>-<date>-<time>.
  run         Record in this window instead of as a service (Ctrl+C to stop).
  version     Print the version.

Options for every command:
  --config <file>     blackbox.json (default: the installed copy, else next to this program)
  --key-file <file>   read key hash (default: the installed copy, else next to this program)
  --json              answer as one JSON line (for the Unravis installer)
`

// deps is everything the commands reach out to, so tests can replace it.
type deps struct {
	mgr      *blackbox.Manager
	exeDir   func() string
	hostname func() string
	now      func() time.Time
	disk     blackbox.DiskSpace
	hostRun  func(ctx context.Context, o blackbox.HostOptions) error
	snapOpt  blackbox.SnapshotOptions
	driveOK  func(root string) bool
	sqlMgr   *blackbox.SQLManager
	// sqlRun is the SQL runner of the sql-service mode (BBX-5-10). Tests inject
	// a fake; realDeps binds the real runner in sqlrunner.go.
	sqlRun func(ctx context.Context, o sqlRunOptions) error
	// elev is the Windows elevate helper. No command here may use it (C14):
	// this program IS the helper, and the SQL grant is run by the owner in
	// SSMS. It stays nil; tests inject a fake to record that it is never called.
	elev blackbox.Elevator
}

func realDeps() deps {
	disk := blackbox.OSDisk{}
	return deps{
		mgr: &blackbox.Manager{Svc: blackbox.WinService{}, Env: blackbox.OSEnv{}, Disk: disk},
		// Pinned (wave 7): the SQLManager talks to blackbox.WinService DIRECTLY.
		// internal/setup's bbSvc adapter drops Account/SIDUnrestricted and would
		// create the SQL service as LocalSystem. RevokeScript stays empty: the
		// remove-sql command prints the script through the answer writer.
		sqlMgr: &blackbox.SQLManager{Svc: blackbox.WinService{}, Env: blackbox.OSEnv{}},
		sqlRun: runSQLServiceLoop,
		disk:   disk,
		exeDir: func() string {
			p, err := blackbox.OSEnv{}.ExePath()
			if err != nil {
				return "."
			}
			return filepath.Dir(p)
		},
		hostname: func() string { h, _ := os.Hostname(); return h },
		now:      time.Now,
		hostRun:  blackbox.RunHost,
		snapOpt:  blackbox.SnapshotOptions{Disk: disk},
		driveOK: func(root string) bool {
			_, err := os.Stat(root)
			return err == nil
		},
	}
}

func main() {
	if handled, code := sqlServiceEntry(os.Args[1:], realDeps()); handled {
		os.Exit(code)
	}
	if handled, code := serviceEntry(os.Args[1:], realDeps()); handled {
		os.Exit(code)
	}
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, realDeps()))
}

// answer prints a result as text or, with --json, as one JSON line.
type answer struct {
	out    io.Writer
	err    io.Writer
	asJSON bool
}

func (a answer) ok(text string, fields map[string]any) int {
	if a.asJSON {
		if fields == nil {
			fields = map[string]any{}
		}
		fields["ok"] = true
		_ = json.NewEncoder(a.out).Encode(fields)
		return exitOK
	}
	fmt.Fprintln(a.out, text)
	return exitOK
}

func (a answer) fail(err error) int {
	reason := "other"
	var ie *blackbox.InstallError
	var se *blackbox.SnapshotError
	switch {
	case errors.As(err, &ie):
		reason = string(ie.Reason)
	case errors.As(err, &se):
		reason = string(se.Reason)
	case errors.Is(err, blackbox.ErrNotAdmin):
		reason = string(blackbox.ReasonNoAdmin)
	case errors.Is(err, blackbox.ErrNotInstalled):
		reason = "not-installed"
	case errors.Is(err, blackbox.ErrNotSQLInstalled):
		reason = "not-installed"
	case errors.Is(err, blackbox.ErrConfirmName):
		reason = "confirm-name"
	case errors.Is(err, blackbox.ErrInvalidConfig):
		reason = "invalid-config"
	}
	if a.asJSON {
		_ = json.NewEncoder(a.out).Encode(map[string]any{"ok": false, "reason": reason, "message": err.Error()})
	} else {
		fmt.Fprintln(a.err, err.Error())
	}
	return exitFail
}

func run(args []string, stdout, stderr io.Writer, d deps) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintf(stderr, usageText, version)
		if len(args) == 0 {
			return exitUsage
		}
		return exitOK
	}
	cmd, rest := args[0], args[1:]
	if cmd == "version" || cmd == "--version" {
		fmt.Fprintln(stdout, "unravis-blackbox "+version)
		return exitOK
	}
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "blackbox.json")
	keyPath := fs.String("key-file", "", "read key hash file")
	asJSON := fs.Bool("json", false, "answer as JSON")
	name := fs.String("name", "", "Blackbox name (install)")
	folder := fs.String("folder", "", "recordings folder (install)")
	port := fs.Int("port", 0, "status port (install)")
	bind := fs.String("bind", "", "address the status page listens on (install)")
	delRec := fs.Bool("delete-recordings", false, "also delete the recordings (remove)")
	confirm := fs.String("confirm-name", "", "the Blackbox name, to confirm deleting the recordings (remove)")
	if err := fs.Parse(rest); err != nil {
		return exitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "unexpected argument %q\n", fs.Arg(0))
		return exitUsage
	}
	ans := answer{out: stdout, err: stderr, asJSON: *asJSON}
	paths := blackbox.Paths{ConfigPath: *configPath, KeyPath: *keyPath}
	// Install reads its settings from next to this program (the unpacked zip) and
	// puts the live copies in the program folder. Every other command works on
	// the installed copies when there are any, so "apply" from the unpacked zip
	// does not read a file the service never sees.
	progDir := ""
	if cmd != "install" {
		if pd, err := d.mgr.Env.ProgramDir(); err == nil {
			progDir = pd
		}
	}
	pick := func(given, name string) string {
		if given != "" {
			return given
		}
		if progDir != "" {
			if _, err := os.Stat(filepath.Join(progDir, name)); err == nil {
				return filepath.Join(progDir, name)
			}
		}
		return filepath.Join(d.exeDir(), name)
	}
	paths.ConfigPath = pick(paths.ConfigPath, defaultConfigName)
	paths.KeyPath = pick(paths.KeyPath, defaultKeyName)

	switch cmd {
	case "install":
		cfg, err := installConfig(paths.ConfigPath, d, *name, *folder, *port, *bind)
		if err != nil {
			return ans.fail(err)
		}
		res, err := d.mgr.Install(cfg)
		if err != nil {
			return ans.fail(err)
		}
		return ans.ok(fmt.Sprintf("Installed. The %s service is running.\nFolder: %s\nProgram and settings: %s\nStatus port: %d\n\nRead key (shown once, copy it now):\n  %s\n\nOnly a fingerprint of the key is kept on this server.",
			res.Service, res.Folder, filepath.Dir(res.ExePath), res.Port, res.ReadKey),
			map[string]any{"readKey": res.ReadKey, "service": res.Service, "folder": res.Folder, "port": res.Port, "name": cfg.Name,
				"exePath": res.ExePath, "configPath": res.ConfigPath})
	case "start":
		if err := d.mgr.Start(); err != nil {
			return ans.fail(err)
		}
		return ans.ok("The Blackbox is running.", nil)
	case "stop":
		if err := d.mgr.Stop(); err != nil {
			return ans.fail(err)
		}
		return ans.ok("The Blackbox is stopped. Recordings are kept.", nil)
	case "apply":
		res, err := d.mgr.Apply(paths.ConfigPath)
		if err != nil {
			return ans.fail(err)
		}
		if !res.Restarted {
			return ans.ok("Settings saved. The Blackbox is stopped: they apply at its next start.", map[string]any{"restarted": false})
		}
		return ans.ok("Settings applied. The Blackbox restarted.", map[string]any{"restarted": true})
	case "remove":
		cfg, err := blackbox.LoadConfig(paths.ConfigPath)
		if err != nil {
			if *delRec {
				return ans.fail(fmt.Errorf("cannot find which folder to delete: %w", err))
			}
			cfg = blackbox.Default()
		}
		res, err := d.mgr.Remove(cfg, paths.KeyPath, *delRec, *confirm)
		if err != nil {
			return ans.fail(err)
		}
		text := "The service is removed. The recordings are kept in " + cfg.Folder + "."
		if res.RecordingsDeleted {
			text = fmt.Sprintf("The service is removed and %d recording items were deleted.", res.FilesDeleted)
		}
		return ans.ok(text, map[string]any{"serviceRemoved": res.ServiceRemoved, "recordingsDeleted": res.RecordingsDeleted, "filesDeleted": res.FilesDeleted})
	case "snapshot":
		cfg, err := blackbox.LoadConfig(paths.ConfigPath)
		if err != nil {
			return ans.fail(err)
		}
		res, err := blackbox.Snapshot(cfg, d.now(), d.snapOpt)
		if err != nil {
			return ans.fail(err)
		}
		return ans.ok(fmt.Sprintf("Snapshot saved: %s (%s). Recordings: %d files. Log files: %d.", res.Dir, humanBytes(res.Bytes), res.Recordings, res.Logs),
			map[string]any{"dir": res.Dir, "bytes": res.Bytes, "recordings": res.Recordings, "logs": res.Logs})
	case "run":
		return runForeground(paths, d, ans, stderr)
	case "install-sql":
		// Creates the service and PRINTS the grant; it never runs SQL, never
		// grants and never calls the elevate helper (C14). The service is bound
		// to the exact config validated here, so it locks and uses the same
		// folder (BBX-5-02 fix: no hardcoded program-folder config divergence).
		cfg, err := loadInstalledConfig(paths.ConfigPath)
		if err != nil {
			return ans.fail(err)
		}
		if err := d.sqlMgr.InstallSQL(cfg, paths.ConfigPath); err != nil {
			return ans.fail(err)
		}
		return ans.ok("The "+blackbox.SQLServiceName+" service is installed and running.\n\nRun this grant in SSMS as an admin (the Blackbox never runs it for you):\n"+grantScript,
			map[string]any{"service": blackbox.SQLServiceName, "account": blackbox.SQLServiceAccount, "grantScript": grantScript})
	case "remove-sql":
		cfg, err := loadInstalledConfig(paths.ConfigPath)
		if err != nil {
			return ans.fail(err)
		}
		if err := d.sqlMgr.RemoveSQL(cfg, *confirm); err != nil {
			return ans.fail(err)
		}
		return ans.ok("The "+blackbox.SQLServiceName+" service is removed. The SQL data is kept.\n\nRun this revoke in SSMS (the Blackbox never runs it for you):\n"+revokeScript,
			map[string]any{"serviceRemoved": true, "revokeScript": revokeScript})
	case "sql-service":
		return runSQLForeground(paths, d, ans, stderr)
	}
	fmt.Fprintf(stderr, "unknown command %q\n\n", cmd)
	fmt.Fprintf(stderr, usageText, version)
	return exitUsage
}

// installConfig is the settings of an install: the file if there is one, else
// the defaults, then the options given on the command line.
func installConfig(path string, d deps, name, folder string, port int, bind string) (blackbox.Config, error) {
	cfg, err := blackbox.LoadConfigDraft(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return cfg, err
		}
		cfg = blackbox.Default()
	}
	if name != "" {
		cfg.Name = name
	}
	if cfg.Name == "" {
		cfg.Name = safeName(d.hostname())
	}
	if cfg.Folder == blackbox.DefaultFolder {
		// The pack's template names D:. A server with no D: drive gets C:.
		cfg.Folder = blackbox.ChooseFolder(d.driveOK)
	}
	if folder != "" {
		cfg.Folder = folder
	}
	if port != 0 {
		cfg.Port = port
	}
	if bind != "" {
		cfg.Bind = bind
	}
	return cfg, nil
}

// safeName turns a computer name into a Blackbox name.
func safeName(host string) string {
	var b strings.Builder
	for _, r := range host {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteByte('-')
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		return "blackbox"
	}
	if len(s) > 63 {
		s = s[:63]
	}
	return s
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.0f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%d KB", (n+1023)>>10)
}

// runForeground records in this window until Ctrl+C. It is the same code the
// service runs.
func runForeground(p blackbox.Paths, d deps, ans answer, stderr io.Writer) int {
	cfg, err := blackbox.LoadConfig(p.ConfigPath)
	if err != nil {
		return ans.fail(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err = d.hostRun(ctx, blackbox.HostOptions{
		Config: cfg, KeyPath: p.KeyPath, Version: version,
		Log:   func(f string, a ...any) { fmt.Fprintf(stderr, f+"\n", a...) },
		Ready: func(addr string) { fmt.Fprintf(stderr, "status page on %s\n", addr) },
	})
	if err != nil {
		return ans.fail(err)
	}
	return exitOK
}

// loadInstalledConfig reads the installed settings the SQL commands work on.
// Without an installed Blackbox there is no folder to lock: say so plainly.
func loadInstalledConfig(path string) (blackbox.Config, error) {
	cfg, err := blackbox.LoadConfig(path)
	if err != nil && errors.Is(err, os.ErrNotExist) {
		err = blackbox.ErrNotInstalled
	}
	return cfg, err
}

// sqlRunOptions wires one run of the SQL service. Dir is the locked SQL data
// folder (<folder>\sql). There is no listener and no port: the SQL service
// never serves HTTP (C14).
type sqlRunOptions struct {
	Config blackbox.Config
	Dir    string
	Log    func(string, ...any)
}

// runSQLService loads the settings and runs the SQL runner until ctx is done.
// It never starts the host recorder or its status page: no HTTP in this mode.
func runSQLService(ctx context.Context, p blackbox.Paths, d deps, logf func(string, ...any)) error {
	cfg, err := blackbox.LoadConfig(p.ConfigPath)
	if err != nil {
		return err
	}
	return d.sqlRun(ctx, sqlRunOptions{Config: cfg, Dir: blackbox.SQLDataDir(cfg.Folder), Log: logf})
}

// runSQLForeground is the console form of the hidden sql-service command, the
// same code the SQL service runs (Ctrl+C to stop).
func runSQLForeground(p blackbox.Paths, d deps, ans answer, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err := runSQLService(ctx, p, d, func(f string, a ...any) { fmt.Fprintf(stderr, f+"\n", a...) })
	if err != nil {
		return ans.fail(err)
	}
	return exitOK
}
