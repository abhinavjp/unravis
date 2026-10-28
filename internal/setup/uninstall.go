package setup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"api-audit-explorer/internal/blackbox"
	"api-audit-explorer/internal/keeper"
)

// An uninstall is not journalled: removing is what it is for, and a half-done uninstall is finished by running it again. It goes on
// past a failed step and lists what it could not remove. Data is removed only when the person asked for it and typed the confirmation;
// shared data is never removed without that.

type uninstallState struct {
	left []string
	kept []string
}

func uninstallSteps(p *Plan) []Step {
	steps := []Step{{ID: "close-app", Title: "Close Unravis", Do: p.closeApp}}
	if p.Opts.Blackbox != nil || p.blackboxPresent() {
		steps = append(steps, Step{ID: "blackbox", Title: "Remove the Blackbox recorder", Do: p.removeBlackbox})
	}
	if p.Opts.Scope == ScopeMachine {
		steps = append(steps, Step{ID: "keeper-service", Title: "Remove the key keeper service", Do: p.removeKeeper})
	}
	steps = append(steps,
		Step{ID: "shortcuts", Title: "Remove the shortcuts", Do: p.removeShortcuts},
		Step{ID: "arp", Title: "Remove Unravis from Installed apps", Do: p.removeARP},
		Step{ID: "program-files", Title: "Remove the program files", Do: p.removeProgramFiles},
		Step{ID: "data", Title: "Cases and saved connections", Do: p.removeData},
	)
	return steps
}

func (p *Plan) un() *uninstallState {
	if p.unState == nil {
		p.unState = &uninstallState{}
	}
	return p.unState
}

func (p *Plan) leftf(format string, a ...any) { p.un().left = append(p.un().left, fmt.Sprintf(format, a...)) }

func (p *Plan) blackboxPresent() bool {
	if st, err := p.r.Sys.Svc.State(blackbox.ServiceName); err == nil && st != blackbox.SvcNotInstalled {
		return true
	}
	_, cfg, _ := p.Layout.BlackboxPaths()
	_, err := os.Stat(cfg)
	return err == nil
}

// checkUninstallConfirmations refuses a deletion without its typed confirmation, before anything is removed.
func checkUninstallConfirmations(p *Plan) error {
	u := p.Uninst
	if u.DeleteCases && u.ConfirmCases != ConfirmDeleteWord {
		return problem("confirmCases", "To delete the cases and saved connections, type %s.", ConfirmDeleteWord)
	}
	if u.DeleteRecordings {
		name := p.blackboxName()
		if name == "" {
			return problem("confirmRecordings", "No Blackbox recordings were found to delete.")
		}
		if u.ConfirmRecordings != name {
			return problem("confirmRecordings", "To delete the Blackbox recordings, type the Blackbox name exactly: %s.", name)
		}
	}
	return nil
}

// blackboxConfig is the installed Blackbox's settings: blackbox.json, else what setup-state.json says.
func (p *Plan) blackboxConfig() (blackbox.Config, bool) {
	_, cfgPath, _ := p.Layout.BlackboxPaths()
	if c, err := blackbox.LoadConfig(cfgPath); err == nil {
		return c, true
	}
	return p.Opts.BlackboxConfig()
}

func (p *Plan) blackboxName() string {
	c, ok := p.blackboxConfig()
	if !ok {
		return ""
	}
	return c.Name
}

func (p *Plan) removeBlackbox(ctx context.Context, t *Txn) error {
	exe, cfgPath, keyPath := p.Layout.BlackboxPaths()
	cfg, haveCfg := p.blackboxConfig()
	m := &blackbox.Manager{Svc: bbSvc{p.r.Sys.Svc}, Env: bbEnv{p: p, exe: exe}, Disk: p.r.Sys.Disk}
	del := p.Uninst.DeleteRecordings && haveCfg
	if _, err := m.Remove(cfg, keyPath, del, p.Uninst.ConfirmRecordings); err != nil && !errors.Is(err, blackbox.ErrNotInstalled) {
		p.leftf("Blackbox service %s (%v)", blackbox.ServiceName, err)
	}
	for _, f := range []string{keyPath, cfgPath, cfgPath + ".bak", exe} {
		if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
			p.leftf("File %s (%v)", f, err)
		}
	}
	removeIfEmpty(p.Layout.BlackboxDir)
	if haveCfg && !del {
		if _, err := os.Stat(cfg.Folder); err == nil {
			p.un().kept = append(p.un().kept, "Blackbox recordings in "+cfg.Folder)
		}
	}
	// The firewall rule belongs to the Blackbox.
	name := p.Old.State.FirewallRule
	if name == "" && haveCfg {
		name = FirewallRuleName(cfg.Port)
	}
	if name != "" {
		if _, err := p.r.Sys.FW.Get(name); err == nil {
			if err := p.r.Sys.FW.Remove(name); err != nil {
				p.leftf("Firewall rule %s (%v)", name, err)
			}
		}
	}
	return nil
}

func (p *Plan) removeKeeper(ctx context.Context, t *Txn) error {
	name := keeper.ServiceName
	st, err := p.r.Sys.Svc.State(name)
	if err != nil {
		p.leftf("Service %s (%v)", name, err)
		return nil
	}
	if st == blackbox.SvcNotInstalled {
		return nil
	}
	if _, err := p.r.Sys.stopService(name); err != nil {
		p.leftf("Service %s (%v)", name, err)
		return nil
	}
	if err := p.r.Sys.Svc.Delete(name); err != nil {
		p.leftf("Service %s (%v)", name, err)
	}
	return nil
}

func (p *Plan) removeShortcuts(ctx context.Context, t *Txn) error {
	for _, l := range []string{p.Layout.StartMenuLink, p.Layout.StartupLink} {
		if err := os.Remove(l); err != nil && !errors.Is(err, os.ErrNotExist) {
			p.leftf("Shortcut %s (%v)", l, err)
		}
	}
	return nil
}

func (p *Plan) removeARP(ctx context.Context, t *Txn) error {
	if err := p.r.Sys.Reg.DeleteKey(p.Layout.ARPRoot, ARPKey); err != nil {
		p.leftf("Registry key %s\\%s (%v)", p.Layout.ARPRoot, ARPKey, err)
	}
	return nil
}

func (p *Plan) removeProgramFiles(ctx context.Context, t *Txn) error {
	dir := p.Layout.InstallDir
	for _, f := range []string{p.Layout.AppExe, p.Layout.KeeperExe, p.Layout.StateFile} {
		if f == "" {
			continue
		}
		if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
			p.leftf("File %s (%v)", f, err)
		}
	}
	// The setup program removes its own copy at the next restart when it runs from there (it normally runs from a temporary copy).
	if err := os.Remove(p.Layout.SetupExe); err != nil && !errors.Is(err, os.ErrNotExist) {
		if p.r.Sys.DeleteLater != nil && p.r.Sys.DeleteLater(p.Layout.SetupExe) == nil {
			if p.r.Sys.DeleteLater(dir) != nil {
				p.leftf("Folder %s (removed at the next restart)", dir)
			}
		} else {
			p.leftf("File %s (%v)", p.Layout.SetupExe, err)
		}
	}
	if ents, err := os.ReadDir(dir); err == nil {
		for _, e := range ents {
			if e.IsDir() && (strings.HasPrefix(e.Name(), ".setup-staging-") || strings.HasPrefix(e.Name(), ".setup-backup-")) {
				_ = os.RemoveAll(filepath.Join(dir, e.Name()))
			}
		}
	}
	if err := os.Remove(dir); err != nil && !errors.Is(err, os.ErrNotExist) {
		if ents, rerr := os.ReadDir(dir); rerr == nil && len(ents) > 0 && !onlySetupExe(ents) {
			p.un().kept = append(p.un().kept, "Files you added to "+dir)
		}
	}
	removeIfEmpty(MachineJournalDir(p.r.Dirs))
	return nil
}

func onlySetupExe(ents []os.DirEntry) bool {
	return len(ents) == 1 && strings.EqualFold(ents[0].Name(), SetupExeName)
}

// removeData deletes the cases and saved connections only on the person's typed yes. For everyone on this PC that is the shared folder
// with every person's cases, and its record in the registry; without the yes both stay, so a later install finds them as they were.
func (p *Plan) removeData(ctx context.Context, t *Txn) error {
	u := p.Uninst
	del := u.DeleteCases && u.ConfirmCases == ConfirmDeleteWord
	if p.Opts.Scope == ScopeMachine {
		root := p.Layout.SharedRoot
		if root == "" {
			return nil
		}
		_, statErr := os.Stat(root)
		if !del {
			if statErr == nil {
				p.un().kept = append(p.un().kept, "Cases and saved connections of everyone on this PC in "+root)
			}
		} else {
			if statErr == nil {
				if err := os.RemoveAll(root); err != nil {
					p.leftf("Folder %s (%v)", root, err)
				}
			}
			if err := p.r.Sys.Reg.DeleteKey(HKLM, AnchorKey); err != nil {
				p.leftf("Registry key HKLM\\%s (%v)", AnchorKey, err)
			}
		}
		// Cases from before the shared setup stay in the person's own folder; they are never removed by an uninstall for everyone.
		if older := filepath.Join(p.r.Dirs.LocalAppData, "APIAuditExplorer"); p.r.Dirs.LocalAppData != "" {
			if _, err := os.Stat(older); err == nil {
				p.un().kept = append(p.un().kept, "Older cases in your own folder "+older)
			}
		}
		return nil
	}
	for _, d := range p.Layout.UserData {
		if _, err := os.Stat(d); err != nil {
			continue
		}
		if !del {
			p.un().kept = append(p.un().kept, "Your cases and saved connections in "+d)
			continue
		}
		if err := os.RemoveAll(d); err != nil {
			p.leftf("Folder %s (%v)", d, err)
		}
	}
	return nil
}

func removeIfEmpty(dir string) {
	if dir != "" {
		_ = os.Remove(dir)
	}
}

// uninstall runs the uninstall steps. It stops before removing anything when Unravis is open without the person's yes to closing it,
// or a deletion lacks its typed confirmation.
func (r *Runner) uninstall(ctx context.Context, p *Plan, res Result) Result {
	if err := checkUninstallConfirmations(p); err != nil {
		res.Outcome = OutcomeInvalid
		res.Message = plainError(err) + " Nothing was removed."
		r.logf("uninstall: %v; nothing removed", err)
		return res
	}
	r.logf("start uninstall %s (scope %s, folder %s, delete cases %t, delete recordings %t)", p.Version, p.Opts.Scope, p.Layout.InstallDir,
		p.Uninst.DeleteCases, p.Uninst.DeleteRecordings)
	t := &Txn{sys: r.Sys, log: func(s string) { r.logf("%s", s) }}
	for _, s := range p.Steps {
		r.emit(StepEvent{ID: s.ID, Title: s.Title, State: StepWaiting})
	}
	for i, s := range p.Steps {
		if err := ctx.Err(); err != nil && i == 0 {
			res.Outcome = OutcomeCancelled
			res.Message = "Cancelled. Nothing was removed."
			return res
		}
		t.step = s.ID
		r.emit(StepEvent{ID: s.ID, Title: s.Title, State: StepRunning})
		before := len(p.un().left)
		err := s.Do(ctx, t)
		if err != nil && s.ID == "close-app" {
			r.emit(StepEvent{ID: s.ID, Title: s.Title, State: StepFailed, Detail: plainError(err)})
			res.Outcome = OutcomePreflightFailed
			res.Failed = s.Title
			res.Message = plainError(err) + " Nothing was removed."
			res.Fix = fixFor(err)
			r.logf("uninstall: %s: %v; nothing removed", s.ID, err)
			return res
		}
		if err != nil {
			p.leftf("%s (%v)", s.Title, err)
		}
		if len(p.un().left) > before {
			r.logf("%s: some items left: %s", s.ID, strings.Join(p.un().left[before:], "; "))
			r.emit(StepEvent{ID: s.ID, Title: s.Title, State: StepLeft})
			continue
		}
		r.logf("%s: ok", s.ID)
		r.emit(StepEvent{ID: s.ID, Title: s.Title, State: StepDone})
	}
	res.Left = p.un().left
	res.Kept = p.un().kept
	if len(res.Left) > 0 {
		res.Outcome = OutcomeLeftovers
		res.Message = "Unravis is uninstalled, but some items could not be removed."
		r.logf("uninstall done with %d item(s) left", len(res.Left))
	} else {
		res.Outcome = OutcomeDone
		res.Message = "Unravis is uninstalled."
		r.logf("uninstall done")
	}
	for _, k := range res.Kept {
		r.logf("kept: %s", k)
	}
	return res
}
