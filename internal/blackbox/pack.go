package blackbox

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The pack is what a person copies to another server to set a Blackbox up there:
// the recorder program, a blackbox.json with the settings chosen in the wizard,
// and a README. It never holds a read key (the key is made on the server at
// install), a password or any recording.

// MaxPackBytes is the size the pack must stay under.
const MaxPackBytes = 8 << 20

// PackExeName, PackConfigName and PackReadmeName are the three files of the pack.
const (
	PackExeName    = InstalledExeName
	PackConfigName = ConfigFileName
	PackReadmeName = "README.txt"
)

// ErrNoProgram is returned when the recorder program cannot be found.
var ErrNoProgram = errors.New("The Blackbox program (unravis-blackbox.exe) is not next to Unravis, so a pack cannot be built. Reinstall Unravis or copy unravis-blackbox.exe into its folder.")

// ProgramSource gives the recorder program the pack carries.
type ProgramSource interface {
	// Open returns the program file. The caller closes it.
	Open() (io.ReadCloser, error)
}

// FileProgram is the recorder program as a file.
type FileProgram struct{ Path string }

// Open opens the file.
func (f FileProgram) Open() (io.ReadCloser, error) {
	r, err := os.Open(f.Path)
	if err != nil {
		return nil, ErrNoProgram
	}
	return r, nil
}

// FindProgram looks for unravis-blackbox.exe in the folders given (normally the
// folder of the running Unravis, then a "blackbox" folder under it) and returns
// the first that exists.
func FindProgram(dirs ...string) (FileProgram, bool) {
	for _, d := range dirs {
		if d == "" {
			continue
		}
		p := filepath.Join(d, PackExeName)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return FileProgram{Path: p}, true
		}
	}
	return FileProgram{}, false
}

// PackInfo says what was built.
type PackInfo struct {
	FileName string `json:"fileName"`
	Bytes    int64  `json:"bytes"`
	Name     string `json:"name"`
}

// PackFileName is the zip name for a Blackbox name.
func PackFileName(name string) string { return "unravis-blackbox-" + name + ".zip" }

// BuildPack writes the pack zip for cfg to w. The settings are checked first, so
// a pack that the recorder would refuse is never built. The zip entries have a
// fixed time, so the same input gives the same bytes.
func BuildPack(w io.Writer, cfg Config, prog ProgramSource) (PackInfo, error) {
	if prog == nil {
		return PackInfo{}, ErrNoProgram
	}
	if err := cfg.Validate(); err != nil {
		return PackInfo{}, err
	}
	cfgBytes, err := cfg.Marshal()
	if err != nil {
		return PackInfo{}, err
	}
	rc, err := prog.Open()
	if err != nil {
		return PackInfo{}, err
	}
	defer rc.Close()
	exe, err := io.ReadAll(io.LimitReader(rc, MaxPackBytes+1))
	if err != nil {
		return PackInfo{}, fmt.Errorf("The Blackbox program could not be read: %w", err)
	}
	if len(exe) == 0 {
		return PackInfo{}, ErrNoProgram
	}
	if len(exe) > MaxPackBytes {
		return PackInfo{}, fmt.Errorf("The Blackbox program is over %d MB, so it is not the program Unravis ships. Reinstall Unravis.", MaxPackBytes>>20)
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	stamp := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	add := func(name string, data []byte, method uint16) error {
		h := &zip.FileHeader{Name: name, Method: method, Modified: stamp}
		h.SetMode(0o644)
		fw, err := zw.CreateHeader(h)
		if err != nil {
			return err
		}
		_, err = fw.Write(data)
		return err
	}
	if err := add(PackExeName, exe, zip.Deflate); err != nil {
		return PackInfo{}, err
	}
	if err := add(PackConfigName, cfgBytes, zip.Deflate); err != nil {
		return PackInfo{}, err
	}
	if err := add(PackReadmeName, []byte(PackReadme(cfg)), zip.Deflate); err != nil {
		return PackInfo{}, err
	}
	if err := zw.Close(); err != nil {
		return PackInfo{}, err
	}
	if buf.Len() > MaxPackBytes {
		return PackInfo{}, fmt.Errorf("The pack would be %d MB, over the %d MB limit.", buf.Len()>>20, MaxPackBytes>>20)
	}
	if _, err := w.Write(buf.Bytes()); err != nil {
		return PackInfo{}, err
	}
	return PackInfo{FileName: PackFileName(cfg.Name), Bytes: int64(buf.Len()), Name: cfg.Name}, nil
}

// PackReadme is the README of the pack, written for the Blackbox it carries.
func PackReadme(cfg Config) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Unravis Blackbox: %s\r\n", cfg.Name)
	b.WriteString("====================\r\n\r\n")
	fmt.Fprintf(&b, "This Blackbox records CPU, memory, disk, network and the busiest processes of\r\n")
	fmt.Fprintf(&b, "the server every %d seconds and keeps the last %d days. It only reads the server it\r\n", cfg.IntervalSeconds, cfg.KeepDays)
	b.WriteString("runs on. It does not record request bodies, passwords or the content of log files.\r\n\r\n")
	b.WriteString("Set up (about two minutes)\r\n--------------------------\r\n")
	fmt.Fprintf(&b, "1. Copy this folder to the server %s and unzip it there.\r\n", cfg.Name)
	b.WriteString("2. Open PowerShell as an administrator in the unzipped folder.\r\n")
	b.WriteString("3. Run:   .\\unravis-blackbox.exe install\r\n")
	b.WriteString("   The service UnravisBlackbox starts at boot. It makes the READ KEY and prints it\r\n")
	b.WriteString("   once. Copy it now: only a fingerprint is kept on the server, and the key is not\r\n")
	b.WriteString("   in this zip.\r\n")
	fmt.Fprintf(&b, "4. Check it: open http://localhost:%d in a browser on that server. It says Running.\r\n", cfg.Port)
	b.WriteString("5. In Unravis, add the Blackbox to a case by its folder, its share or its URL and\r\n")
	b.WriteString("   port, and type the read key. Unravis saves the key sealed on its own PC and never\r\n")
	b.WriteString("   shows it again.\r\n\r\n")
	b.WriteString("Settings in this pack (blackbox.json)\r\n-------------------------------------\r\n")
	fmt.Fprintf(&b, "Recordings folder: %s\r\n", cfg.Folder)
	fmt.Fprintf(&b, "Keep: %d days, at most %.1f GB, never below %d%% free space\r\n", cfg.KeepDays, cfg.MaxGB, cfg.MinFreePercent)
	fmt.Fprintf(&b, "Status page: port %d, listening on %s\r\n", cfg.Port, cfg.Bind)
	if cfg.Bind == DefaultBind {
		b.WriteString("The status page answers on this server only until you opt in. To let Unravis on\r\n")
		b.WriteString("another PC read it, set \"bind\" in blackbox.json to the server's address, run\r\n")
		fmt.Fprintf(&b, "\"unravis-blackbox apply\", and add a Windows Firewall rule for port %d that allows\r\n", cfg.Port)
		b.WriteString("only the Unravis PC. A folder or a share needs no firewall rule: it uses Windows\r\n")
		b.WriteString("permissions.\r\n")
	}
	b.WriteString("\r\nOther commands\r\n--------------\r\n")
	b.WriteString("    unravis-blackbox stop        stop recording (the recordings are kept)\r\n")
	b.WriteString("    unravis-blackbox start       start again\r\n")
	b.WriteString("    unravis-blackbox apply       read blackbox.json again and restart\r\n")
	b.WriteString("    unravis-blackbox snapshot    copy the recordings to <folder>\\snapshots\r\n")
	b.WriteString("    unravis-blackbox remove      remove the service; the recordings stay\r\n")
	fmt.Fprintf(&b, "    unravis-blackbox remove --delete-recordings --confirm-name %s\r\n", cfg.Name)
	b.WriteString("                                 remove the service and delete the recordings\r\n")
	return b.String()
}
