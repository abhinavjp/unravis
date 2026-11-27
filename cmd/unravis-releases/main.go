// Command unravis-releases is a small dashboard for Unravis releases: every release, its channel, the release
// manager's step by step status, the verify result and withdrawn versions. It is a separate program from Unravis. It reads the
// release manager's log on this PC, the public GitHub release list and the signed update feed. The only things it changes are
// the local CI store's queue, machines, policy and holds, through the Homestretch actions (internal/hsaction), each one logged.
// It never promotes or releases, and it never touches a signing key or passphrase.
package main

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"api-audit-explorer/internal/releasedash"
	"api-audit-explorer/internal/update"
)

//go:embed all:ui
var uiFiles embed.FS

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "unravis-releases: "+err.Error())
		showError("Unravis Releases could not start:\n\n" + err.Error()) // the Windows build has no console to print to
		os.Exit(1)
	}
}

func run(args []string) error {
	fl := flag.NewFlagSet("unravis-releases", flag.ContinueOnError)
	data := fl.String("data", "", "release manager data folder (default: the one Release.ps1 uses)")
	port := fl.Int("port", 0, "local port (0 picks a free one)")
	projectsFile := fl.String("projects", "", "JSON file with the project list (name, repo, CI store folder, channel and step labels); default: Unravis")
	ciStore := fl.String("ci-store", "", "folder of the local CI store that the Runs and Overview tabs read (default: the one unravis-ci writes)")
	open := fl.Bool("open", false, "open the link in the default browser instead of the app window")
	exitWhenClosed := fl.Bool("exit-when-closed", false, "stop when the page closes (always on in the app window; for tests)")
	browserOnly := fl.Bool("print-link", false, "only print the link; open no window (for tests and servers)")
	if err := fl.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *port < 0 || *port > 65535 {
		return errors.New("--port must be between 0 and 65535")
	}
	dir := *data
	if dir == "" {
		dir = releasedash.DataDir(os.Getenv)
	}
	projects, err := projectList(*projectsFile, *ciStore, os.Getenv)
	if err != nil {
		return err
	}
	keys, err := update.TrustedKeys()
	if err != nil {
		return err
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		cache = os.TempDir()
	}
	state := filepath.Join(cache, "Unravis", "releases-dashboard", "state.json")
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return errors.New("could not make a local session code")
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	ui, err := fs.Sub(uiFiles, "ui")
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		return err
	}
	// The action key travels only in the fragment (#a=...): a browser never sends a fragment to a server, so another
	// loopback listener that gets our session cookie still cannot take actions (see internal/releasedash/actions.go).
	actionToken := releasedash.NewActionToken()
	link := startLink(ln.Addr().(*net.TCPAddr).Port, token, actionToken)
	fmt.Println("Unravis releases. Link:\n  " + link + "\nPress Ctrl+C to stop.")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	// Without a console (the Windows build) nobody can press Ctrl+C, so the program must end by itself when its page is gone.
	windowMode := *exitWhenClosed || (runtime.GOOS == "windows" && !*browserOnly)
	switch {
	case *open:
		openBrowser(link)
	case !*browserOnly && runtime.GOOS == "windows":
		profile := filepath.Join(filepath.Dir(state), "window")
		_, werr := appWindow(link, profile)
		if werr != nil {
			fmt.Println("Could not open the app window (" + werr.Error() + "), opening your browser instead.")
			openBrowser(link)
			break
		}
		// msedge.exe returns at once when Edge is already running, so the window cannot be waited on. The page says so when it
		// closes (see /api/bye); no page at all within a minute also ends the program.
	}
	dash := releasedash.New(dir, state, keys)
	dash.Projects = projects
	dash.ActionToken = actionToken
	if windowMode {
		var mu sync.Mutex
		var timer *time.Timer
		arm := func(d time.Duration) {
			mu.Lock()
			defer mu.Unlock()
			if timer != nil {
				timer.Stop()
			}
			timer = time.AfterFunc(d, stop)
		}
		arm(60 * time.Second)
		// Any request proves the window is there; a window that vanished without saying so is noticed after 150 seconds
		// (the page pings every 20 seconds, and a minimised window may be slowed to once a minute).
		dash.OnRequest = func() { arm(150 * time.Second) }
		dash.OnBye = func() { arm(4 * time.Second) } // a reload comes back within this time and cancels it
	}
	srv := &http.Server{Handler: releasedash.Handler(dash, token, ui), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sc, c := context.WithTimeout(context.Background(), 3*time.Second)
		defer c()
		_ = srv.Shutdown(sc)
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// startLink is the link the program prints and opens: the session code in the query, which the server reads once and
// swaps for a cookie, and the action key in the fragment, which only the page reads.
func startLink(port int, token, actionToken string) string {
	link := fmt.Sprintf("http://127.0.0.1:%d/?t=%s", port, token)
	if actionToken != "" {
		link += "#a=" + actionToken
	}
	return link
}

func openBrowser(u string) {
	_ = browserCommand(runtime.GOOS, u).Start()
}

// browserCommand builds the command that opens u in the default browser. On Windows the program is rundll32 by its full
// path in the Windows system folder (see rundll32Path), so a planted rundll32.exe on PATH is never started.
func browserCommand(goos, u string) *exec.Cmd {
	switch goos {
	case "windows":
		return exec.Command(rundll32Path(), "url.dll,FileProtocolHandler", u) //nolint:gosec // G204: a fixed program; u is the loopback URL of this dashboard
	case "darwin":
		return exec.Command("open", u) //nolint:gosec // G204: a fixed program; u is the loopback URL of this dashboard
	default:
		return exec.Command("xdg-open", u) //nolint:gosec // G204: a fixed program; u is the loopback URL of this dashboard
	}
}
