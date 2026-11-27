package history

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/sealedfile"
)

const (
	MaxRecords     = 20
	maxFileSize    = 1 << 20
	maxLabelLength = 120
)

var (
	ErrUnavailable = errors.New("encrypted connection history is unavailable")
	ErrCorrupt     = errors.New("saved connection history could not be read")
	ErrNotFound    = errors.New("saved connection was not found")
	// ErrFull means MaxRecords connections are already saved and this one is new. Nothing is removed to make room: a saved password is the
	// owner's to forget.
	ErrFull = errors.New("the list of saved connections is full")
	// ErrNotDamaged means SetAside was asked for a file that reads fine (or is not there). Only an unreadable file may be set aside.
	ErrNotDamaged = errors.New("the saved connections file is not damaged")
	// ErrSetAsideFull means MaxSetAside damaged copies are already kept. None is removed to make room: the person moves them away.
	ErrSetAsideFull = errors.New("too many damaged saved connections files are already set aside")
)

// MaxSetAside is how many set-aside copies are kept next to the live file. A fourth is refused, and none is ever deleted for the person.
const MaxSetAside = 3

// setAsideInfix joins the file name and the time stamp of a set-aside copy: connections.dat.unreadable-20261127-143012.
const setAsideInfix = ".unreadable-"

// Recent contains only fields that are safe to return to the browser.
type Recent struct {
	ID             string    `json:"id"`
	Label          string    `json:"label"`
	URI            string    `json:"uri"`
	Username       string    `json:"username"`
	AuthSource     string    `json:"authSource"`
	TLSMode        string    `json:"tlsMode"`
	CACertificate  string    `json:"caCertificate"`
	Collection     string    `json:"collection"`
	HasCredentials bool      `json:"hasCredentials"`
	LastUsedAt     time.Time `json:"lastUsedAt"`
}

type storedRecord struct {
	Recent
	Password string `json:"password,omitempty"`
}

type vault struct {
	Version int            `json:"version"`
	Records []storedRecord `json:"records"`
}

// Protector encrypts and decrypts the complete history document.
type Protector = sealedfile.Protector

type Service struct {
	path           string
	protector      Protector
	mu             sync.Mutex
	sessionMu      sync.Mutex
	sessionSecrets map[string]sessionCredential
	// now is the clock of the set-aside name; tests replace it.
	now func() time.Time
}

type sessionCredential struct {
	address  Recent
	password string
}

func New(path string, protector Protector) (*Service, error) {
	if path == "" || protector == nil {
		return nil, ErrUnavailable
	}
	return &Service{path: path, protector: protector, sessionSecrets: make(map[string]sessionCredential), now: time.Now}, nil
}

// NewDefault uses the current user's configuration folder. AUDIT_CONFIG_DIR is
// intended for isolated fixtures and relocates the whole application folder.
func NewDefault(getenv func(string) string) (*Service, error) {
	base := ""
	if getenv != nil {
		base = getenv("AUDIT_CONFIG_DIR")
	}
	if base == "" {
		var err error
		base, err = os.UserConfigDir()
		if err != nil {
			return nil, ErrUnavailable
		}
		// The product is Unravis, but this folder keeps its original name on purpose.
		// Renaming it would orphan the DPAPI-encrypted saved connections, and an old
		// and a new build could write the same file during an upgrade. The
		// single-instance mutex and the localStorage key follow the same rule.
		base = filepath.Join(base, "APIAuditExplorer")
	}
	protector, err := currentUserProtector()
	if err != nil {
		return nil, ErrUnavailable
	}
	return New(filepath.Join(base, "connections.dat"), protector)
}

func (s *Service) List() ([]Recent, error) {
	v, err := s.load()
	if err != nil {
		return nil, err
	}
	out := make([]Recent, len(v.Records))
	for i := range v.Records {
		out[i] = v.Records[i].Recent
	}
	return out, nil
}

func (s *Service) Remember(label string, input audit.ConnectionInput, collection string, rememberPassword bool) (Recent, error) {
	label = strings.TrimSpace(label)
	if label == "" || len(label) > maxLabelLength || strings.TrimSpace(collection) == "" || !safeAddress(input) {
		return Recent{}, audit.ErrInvalidInput
	}
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	result := storedRecord{Recent: Recent{
		Label: label, URI: strings.TrimSpace(input.URI), Username: input.Username,
		AuthSource: input.AuthSource, TLSMode: input.TLSMode, CACertificate: input.CACertificate,
		Collection: strings.TrimSpace(collection), HasCredentials: rememberPassword && input.Password != "",
		LastUsedAt: time.Now().UTC(),
	}}
	if rememberPassword {
		result.Password = input.Password
	}
	id, err := newID()
	if err != nil {
		return Recent{}, err
	}
	result.ID = id
	requestedID := input.SavedConnectionID
	if err := s.mutate(func(v *vault) error {
		if requestedID != "" {
			found := false
			for _, prior := range v.Records {
				if prior.ID == requestedID {
					if !sameInput(prior.Recent, audit.ConnectionInput{URI: input.URI, Username: input.Username, AuthSource: input.AuthSource, TLSMode: input.TLSMode, CACertificate: input.CACertificate}) {
						return audit.ErrInvalidInput
					}
					found = true
					break
				}
			}
			if !found {
				return ErrNotFound
			}
		}
		for i, prior := range v.Records {
			if sameAddress(prior.Recent, result.Recent) {
				result.ID = prior.ID
				v.Records = append(v.Records[:i], v.Records[i+1:]...)
				break
			}
		}
		if len(v.Records) >= MaxRecords {
			return ErrFull
		}
		v.Records = append([]storedRecord{result}, v.Records...)
		s.pruneSessionSecrets(v.Records)
		return nil
	}); err != nil {
		return Recent{}, err
	}
	if !rememberPassword && input.Password != "" {
		s.sessionSecrets[result.ID] = sessionCredential{address: result.Recent, password: input.Password}
	} else {
		delete(s.sessionSecrets, result.ID)
	}
	return result.Recent, nil
}

// Resolve restores a saved password only when every public connection field
// matches. An explicitly entered password takes precedence.
func (s *Service) Resolve(input audit.ConnectionInput) (audit.ConnectionInput, error) {
	if input.SavedConnectionID == "" {
		return input, nil
	}
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	v, err := s.load()
	if err != nil {
		return audit.ConnectionInput{}, err
	}
	for _, record := range v.Records {
		if record.ID != input.SavedConnectionID {
			continue
		}
		if !sameInput(record.Recent, input) {
			return audit.ConnectionInput{}, audit.ErrInvalidInput
		}
		if input.Password == "" {
			input.Password = record.Password
			if input.Password == "" {
				if cached, ok := s.sessionSecrets[record.ID]; ok && sameInput(cached.address, input) {
					input.Password = cached.password
				}
			}
		}
		input.SavedConnectionID = ""
		return input, nil
	}
	return audit.ConnectionInput{}, ErrNotFound
}

func (s *Service) Delete(id string) error {
	if id == "" {
		return audit.ErrInvalidInput
	}
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	found := false
	err := s.mutate(func(v *vault) error {
		for i, record := range v.Records {
			if record.ID == id {
				v.Records = append(v.Records[:i], v.Records[i+1:]...)
				found = true
				break
			}
		}
		s.pruneSessionSecrets(v.Records)
		return nil
	})
	if err != nil {
		delete(s.sessionSecrets, id)
		return err
	}
	delete(s.sessionSecrets, id)
	if !found {
		return ErrNotFound
	}
	return nil
}

// SetAside is the one way out of a saved connections file that cannot be read. It is refused unless the file is read again, under the
// file lock, and is still unreadable (ErrCorrupt): a healthy file, a missing file or a read that failed for another reason (ErrUnavailable)
// is never touched. The unreadable file is renamed, byte for byte and never deleted, to connections.dat.unreadable-YYYYMMDD-HHMMSS in
// the same folder, and the list starts empty. At most MaxSetAside copies are kept: a fourth is refused with ErrSetAsideFull and
// older copies are never removed. It returns the base name of the kept copy. Passwords typed for this session only are dropped with the
// list, because their links are gone.
func (s *Service) SetAside() (string, error) {
	s.sessionMu.Lock()
	defer s.sessionMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := acquireVaultLock(s.path)
	if err != nil {
		return "", err
	}
	defer unlock()
	if _, err := s.readLocked(); !errors.Is(err, ErrCorrupt) {
		if err == nil {
			return "", ErrNotDamaged
		}
		return "", err
	}
	dir, base := filepath.Split(s.path)
	entries, err := os.ReadDir(dirOrDot(dir))
	if err != nil {
		return "", ErrUnavailable
	}
	kept := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), base+setAsideInfix) {
			kept++
		}
	}
	if kept >= MaxSetAside {
		return "", ErrSetAsideFull
	}
	clock := s.now
	if clock == nil {
		clock = time.Now
	}
	stamp := clock().Format("20060102-150405")
	// os.Rename replaces an existing target on Windows, so the name is checked first and a second set-aside in the same second gets a suffix.
	target := ""
	for n := 0; n < 100; n++ {
		name := base + setAsideInfix + stamp
		if n > 0 {
			name = fmt.Sprintf("%s-%d", name, n+1)
		}
		if _, err := os.Lstat(filepath.Join(dir, name)); errors.Is(err, os.ErrNotExist) {
			target = name
			break
		} else if err != nil {
			return "", ErrUnavailable
		}
	}
	if target == "" {
		return "", ErrUnavailable
	}
	if err := os.Rename(s.path, filepath.Join(dir, target)); err != nil {
		return "", ErrUnavailable
	}
	for id := range s.sessionSecrets {
		delete(s.sessionSecrets, id)
	}
	return target, nil
}

func dirOrDot(dir string) string {
	if dir == "" {
		return "."
	}
	return dir
}

func (s *Service) pruneSessionSecrets(records []storedRecord) {
	allowed := make(map[string]struct{}, len(records))
	for _, record := range records {
		allowed[record.ID] = struct{}{}
	}
	for id := range s.sessionSecrets {
		if _, ok := allowed[id]; !ok {
			delete(s.sessionSecrets, id)
		}
	}
}

func safeAddress(input audit.ConnectionInput) bool {
	value := strings.TrimSpace(input.URI)
	if value == "" {
		return false
	}
	if !strings.Contains(value, "://") {
		if strings.ContainsAny(value, `/\\`) {
			return false
		}
		value = "mongodb://" + value
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "mongodb" && parsed.Scheme != "mongodb+srv") || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return false
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		return false
	}
	for key, values := range query {
		normalizedKey := strings.ToLower(strings.TrimSpace(key))
		switch normalizedKey {
		case "password", "passwd", "pwd", "proxypassword", "tlsclientcertificatekeypassword", "tlscertificatekeyfilepassword", "sslclientcertificatekeypassword":
			return false
		case "authmechanismproperties":
			for _, value := range values {
				for _, pair := range strings.Split(value, ",") {
					property, _, found := strings.Cut(pair, ":")
					property = strings.ToLower(strings.TrimSpace(property))
					if !found || property == "" || strings.Contains(property, "password") || strings.Contains(property, "token") || strings.Contains(property, "secret") || strings.Contains(property, "credential") {
						return false
					}
				}
			}
		}
	}
	return true
}

func sameAddress(a, b Recent) bool {
	return a.URI == b.URI && a.Username == b.Username && a.AuthSource == b.AuthSource && a.TLSMode == b.TLSMode && a.CACertificate == b.CACertificate && a.Collection == b.Collection
}

func sameInput(saved Recent, input audit.ConnectionInput) bool {
	return saved.URI == strings.TrimSpace(input.URI) && saved.Username == input.Username && saved.AuthSource == input.AuthSource && saved.TLSMode == input.TLSMode && saved.CACertificate == input.CACertificate
}

func (s *Service) load() (vault, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := acquireVaultLock(s.path)
	if err != nil {
		return vault{}, err
	}
	defer unlock()
	return s.readLocked()
}

func (s *Service) mutate(change func(*vault) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := acquireVaultLock(s.path)
	if err != nil {
		return err
	}
	defer unlock()
	v, err := s.readLocked()
	if err != nil {
		return err
	}
	if err := change(&v); err != nil {
		return err
	}
	return s.writeLocked(v)
}

func (s *Service) readLocked() (vault, error) {
	file, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return vault{Version: 1, Records: []storedRecord{}}, nil
	}
	if err != nil {
		return vault{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxFileSize+1))
	if err != nil {
		return vault{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if len(data) == 0 || len(data) > maxFileSize {
		return vault{}, ErrCorrupt
	}
	plain, err := s.protector.Unprotect(data)
	if err != nil || len(plain) == 0 || len(plain) > maxFileSize {
		return vault{}, ErrCorrupt
	}
	var v vault
	decoder := json.NewDecoder(bytes.NewReader(plain))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&v) != nil || v.Version != 1 || v.Records == nil || len(v.Records) > MaxRecords {
		return vault{}, ErrCorrupt
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return vault{}, ErrCorrupt
	}
	seen := make(map[string]struct{}, len(v.Records))
	for _, record := range v.Records {
		if record.ID == "" || len(record.ID) > 64 || record.Label == "" || len(record.Label) > maxLabelLength || strings.TrimSpace(record.Collection) == "" ||
			!safeAddress(audit.ConnectionInput{URI: record.URI}) || record.HasCredentials != (record.Password != "") {
			return vault{}, ErrCorrupt
		}
		if _, exists := seen[record.ID]; exists {
			return vault{}, ErrCorrupt
		}
		seen[record.ID] = struct{}{}
	}
	return v, nil
}

func (s *Service) writeLocked(v vault) error {
	if len(v.Records) > MaxRecords {
		return ErrCorrupt
	}
	plain, err := json.Marshal(v)
	if err != nil || len(plain) > maxFileSize {
		return ErrCorrupt
	}
	ciphertext, err := s.protector.Protect(plain)
	if err != nil || len(ciphertext) == 0 || len(ciphertext) > maxFileSize {
		return ErrUnavailable
	}
	if err := sealedfile.WriteAtomic(s.path, ".connections-*.tmp", ciphertext); err != nil {
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

// Record is one saved link with its stored password. Only the move into the key keeper (P14) handles it; it is never sent to the browser.
type Record = storedRecord

// Export returns every saved link, password included, for the move into the key keeper.
func (s *Service) Export() ([]Record, error) {
	v, err := s.load()
	if err != nil {
		return nil, err
	}
	return v.Records, nil
}

// Import adds links, keeping their ids, so a page that holds an old id still finds it. A link whose id or whose address is already saved is
// skipped. It returns how many were added. ErrFull means the result would hold more than MaxRecords; nothing is written then.
func (s *Service) Import(records []Record) (int, error) {
	added := 0
	err := s.mutate(func(v *vault) error {
	next:
		for _, rec := range records {
			if rec.ID == "" || len(rec.ID) > 64 || rec.Label == "" || len(rec.Label) > maxLabelLength || strings.TrimSpace(rec.Collection) == "" ||
				!safeAddress(audit.ConnectionInput{URI: rec.URI}) || rec.HasCredentials != (rec.Password != "") {
				return audit.ErrInvalidInput
			}
			for _, prior := range v.Records {
				if prior.ID == rec.ID || sameAddress(prior.Recent, rec.Recent) {
					continue next
				}
			}
			v.Records = append(v.Records, rec)
			added++
		}
		if len(v.Records) > MaxRecords {
			return ErrFull
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return added, nil
}

// Path is the sealed file this service reads.
func (s *Service) Path() string { return s.path }
