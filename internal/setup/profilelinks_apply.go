package setup

import "path/filepath"

// The two functions the setup window and silent mode both call (WP-74j), so the two paths cannot drift.

// DeriveStartAtSignIn sets req.StartAtSignIn from what is on this PC. op is the RESOLVED operation (ResolveOp), never the asked one;
// saved is the options of the existing install. For an upgrade or repair the page's value never wins: the saved value is taken, and
// then the real Startup link decides (ours: on; missing or foreign: off). A link that cannot be read keeps the saved value. Other
// operations change nothing.
func (r *Runner) DeriveStartAtSignIn(op Op, saved Options, req *Options) {
	if op != OpUpgrade && op != OpRepair {
		return
	}
	req.StartAtSignIn = saved.StartAtSignIn
	l := NewLayout(saved, r.Dirs)
	if _, _, err := linkTarget(l.StartupLink); err == nil {
		_, ours := linkIsOurs(l.StartupLink, l.AppExe)
		req.StartAtSignIn = ours
	}
}

// ApplyProfileLinks writes or removes the links inside the user's profile after the worker has committed. opts are the options of
// the install: the existing install's for upgrade, repair and uninstall, the request's for a fresh install, with StartAtSignIn set to
// the value the worker received. It does nothing unless the run ended done (or done with leftovers). onLog may be nil.
func (r *Runner) ApplyProfileLinks(opts Options, res *Result, onLog func(string)) {
	if res == nil || (res.Outcome != OutcomeDone && res.Outcome != OutcomeLeftovers) {
		return
	}
	log := func(s string) {
		if onLog != nil {
			onLog(s)
		}
	}
	l := NewLayout(opts, r.Dirs)
	if res.Op == OpUninstall {
		lines := r.RemoveProfileLinks(l)
		res.Left = append(res.Left, lines...)
		if len(lines) > 0 && res.Outcome == OutcomeDone {
			res.Outcome = OutcomeLeftovers
		}
		return
	}
	if res.AppExe != "" {
		l.AppExe = res.AppExe
		l.InstallDir = filepath.Dir(res.AppExe)
	}
	if err := r.WriteProfileLinks(l, opts.StartAtSignIn, onLog); err != nil {
		log("shortcut: " + err.Error())
		res.Left = append(res.Left, "Shortcut ("+err.Error()+")")
	}
}
