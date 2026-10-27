// Package blackbox is the recorder behind unravis-blackbox.exe: a small service
// that samples the host every 15 seconds, keeps the samples as a manifest plus
// hourly NDJSON files, cleans up after itself, and answers status on a port
// behind a read key. It only reads the machine it runs on.
//
// Everything that touches the operating system sits behind an interface
// (Collector, DiskSpace, ServiceControl, Env), so the logic is tested with fakes
// and the recorder can also run as a normal foreground process.
package blackbox

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ServiceName is the Windows service the recorder runs as.
const ServiceName = "UnravisBlackbox"

// Defaults (decision A37: housekeeping cannot be turned off).
const (
	DefaultFolder         = `D:\UnravisBlackbox`
	FallbackFolder        = `C:\UnravisBlackbox` // when the machine has no D: drive
	DefaultIntervalSecond = 15
	DefaultKeepDays       = 7
	DefaultMaxGB          = 2.0
	DefaultMinFreePercent = 10
	DefaultPort           = 7788
	DefaultBind           = "0.0.0.0"
)

// Limits on what blackbox.json may say. They bound the values from below too:
// the limits can be lowered but never switched off.
const (
	maxKeepDays    = 365
	minMaxGB       = 0.1
	maxMaxGB       = 10000
	maxMinFree     = 50
	maxLogPaths    = 20
	maxNameLen     = 63
	maxConfigBytes = 1 << 20
)

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*$`)

// Record says what each sample holds.
type Record struct {
	// Processes records the top 20 processes by CPU. Without them the web server
	// process row in Step 5 says Cannot test yet.
	Processes bool `json:"processes"`
	// LogsCatalogue records names, sizes and times of the IIS and application
	// logs. Never their content.
	LogsCatalogue bool `json:"logsCatalogue"`
	// CopyLogsOnSnapshot copies the log files of the snapshot window when a
	// snapshot is taken. Nothing is copied at any other time.
	CopyLogsOnSnapshot bool `json:"copyLogsOnSnapshot"`
}

// Probe is the SQL probe block. The probe is not part of Release 1: the block
// stays in the file, off and empty, so a later release can fill it.
type Probe struct {
	Enabled bool `json:"enabled"`
}

// Config is blackbox.json.
type Config struct {
	Name            string   `json:"name"`
	Folder          string   `json:"folder"`
	IntervalSeconds int      `json:"intervalSeconds"`
	KeepDays        int      `json:"keepDays"`
	MaxGB           float64  `json:"maxGB"`
	MinFreePercent  int      `json:"minFreePercent"`
	Port            int      `json:"port"`
	Bind            string   `json:"bind"`
	Record          Record   `json:"record"`
	LogPaths        []string `json:"logPaths"`
	Probe           Probe    `json:"probe"`
}

// DefaultLogPaths are the folders the logs catalogue looks in when blackbox.json
// names none.
func DefaultLogPaths() []string {
	return []string{`C:\inetpub\logs\LogFiles`}
}

// Default returns the settings of a new Blackbox. The name is empty: the caller
// picks one (normally the server name).
func Default() Config {
	return Config{
		Folder:          DefaultFolder,
		IntervalSeconds: DefaultIntervalSecond,
		KeepDays:        DefaultKeepDays,
		MaxGB:           DefaultMaxGB,
		MinFreePercent:  DefaultMinFreePercent,
		Port:            DefaultPort,
		Bind:            DefaultBind,
		Record:          Record{Processes: true, LogsCatalogue: true},
		LogPaths:        DefaultLogPaths(),
	}
}

// ChooseFolder returns D:\UnravisBlackbox, or C:\UnravisBlackbox when driveExists
// says there is no D: drive.
func ChooseFolder(driveExists func(root string) bool) string {
	if driveExists != nil && driveExists(`D:\`) {
		return DefaultFolder
	}
	return FallbackFolder
}

// MaxBytes is the size limit in bytes.
func (c Config) MaxBytes() int64 { return int64(c.MaxGB * (1 << 30)) }

// ErrInvalidConfig wraps every validation problem so callers can tell a bad file
// from an unreadable one.
var ErrInvalidConfig = errors.New("blackbox.json is not valid")

func invalidf(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidConfig, fmt.Sprintf(format, a...))
}

// Validate checks every field and says which one is wrong. Housekeeping limits
// below their minimum are refused, not clamped: the limits cannot be turned off.
func (c Config) Validate() error {
	if c.Name == "" || len(c.Name) > maxNameLen || !nameRe.MatchString(c.Name) {
		return invalidf(`"name" must be 1 to %d letters, numbers or dashes, starting with a letter or number`, maxNameLen)
	}
	if strings.TrimSpace(c.Folder) == "" || !filepath.IsAbs(c.Folder) {
		return invalidf(`"folder" must be a full path such as %s`, DefaultFolder)
	}
	switch c.IntervalSeconds {
	case 5, 15, 30, 60:
	default:
		return invalidf(`"intervalSeconds" must be 5, 15, 30 or 60`)
	}
	if c.KeepDays < 1 || c.KeepDays > maxKeepDays {
		return invalidf(`"keepDays" must be 1 to %d: housekeeping cannot be turned off`, maxKeepDays)
	}
	if c.MaxGB < minMaxGB || c.MaxGB > maxMaxGB {
		return invalidf(`"maxGB" must be %.1f to %d: housekeeping cannot be turned off`, minMaxGB, maxMaxGB)
	}
	if c.MinFreePercent < 1 || c.MinFreePercent > maxMinFree {
		return invalidf(`"minFreePercent" must be 1 to %d: housekeeping cannot be turned off`, maxMinFree)
	}
	if c.Port < 1 || c.Port > 65535 {
		return invalidf(`"port" must be 1 to 65535`)
	}
	if c.Bind == "" {
		return invalidf(`"bind" must be an address such as 0.0.0.0 or 127.0.0.1`)
	}
	if len(c.LogPaths) > maxLogPaths {
		return invalidf(`"logPaths" may list at most %d folders`, maxLogPaths)
	}
	for _, p := range c.LogPaths {
		if !filepath.IsAbs(p) {
			return invalidf(`"logPaths" entry %q must be a full path`, p)
		}
	}
	if c.Probe.Enabled {
		return invalidf(`"probe.enabled" must be false: the SQL probe is not part of this release`)
	}
	return nil
}

// ParseConfig reads blackbox.json text. A UTF-8 byte order mark (which
// PowerShell's Out-File adds) is ignored. Unknown fields are refused, so a typo
// cannot silently leave a limit at its default. Fields that are missing take
// their defaults, except the name, which has none.
func ParseConfig(data []byte) (Config, error) {
	if len(data) > maxConfigBytes {
		return Config{}, invalidf("the file is too large")
	}
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	c := Default()
	c.LogPaths = nil
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return Config{}, invalidf("%v", err)
	}
	if dec.More() {
		return Config{}, invalidf("unexpected text after the closing brace")
	}
	if c.LogPaths == nil {
		c.LogPaths = DefaultLogPaths()
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// LoadConfig reads and validates the file at path.
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	return ParseConfig(data)
}

// Marshal is the text of blackbox.json: indented, with a final newline.
func (c Config) Marshal() ([]byte, error) {
	out, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// SaveConfig validates c and writes it to path through a temporary file, so a
// crash never leaves half a file. When keepBackup is set and a file is already
// there, the old one is copied to path+".bak" first.
func SaveConfig(path string, c Config, keepBackup bool) error {
	if err := c.Validate(); err != nil {
		return err
	}
	data, err := c.Marshal()
	if err != nil {
		return err
	}
	if keepBackup {
		if old, err := os.ReadFile(path); err == nil {
			if err := os.WriteFile(path+".bak", old, 0o644); err != nil {
				return err
			}
		}
	}
	return writeFileAtomic(path, data, 0o644)
}

// writeFileAtomic writes data to a temporary file next to path, flushes it, and
// renames it over path.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			tmp.Close()
			os.Remove(name)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, perm); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	ok = true
	return nil
}
