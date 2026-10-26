// Package sqlconn keeps the SQL Server connection details a person types in the create wizard, the way SQL Server Management Studio does:
// server, login, optionally the password, database and encryption choices. They are only stored for now; nothing connects with them yet.
// The file is sealed for the current user (DPAPI on Windows) and holds the password only when the person asked to remember it.
package sqlconn

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"api-audit-explorer/internal/sealedfile"
)

const (
	MaxRecords  = 20
	maxFileSize = 1 << 20
	maxText     = 200
)

var (
	ErrUnavailable = errors.New("saved SQL Server connections are unavailable")
	ErrCorrupt     = errors.New("saved SQL Server connections could not be read")
	ErrNotFound    = errors.New("saved SQL Server connection was not found")
	ErrInvalid     = errors.New("SQL Server connection details are invalid")
)

const (
	AuthSQL     = "sql"
	AuthWindows = "windows"

	EncryptMandatory = "mandatory"
	EncryptOptional  = "optional"
	EncryptStrict    = "strict"
)

// Input is what the browser sends. Password is never returned.
type Input struct {
	Label                  string `json:"label"`
	Server                 string `json:"server"`
	Auth                   string `json:"auth"`
	Username               string `json:"username"`
	Password               string `json:"password"`
	RememberPassword       bool   `json:"rememberPassword"`
	Database               string `json:"database"`
	Encrypt                string `json:"encrypt"`
	TrustServerCertificate bool   `json:"trustServerCertificate"`
}

// Recent is the part safe to return to the browser.
type Recent struct {
	ID                     string    `json:"id"`
	Label                  string    `json:"label"`
	Server                 string    `json:"server"`
	Host                   string    `json:"host"`
	Port                   int       `json:"port"`
	Auth                   string    `json:"auth"`
	Username               string    `json:"username"`
	Database               string    `json:"database"`
	Encrypt                string    `json:"encrypt"`
	TrustServerCertificate bool      `json:"trustServerCertificate"`
	HasPassword            bool      `json:"hasPassword"`
	SavedAt                time.Time `json:"savedAt"`
}

type stored struct {
	Recent
	Password string `json:"password,omitempty"`
}

type vault struct {
	Version int      `json:"version"`
	Records []stored `json:"records"`
}

type Service struct {
	path      string
	protector sealedfile.Protector
	mu        sync.Mutex
}

func New(path string, protector sealedfile.Protector) (*Service, error) {
	if path == "" || protector == nil {
		return nil, ErrUnavailable
	}
	return &Service{path: path, protector: protector}, nil
}

// NewDefault sits beside the saved MongoDB connections (same folder, same sealing).
func NewDefault(getenv func(string) string) (*Service, error) {
	base := ""
	if getenv != nil {
		base = getenv("AUDIT_CONFIG_DIR")
	}
	if base == "" {
		var err error
		if base, err = os.UserConfigDir(); err != nil {
			return nil, ErrUnavailable
		}
		base = filepath.Join(base, "APIAuditExplorer")
	}
	protector, err := sealedfile.CurrentUser()
	if err != nil {
		return nil, ErrUnavailable
	}
	return New(filepath.Join(base, "sqlconnections.dat"), protector)
}

// SplitServer reads SSMS "Server name": "host", "host,port", "host:port" or "tcp:host,port". A named instance ("host\SQL") keeps its name in
// the host and uses the default port; looking the instance port up is for the day the connection is made. Port 0 means the default 1433.
func SplitServer(text string) (host string, port int, ok bool) {
	raw := strings.TrimSpace(text)
	raw = strings.TrimPrefix(strings.TrimPrefix(raw, "tcp:"), "TCP:")
	if raw == "" || len(raw) > maxText {
		return "", 0, false
	}
	hostPart, portPart := raw, ""
	if i := strings.LastIndexAny(raw, ",:"); i >= 0 && !strings.HasSuffix(raw, "]") {
		// A bare IPv6 address has several colons and no comma; only one separator splits host from port.
		if raw[i] == ',' || strings.Count(raw, ":") == 1 {
			hostPart, portPart = strings.TrimSpace(raw[:i]), strings.TrimSpace(raw[i+1:])
		}
	}
	hostPart = strings.Trim(hostPart, "[]")
	if hostPart == "" || strings.ContainsFunc(hostPart, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) || strings.ContainsRune(`/;=@'"`, r) }) {
		return "", 0, false
	}
	if portPart != "" {
		n, err := strconv.Atoi(portPart)
		if err != nil || n < 1 || n > 65535 {
			return "", 0, false
		}
		port = n
	}
	return hostPart, port, true
}

func clean(text string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, text))
}

func validate(in Input) (stored, error) {
	host, port, ok := SplitServer(in.Server)
	if !ok {
		return stored{}, ErrInvalid
	}
	if port == 0 {
		port = 1433
	}
	rec := stored{Recent: Recent{
		Server: net.JoinHostPort(host, strconv.Itoa(port)), Host: host, Port: port, Auth: in.Auth,
		Username: clean(in.Username), Database: clean(in.Database), Encrypt: in.Encrypt, TrustServerCertificate: in.TrustServerCertificate,
	}}
	// "host,port" is how the person wrote it; keep that spelling for the list.
	if strings.Contains(in.Server, ",") {
		rec.Server = host + "," + strconv.Itoa(port)
	}
	if rec.Encrypt == "" {
		rec.Encrypt = EncryptMandatory
	}
	switch rec.Auth {
	case AuthSQL:
		if rec.Username == "" || len(rec.Username) > maxText || len(in.Password) > 512 {
			return stored{}, ErrInvalid
		}
	case AuthWindows:
		rec.Username = ""
	default:
		return stored{}, ErrInvalid
	}
	if rec.Encrypt != EncryptMandatory && rec.Encrypt != EncryptOptional && rec.Encrypt != EncryptStrict {
		return stored{}, ErrInvalid
	}
	if len(rec.Database) > maxText {
		return stored{}, ErrInvalid
	}
	rec.Label = clean(in.Label)
	if rec.Label == "" {
		rec.Label = rec.Server
	}
	if len(rec.Label) > maxText {
		return stored{}, ErrInvalid
	}
	if in.RememberPassword && rec.Auth == AuthSQL && in.Password != "" {
		rec.Password = in.Password
		rec.HasPassword = true
	}
	return rec, nil
}

func (s *Service) List() ([]Recent, error) {
	v, err := s.read()
	if err != nil {
		return nil, err
	}
	out := make([]Recent, len(v.Records))
	for i := range v.Records {
		out[i] = v.Records[i].Recent
	}
	return out, nil
}

// Save stores the details. The same server, login and database replaces its earlier entry (newest first).
func (s *Service) Save(in Input) (Recent, error) {
	rec, err := validate(in)
	if err != nil {
		return Recent{}, err
	}
	id, err := newID()
	if err != nil {
		return Recent{}, err
	}
	rec.ID, rec.SavedAt = id, time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := sealedfile.Lock(s.path)
	if err != nil {
		return Recent{}, ErrUnavailable
	}
	defer unlock()
	v, err := s.readLocked()
	if err != nil {
		return Recent{}, err
	}
	for i, prior := range v.Records {
		if prior.Host == rec.Host && prior.Port == rec.Port && prior.Username == rec.Username && prior.Auth == rec.Auth && strings.EqualFold(prior.Database, rec.Database) {
			rec.ID = prior.ID
			// A blank password with "Remember password" still ticked keeps the one stored earlier.
			if rec.Password == "" && in.RememberPassword && rec.Auth == AuthSQL && prior.Password != "" {
				rec.Password, rec.HasPassword = prior.Password, true
			}
			v.Records = append(v.Records[:i], v.Records[i+1:]...)
			break
		}
	}
	v.Records = append([]stored{rec}, v.Records...)
	if len(v.Records) > MaxRecords {
		v.Records = v.Records[:MaxRecords]
	}
	if err := s.write(v); err != nil {
		return Recent{}, err
	}
	return rec.Recent, nil
}

func (s *Service) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := sealedfile.Lock(s.path)
	if err != nil {
		return ErrUnavailable
	}
	defer unlock()
	v, err := s.readLocked()
	if err != nil {
		return err
	}
	for i, record := range v.Records {
		if record.ID == id {
			v.Records = append(v.Records[:i], v.Records[i+1:]...)
			return s.write(v)
		}
	}
	return ErrNotFound
}

func (s *Service) read() (vault, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := sealedfile.Lock(s.path)
	if err != nil {
		return vault{}, ErrUnavailable
	}
	defer unlock()
	return s.readLocked()
}

func (s *Service) readLocked() (vault, error) {
	file, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return vault{Version: 1, Records: []stored{}}, nil
	}
	if err != nil {
		return vault{}, ErrUnavailable
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxFileSize+1))
	if err != nil {
		return vault{}, ErrUnavailable
	}
	if len(data) == 0 || len(data) > maxFileSize {
		return vault{}, ErrCorrupt
	}
	plain, err := s.protector.Unprotect(data)
	if err != nil || len(plain) == 0 {
		return vault{}, ErrCorrupt
	}
	var v vault
	decoder := json.NewDecoder(bytes.NewReader(plain))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&v) != nil || v.Version != 1 || v.Records == nil || len(v.Records) > MaxRecords {
		return vault{}, ErrCorrupt
	}
	seen := map[string]struct{}{}
	for _, record := range v.Records {
		if record.ID == "" || len(record.ID) > 64 || record.Host == "" || record.HasPassword != (record.Password != "") {
			return vault{}, ErrCorrupt
		}
		if _, dup := seen[record.ID]; dup {
			return vault{}, ErrCorrupt
		}
		seen[record.ID] = struct{}{}
	}
	return v, nil
}

func (s *Service) write(v vault) error {
	plain, err := json.Marshal(v)
	if err != nil || len(plain) > maxFileSize {
		return ErrCorrupt
	}
	ciphertext, err := s.protector.Protect(plain)
	if err != nil || len(ciphertext) == 0 || len(ciphertext) > maxFileSize {
		return ErrUnavailable
	}
	if err := sealedfile.WriteAtomic(s.path, ".sqlconnections-*.tmp", ciphertext); err != nil {
		return ErrUnavailable
	}
	return nil
}

func newID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}
