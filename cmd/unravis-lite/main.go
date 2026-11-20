// Command unravis-lite is Unravis Lite: a small viewer that shows the charts of
// one Blackbox. It is for a server where the full app does not belong. It reads
// the recordings in place, changes nothing, saves nothing and sends nothing
// anywhere. Windows file permissions decide what it may read (D10).
package main

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"api-audit-explorer/internal/blackbox"
	"api-audit-explorer/internal/lite"
	"api-audit-explorer/internal/server"
	"api-audit-explorer/internal/version"
)

// ui is the built page (web/lite, built by "npm run build" into this folder with fixed file names).
//
//go:embed all:ui
var uiFiles embed.FS

const codeLife = 2 * time.Minute

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "Unravis Lite: "+err.Error())
		if ownsConsole() {
			// Started by a double-click: keep the window open so the message can be read.
			fmt.Fprintln(os.Stderr, "Press Enter to close.")
			_, _ = fmt.Fscanln(os.Stdin)
		}
		os.Exit(1)
	}
}

type options struct {
	folder  string
	port    int
	open    bool
	version bool
}

func parse(args []string, stderr io.Writer) (options, error) {
	var o options
	fl := flag.NewFlagSet("unravis-lite", flag.ContinueOnError)
	fl.SetOutput(stderr)
	fl.StringVar(&o.folder, "folder", "", "the Blackbox recordings folder (default: the Blackbox on this PC)")
	fl.IntVar(&o.port, "port", 0, "local port (0 picks a free one)")
	fl.BoolVar(&o.open, "open", false, "also open the link in the default browser (do not use it from an administrator window)")
	fl.BoolVar(&o.version, "version", false, "print the version and exit")
	if err := fl.Parse(args); err != nil {
		return o, err
	}
	if fl.NArg() > 0 {
		return o, fmt.Errorf("unexpected argument %q", fl.Arg(0))
	}
	if o.port < 0 || o.port > 65535 {
		return o, errors.New("--port must be between 0 and 65535")
	}
	return o, nil
}

// find says which folder to show: the one given, else the Blackbox this PC runs.
func find(folder string, scan func() (blackbox.LocalFound, error)) (loc blackbox.Location, name string, err error) {
	name = "Blackbox"
	if strings.TrimSpace(folder) == "" {
		found, serr := scan()
		switch {
		case serr != nil:
			return loc, name, fmt.Errorf("could not ask this PC for its Blackbox (%v). Start with --folder <recordings folder>", serr)
		case found.Installed && found.ConfigUnreadable:
			return loc, name, errors.New("a Blackbox is installed on this PC but its settings could not be read. Run Unravis Lite as administrator, or start with --folder <recordings folder>")
		case !found.Installed || found.Folder == "":
			return loc, name, errors.New("no Blackbox was found on this PC. Start with --folder <recordings folder>")
		}
		folder = found.Folder
		if found.Name != "" {
			name = found.Name
		}
	}
	loc, err = blackbox.ParseLocation(folder)
	if err != nil {
		return loc, name, fmt.Errorf("that folder cannot be used: %w", err)
	}
	if loc.Kind == blackbox.LocURL {
		return loc, name, errors.New("Unravis Lite reads a folder or a share, not an address")
	}
	if info, serr := statFolder(loc.Path); serr != nil {
		return loc, name, fmt.Errorf("cannot open %s (%v). Check the path, and run as administrator if the folder is locked", loc.Path, serr)
	} else if !info.IsDir() {
		return loc, name, fmt.Errorf("%s is not a folder", loc.Path)
	}
	return loc, name, nil
}

// handler is the whole Lite server: the full server's Blackbox code behind the allowlist.
func handler(loc blackbox.Location, name, token string, code string, until time.Time, ui fs.FS) http.Handler {
	store := lite.NewStore(name, loc, time.Now())
	inner := server.New(lite.NoData{}, ui, token, false,
		server.WithBlackbox(server.BlackboxDeps{Store: store}),
		server.WithLaunchCode(code, until),
	)
	return lite.Wrap(inner)
}

// statFolder is a variable so tests can look at paths that do not exist on the test machine.
var statFolder = os.Stat

func random(n int) (string, error) {
	raw := make([]byte, n)
	if _, err := rand.Read(raw); err != nil {
		return "", errors.New("could not make a local session code")
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func run(args []string, stdout, stderr io.Writer) error {
	o, err := parse(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if o.version {
		fmt.Fprintln(stdout, strings.Replace(version.String(), "Unravis", "Unravis Lite", 1))
		return nil
	}
	loc, name, err := find(o.folder, func() (blackbox.LocalFound, error) {
		return blackbox.ScanThisPC(blackbox.WinService{}, blackbox.OSEnv{}, blackbox.LoadConfig)
	})
	if err != nil {
		return err
	}
	ui, err := fs.Sub(uiFiles, "ui")
	if err != nil {
		return err
	}
	token, err := random(32)
	if err != nil {
		return err
	}
	code, err := random(24)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", fmt.Sprint(o.port)))
	if err != nil {
		return fmt.Errorf("could not listen on this PC: %w", err)
	}
	srv := &http.Server{
		Handler:           handler(loc, name, token, code, time.Now().Add(codeLife), ui),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      90 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	link := "http://" + listener.Addr().String() + "/launch?c=" + code
	// The link is always printed: it works once, and a server may have no browser that opens. Lite is often run as
	// administrator, and a browser started from it would run elevated too, so the browser is opened only with --open.
	fmt.Fprintf(stdout, "%s is showing %s. Close this window to stop it.\nOpen it with this one-time link (works once, expires in %d minutes):\n%s\n",
		strings.Replace(version.Short(), "Unravis", "Unravis Lite", 1), loc.Display(), int(codeLife/time.Minute), link)
	if o.open && runtime.GOOS == "windows" {
		_ = openInBrowser(link)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- srv.Serve(listener) }()
	select {
	case <-ctx.Done():
		shut, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		_ = srv.Shutdown(shut)
		return nil
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		log.Printf("stopped: %v", err)
		return err
	}
}

// openInBrowser starts rundll32 by full path, so a planted copy in the current folder is never run.
func openInBrowser(url string) error {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = `C:\Windows`
	}
	cmd := exec.Command(filepath.Join(root, "System32", "rundll32.exe"), "url.dll,FileProtocolHandler", url)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
