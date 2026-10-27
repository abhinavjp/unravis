package blackbox

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// OSEnv is the real Env. IsAdmin is in service_windows.go / service_other.go.
type OSEnv struct{}

// ExePath is the program that is running.
func (OSEnv) ExePath() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(p)
}

// ProgramDir is %ProgramFiles%\Unravis Blackbox.
func (OSEnv) ProgramDir() (string, error) {
	pf := os.Getenv("ProgramFiles")
	if pf == "" {
		return "", errors.New("the ProgramFiles folder is not set on this computer")
	}
	return filepath.Join(pf, "Unravis Blackbox"), nil
}

// PortFree tries to listen on the port and lets go again.
func (OSEnv) PortFree(bind string, port int) error {
	ln, err := net.Listen("tcp", net.JoinHostPort(bind, strconv.Itoa(port)))
	if err != nil {
		return err
	}
	return ln.Close()
}

// FolderWritable writes and removes a small file in dir.
func (OSEnv) FolderWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".write-check-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_, werr := f.WriteString("ok")
	cerr := f.Close()
	rerr := os.Remove(name)
	for _, e := range []error{werr, cerr, rerr} {
		if e != nil {
			return e
		}
	}
	return nil
}

// FileSHA256 is the hex SHA-256 of the file.
func (OSEnv) FileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ProbeStatus asks the local status page for /status with the key, and checks
// that a request without the key is refused.
func (OSEnv) ProbeStatus(port int, key string) error {
	url := fmt.Sprintf("http://127.0.0.1:%d/status", port)
	c := &http.Client{Timeout: 3 * time.Second}
	var last error
	for i := 0; i < 8; i++ { // the service may need a moment to open the port
		last = probeOnce(c, url, key)
		if last == nil {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return last
}

func probeOnce(c *http.Client, url, key string) error {
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set(KeyHeader, key)
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("answered %d with the key", resp.StatusCode)
	}
	req2, _ := http.NewRequest(http.MethodGet, url, nil)
	resp2, err := c.Do(req2)
	if err != nil {
		return err
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		return fmt.Errorf("answered %d without the key", resp2.StatusCode)
	}
	return nil
}

// Sleep pauses.
func (OSEnv) Sleep(d time.Duration) { time.Sleep(d) }
