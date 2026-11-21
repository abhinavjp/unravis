// Command unravis-releases is a small read-only dashboard for Unravis releases: every release, its channel, the release
// manager's step by step status, the verify result and withdrawn versions. It is a separate program from Unravis. It reads the
// release manager's log on this PC, the public GitHub release list and the signed update feed, and changes nothing.
// It never touches a signing key or passphrase.
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
	"time"

	"api-audit-explorer/internal/releasedash"
	"api-audit-explorer/internal/update"
)

//go:embed all:ui
var uiFiles embed.FS

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "unravis-releases: "+err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	fl := flag.NewFlagSet("unravis-releases", flag.ContinueOnError)
	data := fl.String("data", "", "release manager data folder (default: the one Release.ps1 uses)")
	port := fl.Int("port", 0, "local port (0 picks a free one)")
	open := fl.Bool("open", false, "also open the link in the default browser")
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
	keys, err := update.TrustedKeys()
	if err != nil {
		return err
	}
	state := ""
	if c, err := os.UserCacheDir(); err == nil {
		state = filepath.Join(c, "Unravis", "releases-dashboard", "state.json")
	}
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
	link := fmt.Sprintf("http://127.0.0.1:%d/?t=%s", ln.Addr().(*net.TCPAddr).Port, token)
	fmt.Println("Unravis releases (read only). Open this link in your browser:\n  " + link + "\nPress Ctrl+C to stop.")
	if *open {
		openBrowser(link)
	}
	srv := &http.Server{Handler: releasedash.Handler(releasedash.New(dir, state, keys), token, ui), ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
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

func openBrowser(u string) {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		c = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	case "darwin":
		c = exec.Command("open", u)
	default:
		c = exec.Command("xdg-open", u)
	}
	_ = c.Start()
}
