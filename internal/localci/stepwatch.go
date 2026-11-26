package localci

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// A step that names a watcher in local-ci.yml (step-watch: step name -> command) runs with that command started beside it. The command
// gets {log} and {stop} (forward-slash paths): it writes one line to the log for each problem it sees while the step runs, creates
// "<stop>.ready" once it is watching, and exits when "<stop>" appears. A step whose watcher logged anything fails, whatever the step
// itself said. A watcher that cannot start only warns: it must not turn a good run red. The project decides what is watched.

const stepWatchReadyWait = 20 * time.Second

// stepWatch is a running watcher.
type stepWatch struct {
	cmd      *exec.Cmd
	log      string
	stopFile string
	done     chan error
}

// startStepWatch starts the watcher; warn is told why it could not start (the returned value is then nil).
func (in *instance) startStepWatch(ctx context.Context, line string, n int, env *envList, warn func(string)) *stepWatch {
	base := filepath.Join(in.tmpDir, fmt.Sprintf("watch-%02d", n))
	w := &stepWatch{log: base + ".log", stopFile: base + ".stop", done: make(chan error, 1)}
	for _, p := range []string{w.log, w.stopFile, w.stopFile + ".ready", w.stopFile + ".done"} {
		_ = os.Remove(p)
	}
	if err := os.WriteFile(w.log, nil, 0o600); err != nil {
		warn("the step watcher could not start: " + err.Error())
		return nil
	}
	script := strings.NewReplacer("{log}", filepath.ToSlash(w.log), "{stop}", filepath.ToSlash(w.stopFile)).Replace(line)
	sc, err := writeScript(in.defaultShell(), script, in.tmpDir, fmt.Sprintf("watch-%02d", n))
	if err != nil {
		warn("the step watcher could not start: " + err.Error())
		return nil
	}
	cmd := exec.Command(sc.Path, sc.Args...) //nolint:gosec // the command is the project's own local-ci.yml
	cmd.Dir = in.workdir
	cmd.Env = env.items
	if err := cmd.Start(); err != nil {
		warn("the step watcher could not start: " + err.Error())
		return nil
	}
	w.cmd = cmd
	go func() { w.done <- cmd.Wait() }()
	deadline := time.Now().Add(stepWatchReadyWait)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		if _, err := os.Stat(w.stopFile + ".ready"); err == nil {
			return w
		}
		select {
		case <-w.done:
			warn("the step watcher exited before it was ready")
			return nil
		case <-time.After(100 * time.Millisecond):
		}
	}
	warn("the step watcher was not ready in time: this step is not watched")
	_ = cmd.Process.Kill()
	<-w.done
	return nil
}

// stop ends the watcher and returns what it logged (one entry per non-empty line).
func (w *stepWatch) stop() []string {
	if w == nil {
		return nil
	}
	_ = os.WriteFile(w.stopFile, []byte("stop"), 0o600)
	select {
	case <-w.done:
	case <-time.After(15 * time.Second):
		_ = w.cmd.Process.Kill()
		<-w.done
	}
	b, err := os.ReadFile(w.log) //nolint:gosec // the watcher's own log in the job's temp folder
	if err != nil {
		return nil
	}
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}
