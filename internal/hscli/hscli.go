// Package hscli is the `homestretch` command (and its short name `hs`): the command-line side of Homestretch, the
// release and CI screen. Every action a person can take on the page is one command here, and the commands that change
// something run the same action registry (internal/hsaction) as the page, so the safety checks and the activity entry
// are the same. The engine commands of unravis-ci live under `homestretch ci`.
//
// The table in commands.go is the whole command list: the help text, the dispatch and the test that fails when a
// registry action has no command all read it.
package hscli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"api-audit-explorer/internal/cicli"
)

// Version is the version `homestretch version` prints.
const Version = "0.1.0"

// Exit codes. They are the engine's (internal/cicli/exitcodes.go) and the design's (ROUND4-DESIGN.md section 4):
// 0 done, 1 workflow failed or refused, 2 usage or I/O error (and a store that cannot be read), 3 unsupported,
// 4 stopped by a stop state, 5 held by a freeze. Nothing returns 4 yet: the stop control is a later round's, and the
// code is kept for it.
const (
	ExitOK          = 0
	ExitFailed      = 1
	ExitUsage       = 2
	ExitUnsupported = 3
	ExitStopped     = 4
	ExitHeld        = 5
)

// Deps are the parts of the world the commands reach: the engine, PowerShell and the repository folder. Tests
// replace them, so no test starts a program or touches a repository.
type Deps struct {
	// Engine runs one unravis-ci command in this process (default: cicli.Main).
	Engine func(ctx context.Context, args []string, stdout, stderr io.Writer, environ []string) int
	// Script runs one PowerShell script with its arguments and returns its exit code (default: pwsh -File).
	Script func(ctx context.Context, script string, args []string, stdout, stderr io.Writer, environ []string) int
	// Roots are the folders to start looking for the repository from (default: the working folder, then the folder
	// of the program).
	Roots func() []string
}

// DefaultDeps are the real ones.
func DefaultDeps() Deps {
	return Deps{Engine: cicli.Main, Script: runPowerShell, Roots: defaultRoots}
}

// ProgName is the name the program prints about itself for the first word of its command line: `hs` when it was
// started as hs, hs.exe or HS.EXE, `homestretch` otherwise. It is the only thing that depends on the name.
func ProgName(argv0 string) string {
	base := strings.ToLower(filepath.Base(strings.ReplaceAll(argv0, `\`, "/")))
	base = strings.TrimSuffix(base, ".exe")
	if base == "hs" {
		return "hs"
	}
	return "homestretch"
}

// Main runs one command line (args without the program name) and returns the exit code.
func Main(ctx context.Context, argv0 string, args []string, stdout, stderr io.Writer, environ []string) int {
	return Run(ctx, DefaultDeps(), ProgName(argv0), args, stdout, stderr, environ)
}

// Run is Main with the parts of the world given.
func Run(ctx context.Context, d Deps, prog string, args []string, stdout, stderr io.Writer, environ []string) int {
	// The engine names itself and starts its child processes through these two settings: `homestretch ci serve`
	// starts `homestretch ci run-item`, and its messages say `homestretch hold list`.
	oldProg, oldEngine, oldPrefix := cicli.Prog, cicli.EngineProg, cicli.ChildPrefix
	cicli.Prog, cicli.EngineProg, cicli.ChildPrefix = prog, prog+" ci", []string{"ci"}
	defer func() { cicli.Prog, cicli.EngineProg, cicli.ChildPrefix = oldProg, oldEngine, oldPrefix }()

	if d.Engine == nil || d.Script == nil || d.Roots == nil {
		def := DefaultDeps()
		if d.Engine == nil {
			d.Engine = def.Engine
		}
		if d.Script == nil {
			d.Script = def.Script
		}
		if d.Roots == nil {
			d.Roots = def.Roots
		}
	}
	c := &run{ctx: ctx, d: d, prog: prog, out: stdout, err: stderr, environ: environ}
	return c.dispatch(args)
}

// run is one command line being run.
type run struct {
	ctx     context.Context
	d       Deps
	prog    string
	out     io.Writer
	err     io.Writer
	environ []string
}

func (c *run) dispatch(args []string) int {
	if len(args) == 0 {
		c.usage(c.err)
		return ExitUsage
	}
	switch args[0] {
	case "help", "-h", "--help", "-help":
		c.usage(c.out)
		return ExitOK
	}
	cmd, rest := find(args)
	if cmd == nil {
		_, _ = fmt.Fprintf(c.err, "%s: %q is not a command. Run \"%s help\" to see them.\n", c.prog, args[0], c.prog)
		return ExitUsage
	}
	return cmd.run(c, rest)
}

// find returns the command with the longest path that starts args, and the arguments after that path. A group
// word followed by something that is not one of its sub-commands finds the group's own command, if it has one.
func find(args []string) (*command, []string) {
	var best *command
	for i := range commands {
		cmd := &commands[i]
		if len(cmd.Path) > len(args) {
			continue
		}
		ok := true
		for j, w := range cmd.Path {
			if args[j] != w {
				ok = false
				break
			}
		}
		if ok && (best == nil || len(cmd.Path) > len(best.Path)) {
			best = cmd
		}
	}
	if best == nil {
		return nil, nil
	}
	return best, args[len(best.Path):]
}

func (c *run) usage(w io.Writer) {
	_, _ = fmt.Fprintf(w, "%s: release and CI on this PC (short name: hs)\n\nusage: %s <command> [options]\n\n", c.prog, c.prog)
	for _, g := range groups() {
		_, _ = fmt.Fprintf(w, "%s\n", g.title)
		for _, cmd := range g.cmds {
			note := ""
			if cmd.NotBacked != "" {
				note = "   (not built yet: exit 3)"
			}
			_, _ = fmt.Fprintf(w, "  %-62s %s%s\n", c.prog+" "+cmd.Usage, cmd.Summary, note)
		}
		_, _ = fmt.Fprintln(w)
	}
	_, _ = fmt.Fprintf(w, "Read commands take --json. Changing commands record who did it in the activity log (actor cli, or HOMESTRETCH_ACTOR).\n")
	_, _ = fmt.Fprintf(w, "Exit codes: 0 done, 1 failed or refused, 2 usage or I/O error, 3 not supported, 4 stopped (reserved), 5 held by a freeze.\n")
	_, _ = fmt.Fprintf(w, "The engine's own commands (serve, run, lint, log, ...) are under \"%s ci\"; \"%s ci help\" lists them.\n", c.prog, c.prog)
}

type group struct {
	title string
	cmds  []*command
}

// groups returns the commands by their Group, in the order the table lists them.
func groups() []group {
	var out []group
	idx := map[string]int{}
	for i := range commands {
		cmd := &commands[i]
		if cmd.Hidden {
			continue
		}
		n, ok := idx[cmd.Group]
		if !ok {
			n = len(out)
			idx[cmd.Group] = n
			out = append(out, group{title: cmd.Group})
		}
		out[n].cmds = append(out[n].cmds, cmd)
	}
	return out
}

// ---------------------------------------------------------------- small helpers the commands share

// engine runs one engine command in this process.
func (c *run) engine(args ...string) int {
	return c.d.Engine(c.ctx, args, c.out, c.err, c.environ)
}

func (c *run) usageErr(format string, a ...any) int {
	_, _ = fmt.Fprintf(c.err, "%s: %s\n", c.prog, fmt.Sprintf(format, a...))
	return ExitUsage
}

// notBacked is the answer of a command that nothing stands behind yet: it says so, changes nothing and exits 3.
func (c *run) notBacked(cmd *command) int {
	_, _ = fmt.Fprintf(c.err, "%s %s: not built yet. %s Nothing was changed.\n", c.prog, strings.Join(cmd.Path, " "), cmd.NotBacked)
	return ExitUnsupported
}

// stateFlag turns the --state-dir of this command line into the --state the older engine commands take.
func stateFlag(args []string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		switch {
		case a == "--state-dir" || a == "-state-dir":
			a = "--state"
		case strings.HasPrefix(a, "--state-dir="):
			a = "--state=" + strings.TrimPrefix(a, "--state-dir=")
		case strings.HasPrefix(a, "-state-dir="):
			a = "--state=" + strings.TrimPrefix(a, "-state-dir=")
		}
		out = append(out, a)
	}
	return out
}

// takeFlag removes `--name value` or `--name=value` from args and returns the value ("" when it was not given).
// Everything after a bare "--" is left alone.
func takeFlag(args []string, name string) (value string, rest []string, err error) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			rest = append(rest, args[i:]...)
			break
		}
		for _, p := range []string{"--" + name, "-" + name} {
			if a == p {
				if i+1 >= len(args) {
					return "", nil, fmt.Errorf("--%s needs a value", name)
				}
				value = args[i+1]
				i++
				goto next
			}
			if strings.HasPrefix(a, p+"=") {
				value = strings.TrimPrefix(a, p+"=")
				goto next
			}
		}
		rest = append(rest, a)
	next:
	}
	return value, rest, nil
}

// positionals returns the arguments that are not flags and not the value of a flag in valueFlags.
func positionals(args []string, valueFlags ...string) (pos, flags []string) {
	takes := map[string]bool{}
	for _, f := range valueFlags {
		takes["--"+f] = true
		takes["-"+f] = true
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			pos = append(pos, args[i+1:]...)
			return pos, flags
		case strings.HasPrefix(a, "-") && len(a) > 1:
			flags = append(flags, a)
			if takes[a] && i+1 < len(args) {
				flags = append(flags, args[i+1])
				i++
			}
		default:
			pos = append(pos, a)
		}
	}
	return pos, flags
}
