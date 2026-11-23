// Package cmdtree is the Cobra command tree of the command line (spec 05-cli D3, 3.1). Only this package and cmd/unravis import Cobra;
// policy, data and server packages never do. This slice holds the commands that need no running app: version, schema, completion and
// `blackbox inspect --offline`. The online commands come with the attach layer.
package cmdtree

import (
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"api-audit-explorer/internal/clicore"
)

// IO is what a run reads and writes. A test passes buffers; main passes the process streams.
type IO struct {
	In     io.Reader
	Out    io.Writer
	Err    io.Writer
	Getenv func(string) string // nil means the allowed environment variables only
	Exe    func() (string, error)
}

// Default and largest --timeout (spec 3.2).
const (
	DefaultTimeout = 30 * time.Second
	MaxTimeout     = time.Hour
)

// state is what the commands of one run share.
type state struct {
	io      IO
	mode    clicore.Mode
	reqID   string
	ctx     context.Context
	opts    globalFlags
	flagSet bool // a flag named the output mode
	cancel  context.CancelFunc
}

type globalFlags struct {
	output     string
	json       bool
	timeout    time.Duration
	quiet      bool
	noColor    bool
	outputFile string
	overwrite  bool
}

// opAnnotation names the operation id of a command, for the error envelope of a usage error.
const opAnnotation = "unravis.op"

// emitsAnnotation marks a command that writes a result through emit (the others write help or a shell script, or nothing). Only
// those take --output-file. No command has a JSON Lines form yet, so they refuse -o jsonl before they run.
const emitsAnnotation = "unravis.emits"

func emitting(op string) map[string]string {
	return map[string]string{opAnnotation: op, emitsAnnotation: "1"}
}

func init() {
	// Cobra's own Explorer double-click notice sleeps and exits 1; the notice of spec 3.8 comes with the Windows slice.
	cobra.MousetrapHelpText = ""
}

// legacyFlags are the options of the app program (unravis.exe) that the command line does not take (spec D5). "--version" is answered
// here; "--quit" and "--check-update" are forwarded to the app program in a later slice.
var legacyFlags = map[string]bool{
	"demo": true, "port": true, "no-browser": true, "mcp": true, "mcp-allow-identifiers": true, "allow-broad-scans": true,
	"docs-examined-budget": true, "quit": true, "check-update": true, "background": true, "pipe-server": true, "pipe-only": true, "loopback": true,
}

// Run runs the command line and returns its exit code. Output goes to io; nothing else is written anywhere.
func Run(ctx context.Context, args []string, io IO) int {
	if io.Getenv == nil {
		io.Getenv = clicore.Getenv
	}
	if args == nil {
		args = []string{} // a nil slice would make Cobra read the process arguments
	}
	st := &state{io: io, reqID: clicore.NewRequestID(), mode: clicore.PeekOutputMode(args, io.Getenv)}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	go func() {
		<-ctx.Done()
		stop() // after the first Ctrl+C the next one gets the default action, so a read stuck in a blocking call can be killed
	}()
	st.ctx = ctx

	if code, handled := st.legacy(args); handled {
		return code
	}
	root := st.root()
	root.SetArgs(args)
	root.SetContext(ctx)
	executed, err := root.ExecuteC()
	if st.cancel != nil {
		st.cancel()
	}
	if err == nil {
		return 0
	}
	if errors.Is(err, errOutputClosed) {
		return clicore.ExitCode(clicore.CodeCanceled) // nobody reads stdout any more: no text anywhere
	}
	op := "unknown"
	if executed != nil {
		if v, ok := executed.Annotations[opAnnotation]; ok {
			op = v
		}
	}
	return clicore.WriteError(io.Err, st.mode, op, st.reqID, st.convert(err, executed))
}

// legacy answers an argument (before "--") that is an option of the app program, wherever it stands: a host that puts another switch
// first ("-o json --mcp") still gets LEGACY_OPTION. One or two dashes are read, like the flag package of the app does.
func (st *state) legacy(args []string) (int, bool) {
	for _, a := range args {
		if a == "--" {
			return 0, false
		}
		if !strings.HasPrefix(a, "-") || strings.HasPrefix(a, "---") {
			continue
		}
		name := strings.TrimPrefix(strings.TrimPrefix(a, "-"), "-")
		if name == "version" {
			return st.versionLine(), true
		}
		name, _, _ = strings.Cut(name, "=")
		if legacyFlags[name] {
			e := clicore.NewError(clicore.CodeLegacyOption, "That option belongs to "+st.appExe()+", not to the command line.", "")
			return clicore.WriteError(st.io.Err, st.mode, "unknown", st.reqID, e), true
		}
	}
	return 0, false
}

// appExe is the absolute path of the app program (unravis.exe): the folder above the one that holds this program (spec D5).
func (st *state) appExe() string {
	f := st.io.Exe
	if f == nil {
		f = os.Executable
	}
	if p, err := f(); err == nil && filepath.IsAbs(p) {
		return filepath.Join(filepath.Dir(filepath.Dir(p)), "unravis.exe")
	}
	return "unravis.exe in the Unravis folder"
}

var (
	reUnknownFlag = regexp.MustCompile(`unknown (?:shorthand )?flag: ('?-[-A-Za-z0-9_]+'?)`)
	reBadValue    = regexp.MustCompile(`for ("?-[-A-Za-z0-9_]+"?|'[A-Za-z0-9]'|"[-A-Za-z0-9_]+") flag`)
	reNeedsValue  = regexp.MustCompile(`flag needs an argument: ('?-[-A-Za-z0-9_]+'?|'[A-Za-z0-9]' in -[A-Za-z0-9])`)
	reFlagName    = regexp.MustCompile(`^--?[a-z][a-z0-9-]{0,31}$`)
)

// convert turns what Cobra or a command returns into a command line error. A usage message never repeats the value the person typed.
func (st *state) convert(err error, executed *cobra.Command) *clicore.Error {
	var ce *clicore.Error
	if errors.As(err, &ce) {
		return ce
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return clicore.NewError(clicore.CodeTimeout, "The command ran out of time.", "")
	}
	if errors.Is(err, context.Canceled) {
		return clicore.NewError(clicore.CodeCanceled, "Canceled.", "")
	}
	msg := err.Error()
	hint := "Run 'unravis --help' to see the commands and options."
	switch {
	case strings.HasPrefix(msg, "unknown command") && executed != nil && executed.HasParent() && !executed.HasSubCommands():
		return clicore.NewError(clicore.CodeInvalidArgument, "The command was given an argument it does not take.", hint)
	case strings.HasPrefix(msg, "unknown command"):
		return clicore.NewError(clicore.CodeUnknownOperation, "That is not a command.", hint)
	case reUnknownFlag.MatchString(msg):
		// Only a short, plain flag name is repeated: whatever else was typed (a pasted token) never reaches the output.
		if n := strings.Trim(reUnknownFlag.FindStringSubmatch(msg)[1], `'"`); reFlagName.MatchString(n) {
			return clicore.NewError(clicore.CodeInvalidArgument, "Unknown option "+n+".", hint)
		}
		return clicore.NewError(clicore.CodeInvalidArgument, "Unknown option.", hint)
	case reNeedsValue.MatchString(msg):
		return clicore.NewError(clicore.CodeInvalidArgument, "An option needs a value.", hint)
	case reBadValue.MatchString(msg):
		return clicore.NewError(clicore.CodeInvalidArgument, "The value of "+strings.Trim(reBadValue.FindStringSubmatch(msg)[1], `'"`)+" is not valid.", hint)
	case strings.Contains(msg, "accepts") && strings.Contains(msg, "arg"):
		return clicore.NewError(clicore.CodeInvalidArgument, "The command was given arguments it does not take.", hint)
	}
	return clicore.NewError(clicore.CodeInvalidArgument, "The command line is not valid.", hint)
}

func usage(msg string) *clicore.Error {
	return clicore.NewError(clicore.CodeInvalidArgument, msg, "Run 'unravis --help' to see the commands and options.")
}

// root builds the command tree of one run.
func (st *state) root() *cobra.Command {
	root := &cobra.Command{
		Use:           "unravis",
		Short:         "Unravis command line",
		Long:          "The Unravis command line. Commands that need no running app: version, schema, completion and blackbox inspect --offline.",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		RunE:          func(c *cobra.Command, args []string) error { return st.groupHelp(c) },
	}
	root.SetOut(st.io.Out)
	root.SetErr(st.io.Err)
	root.SetIn(st.io.In)
	root.SetFlagErrorFunc(func(c *cobra.Command, err error) error { return err })
	root.CompletionOptions.DisableDefaultCmd = true // our own static `completion` command takes its place
	root.SetHelpCommand(st.helpCmd())
	f := root.PersistentFlags()
	f.StringVarP(&st.opts.output, "output", "o", "table", "output format: table, json or jsonl")
	f.BoolVar(&st.opts.json, "json", false, "same as -o json")
	f.DurationVar(&st.opts.timeout, "timeout", DefaultTimeout, "give up after this long (at most 1h)")
	f.BoolVarP(&st.opts.quiet, "quiet", "q", false, "no progress text on stderr")
	f.BoolVar(&st.opts.noColor, "no-color", false, "no colour (there is none yet)")
	f.StringVar(&st.opts.outputFile, "output-file", "", "write JSON output to a new file (json mode only)")
	f.BoolVar(&st.opts.overwrite, "overwrite", false, "with --output-file: replace an existing file")
	root.PersistentPreRunE = func(c *cobra.Command, args []string) error {
		fl := c.Flags()
		st.flagSet = fl.Changed("output") || fl.Changed("json")
		if !fl.Changed("timeout") {
			if v := st.io.Getenv("UNRAVIS_TIMEOUT"); v != "" {
				d, err := time.ParseDuration(v)
				if err != nil {
					return usage("UNRAVIS_TIMEOUT is not a duration like 30s.")
				}
				st.opts.timeout = d
			}
		}
		if st.opts.timeout <= 0 || st.opts.timeout > MaxTimeout {
			return usage("The timeout must be more than 0 and at most 1h.")
		}
		// The mode that Cobra parsed must be the one the peek found: when a flag took another flag as its value ("--output-file
		// --json") they differ, and the request is unclear, so it is refused instead of guessed.
		parsed, perr := st.parsedMode(c)
		if perr != nil {
			return perr
		}
		if parsed != st.mode {
			return usage("The output format is given twice in different ways, or a flag took another flag as its value.")
		}
		if strings.HasPrefix(st.opts.outputFile, "-") {
			return usage("--output-file needs a file name (the next argument looks like a flag).")
		}
		emits := c.Annotations[emitsAnnotation] == "1"
		if st.opts.outputFile != "" && (!emits || st.mode == clicore.ModeTable) {
			return usage("--output-file needs a command that writes a result, with -o json.")
		}
		if st.opts.overwrite && st.opts.outputFile == "" {
			return usage("--overwrite goes with --output-file.")
		}
		if emits && st.mode == clicore.ModeJSONL {
			return clicore.NewError(clicore.CodeUnsupportedFormat, "This command has no JSON Lines form. Use -o json.", "")
		}
		st.ctx, st.cancel = context.WithTimeout(st.ctx, st.opts.timeout) // Run cancels it when the command ends
		return nil
	}
	root.AddCommand(st.versionCmd(), st.schemaCmd(), st.blackboxCmd(), st.completionCmd())
	return root
}

// parsedMode is the output mode as Cobra parsed the flags (and the variable when no flag names one).
func (st *state) parsedMode(c *cobra.Command) (clicore.Mode, *clicore.Error) {
	fl := c.Flags()
	switch {
	case fl.Changed("output"):
		m, ok := clicore.ParseMode(st.opts.output)
		if !ok {
			return "", clicore.NewError(clicore.CodeUnsupportedFormat, "The output format must be table, json or jsonl.", "")
		}
		return m, nil
	case fl.Changed("json") && st.opts.json:
		return clicore.ModeJSON, nil
	}
	if v := st.io.Getenv("UNRAVIS_OUTPUT"); v != "" {
		m, ok := clicore.ParseMode(v)
		if !ok {
			return "", clicore.NewError(clicore.CodeUnsupportedFormat, "UNRAVIS_OUTPUT must be table, json or jsonl.", "")
		}
		return m, nil
	}
	return clicore.ModeTable, nil
}

// groupHelp is the body of a command that only groups others (the root, blackbox): bare, it prints its help in table mode; with an
// unknown word or in a machine mode it is an error, so a script that mistyped a name never sees exit 0.
func (st *state) groupHelp(c *cobra.Command) error {
	if st.mode != clicore.ModeTable {
		return usage("A command is needed. Run 'unravis --help' to see the commands.")
	}
	return c.Help()
}

// helpCmd is `unravis help [command]`; an unknown command is an error, not the root help with exit 0.
func (st *state) helpCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "help [command]",
		Short:       "Help about a command",
		Annotations: map[string]string{opAnnotation: "help"},
		RunE: func(c *cobra.Command, args []string) error {
			target, rest, err := c.Root().Find(args)
			if err != nil || target == nil || len(rest) > 0 {
				return clicore.NewError(clicore.CodeUnknownOperation, "That is not a command.", "Run 'unravis --help' to see the commands.")
			}
			if st.mode != clicore.ModeTable {
				return usage("Help is text. Use -o table.")
			}
			return target.Help()
		},
	}
}

var shells = []string{"bash", "zsh", "fish", "powershell"}

// completionCmd prints a completion script for a shell. The script only offers the fixed names of commands and options: no
// candidate comes from a function, a file or the app (spec D24).
func (st *state) completionCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "completion bash|zsh|fish|powershell",
		Short:       "Print a shell completion script",
		Args:        cobra.ExactArgs(1),
		ValidArgs:   shells,
		Annotations: map[string]string{opAnnotation: "completion"},
		RunE: func(c *cobra.Command, args []string) error {
			if st.mode != clicore.ModeTable {
				return clicore.NewError(clicore.CodeUnsupportedFormat, "A completion script is text. Use -o table.", "")
			}
			if st.opts.outputFile != "" {
				return usage("--output-file does not go with completion.")
			}
			root := c.Root()
			var err error
			switch args[0] {
			case "bash":
				err = root.GenBashCompletionV2(st.io.Out, false)
			case "zsh":
				err = root.GenZshCompletion(st.io.Out)
			case "fish":
				err = root.GenFishCompletion(st.io.Out, false)
			case "powershell":
				err = root.GenPowerShellCompletion(st.io.Out)
			default:
				return usage("The shell must be bash, zsh, fish or powershell.")
			}
			return writeErr(err)
		},
	}
}

// emit writes the result of a command: a JSON line (or a file) in json mode, the table in table mode, and an error for jsonl, which
// only the paging commands have.
func (st *state) emit(op string, data any, meta clicore.Meta, table func(io.Writer) error) error {
	switch st.mode {
	case clicore.ModeJSON:
		b, err := clicore.MarshalSuccess(op, st.reqID, data, meta)
		if err != nil {
			return clicore.NewError(clicore.CodeInternal, "The result could not be written.", "")
		}
		if st.opts.outputFile != "" {
			if e := clicore.WriteNewFile(st.opts.outputFile, b, st.opts.overwrite); e != nil {
				return e
			}
			return nil
		}
		_, err = st.io.Out.Write(b)
		return writeErr(err)
	case clicore.ModeJSONL:
		return clicore.NewError(clicore.CodeUnsupportedFormat, "This command has no JSON Lines form. Use -o json.", "")
	}
	return writeErr(table(st.io.Out))
}

// writeErr maps a failed write. A reader that closed the pipe (EPIPE; ERROR_BROKEN_PIPE and ERROR_NO_DATA on Windows) stops the program
// quietly with the stopped code (Run writes no error text for it). Any other failure (a full disk, an I/O error) is reported.
func writeErr(err error) error {
	if err == nil {
		return nil
	}
	var no syscall.Errno
	if errors.As(err, &no) {
		if no == syscall.EPIPE || (runtime.GOOS == "windows" && (no == 109 || no == 232)) {
			return errOutputClosed
		}
	}
	return clicore.NewError(clicore.CodeInternal, "The result could not be written.", "")
}

var errOutputClosed = errors.New("the output was closed")

func fmtTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
