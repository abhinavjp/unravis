package setup

import (
	"context"
	"strings"
	"time"
)

// The screenshot build (unravis-setup built with the "capture" tag and started with --capture) serves the setup page with these
// fake backends instead of LocalBackend. Each one replays a fixed run, so every screen, failures included, can be captured the same
// way on every PC. They change nothing on the machine: no registry, no services, no files.

// ScriptedBackend replays one fixed run.
type ScriptedBackend struct {
	St     UIState
	Checks []Check
	// Script is played in order when the person presses Install; Hold then waits (the progress screen) until Cancel.
	Script []StepEvent
	Hold   bool
	Res    Result
	// Pace is the pause between events (0 in tests).
	Pace time.Duration
}

func (b *ScriptedBackend) State() UIState                { return b.St }
func (b *ScriptedBackend) Preflight(WorkRequest) []Check { return b.Checks }
func (b *ScriptedBackend) Changes(req WorkRequest) UIChanges {
	d := captureDirs()
	var old *Installed
	if in := b.St.Installed; in != nil {
		old = &Installed{Version: in.Version, Location: in.Location, State: State{Scope: in.Scope, Location: in.Location}}
		if in.BlackboxName != "" {
			bb := DefaultBlackbox(in.BlackboxName, func(string) bool { return true })
			old.State.Blackbox = &bb
		}
	}
	return DescribeChanges(req, old, d, false)
}

func (b *ScriptedBackend) Run(ctx context.Context, req WorkRequest, onEvent func(StepEvent), onLog func(string)) Result {
	for _, e := range b.Script {
		if b.Pace > 0 {
			select {
			case <-ctx.Done():
			case <-time.After(b.Pace):
			}
		}
		onEvent(e)
		onLog(e.ID + ": " + string(e.State))
	}
	if b.Hold {
		<-ctx.Done()
		// As the engine does on a cancel: the step that was running stops, and every step that finished is put back, last first.
		var done []StepEvent
		for _, e := range b.Script {
			if e.State == StepDone {
				done = append(done, e)
			}
		}
		for i := len(done) - 1; i >= 0; i-- {
			onEvent(ev(done[i].ID, done[i].Title, StepUndone, ""))
		}
		return Result{Outcome: OutcomeCancelled, Op: req.Op, Message: "Cancelled. Everything setup changed has been put back."}
	}
	res := b.Res
	// What the person ticked for deletion is not kept.
	var kept []string
	for _, k := range res.Kept {
		if (req.Uninstall.DeleteCases && strings.HasPrefix(k, "Cases")) || (req.Uninstall.DeleteRecordings && strings.HasPrefix(k, "Blackbox recordings")) {
			continue
		}
		kept = append(kept, k)
	}
	res.Kept = kept
	res.Op = req.Op
	if res.Op == "" {
		res.Op = b.St.Mode
	}
	return res
}

// captureDirs are the folders of a typical PC, so the pictures show real-looking paths.
func captureDirs() Dirs {
	prof := `C:\Users\ana`
	return Dirs{Windows: `C:\Windows`, ProgramFiles: `C:\Program Files`, ProgramData: `C:\ProgramData`,
		CommonPrograms: `C:\ProgramData\Microsoft\Windows\Start Menu\Programs`, Temp: prof + `\AppData\Local\Temp`, UserProfile: prof,
		LocalAppData: prof + `\AppData\Local`, AppData: prof + `\AppData\Roaming`,
		Programs: prof + `\AppData\Roaming\Microsoft\Windows\Start Menu\Programs`,
		Startup:  prof + `\AppData\Roaming\Microsoft\Windows\Start Menu\Programs\Startup`}
}

func ev(id, title string, st StepState, detail string) StepEvent {
	return StepEvent{ID: id, Title: title, State: st, Detail: detail}
}

// CaptureScenarios are the fake backends by name; the page picks one with ?scenario=<name>.
func CaptureScenarios() map[string]UIBackend {
	const version = "1.0.0"
	d := captureDirs()
	logPath := DefaultLogPath(d.LocalAppData, time.Date(2026, 10, 27, 9, 12, 4, 0, time.UTC), false)
	base := UIState{Version: version, Mode: OpInstall, LogPath: logPath, Capture: true,
		Defaults: UIDefaults{UserLocation: DefaultLocation(ScopeUser, d), MachineLocation: DefaultLocation(ScopeMachine, d),
			Blackbox: BlackboxOptions{Name: "web-01", Folder: `D:\UnravisBlackbox`, KeepDays: 7, MaxGB: 2, MinFreePercent: 10, Port: 7788}}}
	ready := []Check{
		{ID: "windows", Title: "Windows", Status: CheckReady, Detail: "Windows 11 64-bit (build 22631)"},
		{ID: "browser", Title: "Browser", Status: CheckReady, Detail: "Microsoft Edge"},
		{ID: "install-folder", Title: "Disk space", Status: CheckReady, Detail: "C: has 41 GB free"},
		{ID: "admin", Title: "Administrator approval", Status: CheckReady, Detail: "You can approve administrator changes when Windows asks"},
		{ID: "reboot", Title: "Restart pending", Status: CheckReady, Detail: "No restart is waiting"},
	}
	userSteps := [][2]string{{"close-app", "Close Unravis"}, {"program-folder", "Make the program folder"}, {"copy-files", "Copy the program files"},
		{"shortcuts", "Add the Start menu shortcut"}, {"arp", "Add Unravis to Installed apps"}, {"state", "Save the setup choices"},
		{"verify", "Check that Unravis starts"}}
	machineSteps := [][2]string{{"close-app", "Close Unravis"}, {"program-folder", "Make the program folder"}, {"copy-files", "Copy the program files"},
		{"shared-folder", "Make the shared data folder"}, {"anchor", "Record the shared folder for Unravis"},
		{"keeper-service", "Set up the key keeper service"}, {"blackbox", "Set up the Blackbox recorder"},
		{"shortcuts", "Add the Start menu shortcut"}, {"arp", "Add Unravis to Installed apps"}, {"state", "Save the setup choices"},
		{"verify", "Check that Unravis starts"}}
	all := func(steps [][2]string, st StepState) []StepEvent {
		var out []StepEvent
		for _, s := range steps {
			out = append(out, ev(s[0], s[1], st, ""))
		}
		return out
	}
	userLoc := DefaultLocation(ScopeUser, d)
	machineLoc := DefaultLocation(ScopeMachine, d)

	// A run that finishes: every step waits, runs and is done.
	finished := func(steps [][2]string, details map[string]string) []StepEvent {
		out := all(steps, StepWaiting)
		for _, s := range steps {
			out = append(out, ev(s[0], s[1], StepRunning, ""), ev(s[0], s[1], StepDone, details[s[0]]))
		}
		return out
	}

	fresh := &ScriptedBackend{St: base, Checks: ready, Script: finished(userSteps, map[string]string{"copy-files": userLoc + `\unravis.exe`}),
		Res: Result{Outcome: OutcomeDone, Message: "Unravis " + version + " is installed.", Version: version, AppExe: userLoc + `\unravis.exe`}}

	failChecks := append([]Check{}, ready...)
	failChecks[2] = Check{ID: "install-folder", Title: "Disk space", Status: CheckFailed, Detail: "C: has 61 MB free; Unravis needs 100 MB",
		Fix: "Free some space on C: (empty the Recycle Bin, or remove files you no longer need), then press Check again."}
	failChecks[4] = Check{ID: "reboot", Title: "Restart pending", Status: CheckPartly,
		Detail: "Windows is waiting for a restart after an update", Fix: "Setup can go on. Restart when it suits you."}
	preflightFail := &ScriptedBackend{St: base, Checks: failChecks}

	// The progress screen: three steps done, one running, the rest waiting. It holds until Cancel.
	prog := all(userSteps, StepWaiting)
	for _, s := range userSteps[:3] {
		detail := ""
		if s[0] == "copy-files" {
			detail = userLoc + `\unravis.exe`
		}
		prog = append(prog, ev(s[0], s[1], StepRunning, ""), ev(s[0], s[1], StepDone, detail))
	}
	prog = append(prog, ev(userSteps[3][0], userSteps[3][1], StepRunning, ""))
	progress := &ScriptedBackend{St: base, Checks: ready, Script: prog, Hold: true}

	bbChecks := append([]Check{}, ready...)
	bbChecks = append(bbChecks, Check{ID: "port", Title: "Blackbox port", Status: CheckReady, Detail: "Port 7788 is free"},
		Check{ID: "recordings-folder", Title: "Recordings folder", Status: CheckReady, Detail: `D: has 212 GB free`})
	done := &ScriptedBackend{St: base, Checks: bbChecks,
		Script: finished(machineSteps, map[string]string{"copy-files": machineLoc + `\unravis.exe`, "blackbox": "web-01 is Running on port 7788"}),
		Res: Result{Outcome: OutcomeDone, Message: "Unravis " + version + " is installed.", Version: version, AppExe: machineLoc + `\unravis.exe`,
			ReadKey: "ubk-capture-7Q2M-HX4D-9KWP-T3ZC"}}

	// A failure at the Blackbox step, everything before it put back.
	failScript := all(machineSteps, StepWaiting)
	for _, s := range machineSteps[:6] {
		failScript = append(failScript, ev(s[0], s[1], StepRunning, ""), ev(s[0], s[1], StepDone, ""))
	}
	failScript = append(failScript, ev("blackbox", "Set up the Blackbox recorder", StepRunning, ""),
		ev("blackbox", "Set up the Blackbox recorder", StepFailed, "Port 7788 is used by another program."))
	for i := 5; i >= 0; i-- {
		failScript = append(failScript, ev(machineSteps[i][0], machineSteps[i][1], StepUndone, ""))
	}
	failed := &ScriptedBackend{St: base, Checks: bbChecks, Script: failScript,
		Res: Result{Outcome: OutcomeRolledBack, Version: version, Failed: "Set up the Blackbox recorder",
			Message: "Setup stopped at “Set up the Blackbox recorder”: Port 7788 is used by another program. Everything it changed has been put back.",
			Fix:     "Pick another port in Options, then try again."}}

	up := base
	up.Mode = OpUpgrade
	up.Installed = &UIInstalled{Version: "0.9.2", Scope: ScopeUser, Location: userLoc}
	upChecks := []Check{ready[0], ready[1], ready[2],
		{ID: "existing", Title: "Unravis already installed", Status: CheckReady, Detail: "Version 0.9.2 in " + userLoc + ". This will upgrade it."},
		{ID: "app-running", Title: "Unravis is running", Status: CheckPartly, Detail: "It will be closed. Unsaved work in an open wizard is lost.",
			Fix: "Finish what you are doing in Unravis first, or let setup close it."}}
	upSteps := [][2]string{{"close-app", "Close Unravis"}, {"copy-files", "Copy the program files"}, {"shortcuts", "Add the Start menu shortcut"},
		{"arp", "Add Unravis to Installed apps"}, {"state", "Save the setup choices"}, {"verify", "Check that Unravis starts"}}
	upgrade := &ScriptedBackend{St: up, Checks: upChecks, Script: finished(upSteps, nil),
		Res: Result{Outcome: OutcomeDone, Version: version, AppExe: userLoc + `\unravis.exe`, RestartApp: true,
			Message: "Unravis is upgraded to " + version + ". Your cases and saved connections were kept."}}

	rep := base
	rep.Mode = OpRepair
	rep.Installed = &UIInstalled{Version: version, Scope: ScopeUser, Location: userLoc}
	repChecks := []Check{ready[0], ready[1], ready[2],
		{ID: "existing", Title: "Unravis already installed", Status: CheckReady, Detail: "Version " + version + " in " + userLoc + ". This will repair it."}}
	repair := &ScriptedBackend{St: rep, Checks: repChecks, Script: finished(upSteps, nil),
		Res: Result{Outcome: OutcomeDone, Version: version, AppExe: userLoc + `\unravis.exe`, Message: "Unravis is repaired."}}

	un := base
	un.Mode = OpUninstall
	un.Installed = &UIInstalled{Version: version, Scope: ScopeMachine, Location: machineLoc, BlackboxName: "web-01"}
	un.Data = []UIDataPlace{{Kind: "cases", Folder: `C:\ProgramData\Unravis`, Bytes: 3_435_973_837}, {Kind: "recordings", Folder: `D:\UnravisBlackbox`, Bytes: 1_181_116_006}}
	unSteps := [][2]string{{"close-app", "Close Unravis"}, {"blackbox", "Remove the Blackbox recorder"}, {"keeper-service", "Remove the key keeper service"},
		{"shortcuts", "Remove the shortcuts"}, {"arp", "Remove Unravis from Installed apps"}, {"program-files", "Remove the program files"}, {"data", "Deal with your data"}}
	uninstall := &ScriptedBackend{St: un, Script: finished(unSteps, nil),
		Res: Result{Outcome: OutcomeDone, Version: version, Message: "Unravis is removed.",
			Kept: []string{`Cases and saved connections of everyone on this PC in C:\ProgramData\Unravis`, `Blackbox recordings in D:\UnravisBlackbox`}}}

	return map[string]UIBackend{
		"fresh": fresh, "preflight-fail": preflightFail, "progress": progress, "done": done, "failed": failed,
		"upgrade": upgrade, "repair": repair, "uninstall": uninstall,
	}
}
