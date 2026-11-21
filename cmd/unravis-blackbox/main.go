// Command unravis-blackbox is the Unravis Blackbox recorder. It samples CPU,
// memory, disk, network and process load every few seconds and keeps the last
// days on this server, so the evidence exists when something goes wrong. It only
// reads the machine it runs on and never changes anything but its own folder.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
  apply       Save the settings to the installed blackbox.json and restart
              the service. A stopped service keeps them for its next start.
  update      Replace the installed recorder program with this one and
              restart the service if it was running. Settings, key and
              recordings are kept; the old program is kept as .old.
  remove      Stop and remove the service. The recordings stay.
              --delete-recordings --confirm-name <name>  also deletes them.
  install-sql Install the SQL service (UnravisBlackboxSql) under its own
              virtual account, STOPPED, then print the grant to run in SSMS.
              The Blackbox never runs the grant for you. After the grant,
              activate the service with enable-sql.
  enable-sql  Activate the installed SQL service after the owner grant:
              confirms the installed settings and folder, clears the
              persisted off latch (owner-only) and starts the service.
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
	// verifyConfig proves the settings file is administrator-locked (owner and
	// access list). The SQL service runs it at every start before it reads the
	// file (BL-140). It is the file-only proof: the service account cannot read
	// folder access lists, and the folder chain is proven by an administrator at
	// install-sql and enable-sql. realDeps binds the real proof; a nil value
	// (tests) skips it.
	verifyConfig func(path string) error
	// elev is the Windows elevate helper. No command here may use it (C14):
	// this program IS the helper, and the SQL grant is run by the owner in
	// SSMS. It stays nil; tests inject a fake to record that it is never called.
	elev blackbox.Elevator
}

func realDeps() deps {
	disk := blackbox.OSDisk{}
	sqlMgr := &blackbox.SQLManager{Svc: blackbox.WinService{}, Env: blackbox.OSEnv{}}
	return deps{
		verifyConfig: func(path string) error { return blackbox.OSEnv{}.VerifySettingsFileForStart(path) },
		// remove also removes the SQL service (E2E-10), through the same SQLManager.
		mgr: &blackbox.Manager{Svc: blackbox.WinService{}, Env: blackbox.OSEnv{}, Disk: disk, SQL: sqlMgr},
		// Pinned (wave 7): the SQLManager talks to blackbox.WinService DIRECTLY.
		// internal/setup's bbSvc adapter drops Account/SIDUnrestricted and would
		// create the SQL service as LocalSystem. RevokeScript stays empty: the
		// remove-sql command prints the script through the answer writer.
		sqlMgr: sqlMgr,
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

// reasonOf maps an error to its fixed reason code (decision A39). It never
// reads error text into the reason.
func reasonOf(err error) string {
	var ie *blackbox.InstallError
	var ae *blackbox.ApplyError
	var se *blackbox.SnapshotError
	var re *blackbox.RemoveError
	var ue *blackbox.UpdateError
	switch {
	case errors.As(err, &ue):
		return string(ue.Reason)
	case errors.As(err, &re):
		return re.Reason
	case errors.As(err, &ie):
		return string(ie.Reason)
	case errors.As(err, &ae):
		return string(ae.Reason)
	case errors.As(err, &se):
		return string(se.Reason)
	case errors.Is(err, blackbox.ErrNotAdmin):
		return string(blackbox.ReasonNoAdmin)
	case errors.Is(err, blackbox.ErrNotInstalled):
		return "not-installed"
	case errors.Is(err, blackbox.ErrNotSQLInstalled):
		return "not-installed"
	case errors.Is(err, blackbox.ErrConfirmName):
		return "confirm-name"
	case errors.Is(err, blackbox.ErrInvalidConfig):
		return "invalid-config"
	}
	return "other"
}

func (a answer) fail(err error) int {
	reason := reasonOf(err)
	if a.asJSON {
		_ = json.NewEncoder(a.out).Encode(map[string]any{"ok": false, "reason": reason, "message": err.Error()})
	} else {
		fmt.Fprintln(a.err, err.Error())
	}
	return exitFail
}

// readCheckedConfig reads the settings file ONCE and refuses it unless its
// SHA-256 is the one Unravis checked before it asked for administrator rights.
// The caller parses the bytes returned here, never the file again (SEC-FIX-08).
func readCheckedConfig(path, want string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("the settings file could not be read: %w", blackbox.ErrInvalidConfig)
	}
	sum := sha256.Sum256(data)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), strings.TrimSpace(want)) {
		return nil, fmt.Errorf("the settings file changed after it was checked, so nothing was changed. Press the button again: %w", blackbox.ErrInvalidConfig)
	}
	return data, nil
}

// checkConfigHash is readCheckedConfig for a caller that only needs the verdict.
func checkConfigHash(path, want string) error {
	_, err := readCheckedConfig(path, want)
	return err
}

// withBindWarning adds the unsafe-listen-address warning to an answer.
func withBindWarning(bind string, fields map[string]any) map[string]any {
	if w := blackbox.BindWarning(bind); w != "" {
		fields["bindWarning"] = w
	}
	return fields
}

func bindWarningText(bind string) string {
	if w := blackbox.BindWarning(bind); w != "" {
		return " Warning: " + w
	}
	return ""
}

// sqlReasonSentences are the ONLY messages a SQL command answer may carry
// (W8-07): one fixed sentence per reason, never err.Error(), so no path,
// configuration text or OS/driver prose reaches --json or the terminal log.
var sqlReasonSentences = map[string]string{
	string(blackbox.ReasonNoAdmin): "This account cannot approve administrator changes. Sign in as an administrator and try again.",
	"not-installed":                "The service is not installed here. Install the Blackbox first.",
	"confirm-name":                 "Type the Blackbox name exactly to confirm (--confirm-name).",
	"invalid-config":               "The settings file is not valid. Fix it and try again.",
	string(blackbox.ReasonExists):  "The SQL service is already installed. Remove it first (remove-sql).",
	string(blackbox.ReasonFolder):  "The SQL data folder could not be made and locked for the SQL service only.",
	"other":                        "The SQL command could not be completed.",
}

// failSQL answers a SQL command failure with a fixed reason and a fixed
// sentence. The underlying error (which may name paths or carry OS text) is
// deliberately dropped at this boundary (W8-07, C10).
func (a answer) failSQL(err error) int {
	reason := reasonOf(err)
	msg, ok := sqlReasonSentences[reason]
	if !ok {
		msg = sqlReasonSentences["other"]
	}
	if a.asJSON {
		_ = json.NewEncoder(a.out).Encode(map[string]any{"ok": false, "reason": reason, "message": msg})
	} else {
		fmt.Fprintln(a.err, msg)
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
	answerFile := fs.String(strings.TrimPrefix(blackbox.AnswerFileFlag, "--"), "", "write the answer to this file instead of the console (set by Unravis)")
	expectSHA := fs.String("expect-sha256", "", "SHA-256 the new program must have (update)")
	configSHA := fs.String("config-sha256", "", "SHA-256 of the settings file Unravis checked (install, apply)")
	if err := fs.Parse(rest); err != nil {
		return exitUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "unexpected argument %q\n", fs.Arg(0))
		return exitUsage
	}
	if *answerFile != "" {
		// Unravis starts this program elevated and directly, so the answer cannot
		// come back on the console. Only the owner and administrators can read it.
		// SEC-FIX-04: Unravis made this file before the prompt, so it is opened
		// as it is (never created) and a link is refused.
		f, err := blackbox.OpenAnswerFile(*answerFile)
		if err != nil {
			fmt.Fprintln(stderr, "cannot write the answer file: Unravis makes it before this program starts. Press the button in Unravis again")
			return exitFail
		}
		defer f.Close()
		stdout = f
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
		if cmd == "apply" || cmd == "remove" {
			// SEC-15: these run elevated from the app, whose folder an ordinary
			// user can write to on a per-user install. Never take settings or a
			// key file from there: use the installed copy (which may be missing,
			// and then the command says so) and nothing else.
			if progDir != "" {
				return filepath.Join(progDir, name)
			}
			return ""
		}
		return filepath.Join(d.exeDir(), name)
	}
	paths.ConfigPath = pick(paths.ConfigPath, defaultConfigName)
	paths.KeyPath = pick(paths.KeyPath, defaultKeyName)
	// W8-09: the SQL commands bind the config path into the service registry,
	// so it must be resolved to an absolute canonical path HERE — under the
	// installer's working directory — before both loading and registration. A
	// relative path would resolve against the SCM's different working
	// directory at service startup and read a different (or no) file.
	switch cmd {
	case "install-sql", "remove-sql", "enable-sql":
		if abs, err := filepath.Abs(paths.ConfigPath); err == nil {
			paths.ConfigPath = abs
		}
	}

	// cfgData is the one read of the settings file for install and apply.
	var cfgData []byte
	if cmd == "install" || cmd == "apply" {
		switch {
		case *configSHA != "":
			// SEC-05: the settings Unravis checked and showed are the ones used. A
			// file rewritten while the permission prompt was open is refused, and
			// the bytes hashed here are the bytes parsed below (SEC-FIX-08).
			b, err := readCheckedConfig(paths.ConfigPath, *configSHA)
			if err != nil {
				return ans.fail(err)
			}
			cfgData = b
		case *answerFile != "":
			// Unravis always sends the hash; a call that runs elevated for it
			// without one is not from Unravis, so it is not trusted to read a file.
			return ans.fail(fmt.Errorf("the settings were not checked first, so nothing was changed. Press the button in Unravis again: %w", blackbox.ErrInvalidConfig))
		}
	}

	switch cmd {
	case "install":
		cfg, err := installConfig(paths.ConfigPath, cfgData, d, *name, *folder, *port, *bind)
		if err != nil {
			return ans.fail(err)
		}
		res, err := d.mgr.Install(cfg)
		if err != nil {
			return ans.fail(err)
		}
		if *answerFile != "" {
			// Installed from Unravis: it updates this recorder together with itself (blackbox.ManagedMarkerName).
			_ = blackbox.MarkManaged(filepath.Dir(res.ExePath))
		}
		return ans.ok(fmt.Sprintf("Installed. The %s service is running.\nFolder: %s\nProgram and settings: %s\nStatus port: %d\n\nRead key (shown once, copy it now):\n  %s\n\nOnly a fingerprint of the key is kept on this server.",
			res.Service, res.Folder, filepath.Dir(res.ExePath), res.Port, res.ReadKey)+bindWarningText(cfg.Bind),
			withBindWarning(cfg.Bind, map[string]any{"readKey": res.ReadKey, "service": res.Service, "folder": res.Folder, "port": res.Port, "name": cfg.Name,
				"exePath": res.ExePath, "configPath": res.ConfigPath}))
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
		var res blackbox.ApplyResult
		var err error
		bind := ""
		if cfgData != nil {
			cfg, perr := blackbox.ParseConfig(cfgData)
			if perr != nil {
				return ans.fail(perr)
			}
			bind = cfg.Bind
			res, err = d.mgr.ApplyConfig(cfg)
		} else {
			res, err = d.mgr.Apply(paths.ConfigPath)
			if c, lerr := blackbox.LoadConfig(paths.ConfigPath); lerr == nil {
				bind = c.Bind
			}
		}
		if err != nil {
			return ans.fail(err)
		}
		sqlNote := ""
		if res.SQLSettingsUnlocked {
			sqlNote = " " + blackbox.SQLSettingsUnlockedText
		}
		if !res.Restarted {
			return ans.ok("Settings saved. The Blackbox is stopped: they apply at its next start."+sqlNote+bindWarningText(bind), withBindWarning(bind, map[string]any{"restarted": false, "sqlSettingsUnlocked": res.SQLSettingsUnlocked}))
		}
		return ans.ok("Settings applied. The Blackbox restarted."+sqlNote+bindWarningText(bind), withBindWarning(bind, map[string]any{"restarted": true, "sqlSettingsUnlocked": res.SQLSettingsUnlocked}))
	case "update":
		d.mgr.ExpectSHA256 = *expectSHA
		res, err := d.mgr.Update()
		if err != nil {
			return ans.fail(err)
		}
		if *answerFile != "" {
			// Updated from Unravis: it updates this recorder together with itself (blackbox.ManagedMarkerName).
			if progDir, perr := d.mgr.Env.ProgramDir(); perr == nil {
				_ = blackbox.MarkManaged(progDir)
			}
		}
		text := "The recorder is already up to date. Nothing was stopped or restarted."
		switch {
		case res.Updated && res.Restarted:
			text = "The recorder was updated and restarted. Settings, key and recordings were not touched."
		case res.Updated:
			text = "The recorder was updated. It was stopped, so it stays stopped until you start it."
		}
		return ans.ok(text, map[string]any{"updated": res.Updated, "wasRunning": res.WasRunning, "restarted": res.Restarted,
			"oldHash": res.OldHash, "newHash": res.NewHash, "version": version})
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
		if res.SQLServiceRemoved {
			text += " The SQL service was removed too."
		}
		extra := map[string]any{"serviceRemoved": res.ServiceRemoved, "recordingsDeleted": res.RecordingsDeleted, "filesDeleted": res.FilesDeleted, "sqlServiceRemoved": res.SQLServiceRemoved}
		if res.SQLNote != "" {
			extra["note"] = res.SQLNote
		}
		return ans.ok(text, extra)
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
		// Creates the service STOPPED and PRINTS the grant; it never runs SQL,
		// never grants and never calls the elevate helper (C14). The owner runs
		// the grant in SSMS and then activates with enable-sql (W8-03): starting
		// before the grant would latch a denial that survives restarts. The
		// service is bound to the exact absolute config validated here, so it
		// locks and uses the same folder (BBX-5-02, W8-09).
		cfg, err := loadInstalledConfig(paths.ConfigPath)
		if err != nil {
			return ans.failSQL(err)
		}
		if err := d.sqlMgr.InstallSQL(cfg, paths.ConfigPath); err != nil {
			return ans.failSQL(err)
		}
		return ans.ok("The "+blackbox.SQLServiceName+" service is installed and stopped.\n\n1. Run this grant in SSMS as an admin (the Blackbox never runs it for you):\n"+grantScript+"\n\n2. Then activate the service:\n  unravis-blackbox enable-sql",
			map[string]any{"service": blackbox.SQLServiceName, "account": blackbox.SQLServiceAccount, "grantScript": grantScript, "started": false})
	case "enable-sql":
		// Administrator-only owner activation (W8-03): confirms the installed
		// settings/folder, durably clears only the persisted off latch, and
		// starts/checks the service. Never runs SQL or the grant itself.
		cfg, err := loadInstalledConfig(paths.ConfigPath)
		if err != nil {
			return ans.failSQL(err)
		}
		if err := d.sqlMgr.EnableSQL(cfg, paths.ConfigPath); err != nil {
			return ans.failSQL(err)
		}
		return ans.ok("The "+blackbox.SQLServiceName+" service is enabled and running. It samples only while the rights ceiling passes.",
			map[string]any{"service": blackbox.SQLServiceName, "started": true})
	case "remove-sql":
		cfg, err := loadInstalledConfig(paths.ConfigPath)
		if err != nil {
			return ans.failSQL(err)
		}
		if err := d.sqlMgr.RemoveSQL(cfg, *confirm); err != nil {
			return ans.failSQL(err)
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
func installConfig(path string, data []byte, d deps, name, folder string, port int, bind string) (blackbox.Config, error) {
	var cfg blackbox.Config
	var err error
	if data != nil {
		cfg, err = blackbox.ParseConfigDraft(data)
	} else {
		cfg, err = blackbox.LoadConfigDraft(path)
	}
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

// errSQLSettingsNotLocked is the fixed sentence for a start the proof refused.
var errSQLSettingsNotLocked = errors.New("The SQL service did not start: its settings file is not locked to administrators. Run enable-sql again from an elevated prompt.")

// runSQLService loads the settings and runs the SQL runner until ctx is done.
// It never starts the host recorder or its status page: no HTTP in this mode.
// A clean stop publishes the off status (W8-04) so the host never keeps
// advertising an old enabled:true record after the service ends.
func runSQLService(ctx context.Context, p blackbox.Paths, d deps, logf func(string, ...any)) error {
	// BL-140: the service reads this file with its own rights at every start, so the
	// file proof is repeated here, not only at install-sql and enable-sql. A file
	// swapped in by an ordinary user is not owned by an administrator and stops the start.
	if d.verifyConfig != nil {
		if verr := d.verifyConfig(p.ConfigPath); verr != nil {
			if logf != nil {
				logf("sql: settings-not-locked")
			}
			return errSQLSettingsNotLocked
		}
	}
	cfg, err := blackbox.LoadConfig(p.ConfigPath)
	if err != nil {
		return err
	}
	err = d.sqlRun(ctx, sqlRunOptions{Config: cfg, Dir: blackbox.SQLDataDir(cfg.Folder), Log: logf})
	if err == nil {
		if perr := blackbox.PublishSQLStopped(cfg.Folder); perr != nil && logf != nil {
			logf("sql: status-off")
		}
	}
	return err
}

// runSQLForeground is the console form of the hidden sql-service command. It is
// DIAGNOSTIC wiring only (W10-11): production sampling runs exclusively inside
// the UnravisBlackboxSql service under its own virtual account, and the real
// runner refuses any other identity through the same folder/identity controls
// (bbsql.CheckFolderAccess). It serves no HTTP and uses the same fixed-sentence
// answer boundary as the other SQL commands.
func runSQLForeground(p blackbox.Paths, d deps, ans answer, stderr io.Writer) int {
	fmt.Fprintln(stderr, "sql-service console mode is diagnostic only; production runs as the UnravisBlackboxSql service.")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err := runSQLService(ctx, p, d, func(f string, a ...any) { fmt.Fprintf(stderr, f+"\n", a...) })
	if err != nil {
		return ans.failSQL(err)
	}
	return exitOK
}
