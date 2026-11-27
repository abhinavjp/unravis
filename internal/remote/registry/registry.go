// Package registry keeps the servers Unravis can reach, sealed for the current user in servers.dat beside blackboxes.dat.
// A server's password or private key lives only in this file. Entry, the part the page, the CLI and code mode see, never carries a secret;
// the only way out is Secret, which only the transport code that signs in calls. Every field is checked with internal/remote/grammar on the way in
// and again on the way out of the file, so a damaged or hand-edited file cannot smuggle in an address, user or path the grammar refuses.
package registry

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"api-audit-explorer/internal/remote/grammar"
	"api-audit-explorer/internal/sealedfile"
)

// Errors of the store. The text is shown to people, so it is plain.
var (
	ErrUnavailable = errors.New("The saved server list is unavailable on this PC.")
	ErrCorrupt     = errors.New("The saved server list could not be read (damaged, or sealed for another user). It was left as it is.")
	ErrNotFound    = errors.New("That server is not in the list.")
	ErrDuplicate   = errors.New("That server is already in the list.")
	ErrInvalid     = errors.New("The server details are not valid.")
	ErrTooMany     = errors.New("The list is full. Remove a server before adding another.")
	ErrHostKeySet  = errors.New("This server already has a pinned host key. Use the key-changed flow to replace it.")
	ErrNoHostKey   = errors.New("This server has no pinned host key yet.")
)

// MaxEntries is the most servers the list holds.
const MaxEntries = 50

const (
	maxFileSize   = 1 << 20
	maxText       = 200
	maxKeyBytes   = 16 << 10
	maxPassword   = 256
	maxFolders    = 40
	maxMinions    = 20
	defaultSSHTCP = 22
)

// Auth kinds.
const (
	AuthKey      = "key"
	AuthPassword = "password"
)

// Transports a server may enable.
const (
	TransportSSH      = "ssh"
	TransportSalt     = "salt"
	TransportBlackbox = "blackbox-http"
	TransportFolder   = "folder"
)

// Folder kinds an allowlist is grouped by.
const (
	FolderBlackbox = "blackbox"
	FolderIIS      = "iis"
	FolderRedis    = "redis"
	FolderSQL      = "sql"
	FolderOther    = "other"
)

var (
	authKinds   = map[string]bool{AuthKey: true, AuthPassword: true}
	transports  = map[string]bool{TransportSSH: true, TransportSalt: true, TransportBlackbox: true, TransportFolder: true}
	folderKinds = map[string]bool{FolderBlackbox: true, FolderIIS: true, FolderRedis: true, FolderSQL: true, FolderOther: true}
	osKinds     = map[string]bool{"windows": true, "linux": true}
)

// Entry is what the page sees. It has no secret.
type Entry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Host string `json:"host"`
	Port int    `json:"port"`
	User string `json:"user"`
	// OS is windows or linux; it decides how paths are read.
	OS   string `json:"os"`
	Auth string `json:"auth"`
	// HasSecret says a password or private key is sealed here. The secret itself is never in Entry.
	HasSecret bool `json:"hasSecret"`
	// HostKey is the pinned SHA256 fingerprint. Empty until the person has compared and pinned it on first connect.
	HostKey         string     `json:"hostKey,omitempty"`
	HostKeyPinnedAt *time.Time `json:"hostKeyPinnedAt,omitempty"`
	Transports      []string   `json:"transports"`
	// MinionIDs are exact Salt minion ids (no wildcards).
	MinionIDs []string `json:"minionIds,omitempty"`
	// SaltSudo runs the jump host's salt commands with `sudo -n` (Linux). Many masters let only root or a salt group run them.
	SaltSudo bool `json:"saltSudo,omitempty"`
	// Folders is the allowlist of browsable folders, grouped by kind.
	Folders map[string][]string `json:"folders,omitempty"`
	// BlackboxAddr is the recorder status address as seen from the server itself (default 127.0.0.1:7790).
	BlackboxAddr string `json:"blackboxAddr,omitempty"`
	// BlackboxID points at the bbsaved entry that holds the read key, so a key is stored once.
	BlackboxID string    `json:"blackboxId,omitempty"`
	AddedAt    time.Time `json:"addedAt"`
}

// AddInput is a server to add. Secret is a password or a private key in OpenSSH or PEM text; Passphrase unlocks an encrypted private key.
type AddInput struct {
	Name         string
	Host         string
	Port         int
	User         string
	OS           string
	Auth         string
	Secret       string
	Passphrase   string
	Transports   []string
	MinionIDs    []string
	SaltSudo     bool
	Folders      map[string][]string
	BlackboxAddr string
	BlackboxID   string
}

type stored struct {
	Entry
	Secret     string `json:"secret,omitempty"`
	Passphrase string `json:"passphrase,omitempty"`
}

type vault struct {
	Version int      `json:"version"`
	Records []stored `json:"records"`
}

// Store is the sealed list. Safe for concurrent use.
type Store struct {
	path      string
	protector sealedfile.Protector
	mu        sync.Mutex
	now       func() time.Time
}

// New opens the list at path. A missing file is an empty list and writes nothing.
func New(path string, protector sealedfile.Protector) (*Store, error) {
	if path == "" || protector == nil {
		return nil, ErrUnavailable
	}
	return &Store{path: path, protector: protector, now: time.Now}, nil
}

// SetClock replaces the clock (tests).
func (s *Store) SetClock(now func() time.Time) {
	if now != nil {
		s.now = now
	}
}

// NewDefault sits beside blackboxes.dat in the per-user data folder and is sealed the same way.
func NewDefault(getenv func(string) string) (*Store, error) {
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
	return New(filepath.Join(base, "servers.dat"), protector)
}

// Path is the sealed file this store reads.
func (s *Store) Path() string { return s.path }

// validate checks every field of an entry and returns a plain sentence for the first problem, or "".
func validate(e Entry) string {
	name := strings.TrimSpace(e.Name)
	if name == "" || len(name) > maxText || strings.ContainsAny(name, "\r\n\x00") {
		return "Give the server a name (up to 200 characters)."
	}
	if r := grammar.Host(e.Host); r != "" {
		return r
	}
	if r := grammar.Port(e.Port); r != "" {
		return r
	}
	if !osKinds[e.OS] {
		return "Say whether the server runs Windows or Linux."
	}
	if needsSecret(e) {
		if r := grammar.User(e.User); r != "" {
			return r
		}
		if !authKinds[e.Auth] {
			return "Sign-in must be a key or a password."
		}
	} else if e.User != "" || e.Auth != "" {
		// A Blackbox-only server signs in with nothing but its read key; no account is kept for it.
		return "A Blackbox-only server has no sign-in account."
	}
	if e.HostKey != "" {
		if r := grammar.Fingerprint(e.HostKey); r != "" {
			return r
		}
	}
	if len(e.Transports) == 0 || len(e.Transports) > len(transports) {
		return "Choose at least one way to reach the server."
	}
	seen := map[string]bool{}
	for _, t := range e.Transports {
		if !transports[t] || seen[t] {
			return "That way of reaching the server is not supported."
		}
		seen[t] = true
	}
	if len(e.MinionIDs) > maxMinions {
		return "Too many server ids."
	}
	for _, m := range e.MinionIDs {
		if r := grammar.MinionID(m); r != "" {
			return r
		}
	}
	if seen[TransportSalt] && len(e.MinionIDs) == 0 {
		return "Salt needs at least one exact server id."
	}
	total := 0
	for kind, list := range e.Folders {
		if !folderKinds[kind] {
			return "That kind of folder is not supported."
		}
		for _, p := range list {
			total++
			if r := grammar.AllowedRoot(p); r != "" {
				return r
			}
		}
	}
	if total > maxFolders {
		return "Too many allowed folders."
	}
	if e.BlackboxAddr != "" && len(e.BlackboxAddr) > maxText {
		return "The Blackbox address is too long."
	}
	if e.BlackboxID != "" && len(e.BlackboxID) > 64 {
		return "The Blackbox link is not valid."
	}
	return ""
}

// needsSecret says the entry signs in over SSH or Salt, so it must hold a sealed password or key. A server reached only through its
// Blackbox recorder or a folder (the "Lite" case) keeps no secret here; its read key lives in the Blackbox store.
func needsSecret(e Entry) bool {
	for _, t := range e.Transports {
		if t == TransportSSH || t == TransportSalt {
			return true
		}
	}
	return false
}

// validSecret checks the shape of the sealed secret for the auth kind.
func validSecret(auth, secret, passphrase string) bool {
	switch auth {
	case AuthPassword:
		return secret != "" && len(secret) <= maxPassword && !strings.ContainsAny(secret, "\r\n\x00") && passphrase == ""
	case AuthKey:
		if len(secret) == 0 || len(secret) > maxKeyBytes || len(passphrase) > maxPassword || strings.ContainsRune(secret, 0) {
			return false
		}
		t := strings.TrimSpace(secret)
		return strings.HasPrefix(t, "-----BEGIN ") && strings.Contains(t, "PRIVATE KEY-----")
	}
	return false
}

func normalise(e *Entry) {
	e.Name = strings.TrimSpace(e.Name)
	e.Host = strings.ToLower(strings.TrimSpace(e.Host))
	e.User = strings.TrimSpace(e.User)
	e.BlackboxAddr = strings.TrimSpace(e.BlackboxAddr)
	if e.Port == 0 {
		e.Port = defaultSSHTCP
	}
	e.Transports = sortedUnique(e.Transports)
	e.MinionIDs = sortedUnique(e.MinionIDs)
	if len(e.Folders) > 0 {
		f := map[string][]string{}
		for k, v := range e.Folders {
			if v = sortedUnique(trimAll(v)); len(v) > 0 {
				f[k] = v
			}
		}
		e.Folders = f
	}
	if len(e.Folders) == 0 {
		e.Folders = nil
	}
}

func trimAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func sortedUnique(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	m := map[string]bool{}
	for _, s := range in {
		m[s] = true
	}
	out := make([]string, 0, len(m))
	for s := range m {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func samePlace(a Entry, host string, port int, user string) bool {
	return strings.EqualFold(a.Host, host) && a.Port == port && a.User == user
}

// Add saves a server. The same host, port and user is not added twice: Add returns the earlier entry and ErrDuplicate.
// The host key is not set here; the person pins it after comparing it on the first connect.
func (s *Store) Add(in AddInput) (Entry, error) {
	e := Entry{
		Name: in.Name, Host: in.Host, Port: in.Port, User: in.User, OS: in.OS, Auth: in.Auth,
		Transports: in.Transports, MinionIDs: in.MinionIDs, SaltSudo: in.SaltSudo, Folders: in.Folders,
		BlackboxAddr: in.BlackboxAddr, BlackboxID: in.BlackboxID,
	}
	normalise(&e)
	secret := needsSecret(e)
	if validate(e) != "" || (secret && !validSecret(e.Auth, in.Secret, in.Passphrase)) || (!secret && (in.Secret != "" || in.Passphrase != "")) {
		return Entry{}, ErrInvalid
	}
	id, err := newID()
	if err != nil {
		return Entry{}, err
	}
	e.ID, e.HasSecret, e.AddedAt = id, secret, s.now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := sealedfile.Lock(s.path)
	if err != nil {
		return Entry{}, ErrUnavailable
	}
	defer unlock()
	v, err := s.readLocked()
	if err != nil {
		return Entry{}, err
	}
	for _, r := range v.Records {
		if samePlace(r.Entry, e.Host, e.Port, e.User) {
			return r.Entry, ErrDuplicate
		}
	}
	if len(v.Records) >= MaxEntries {
		return Entry{}, ErrTooMany
	}
	v.Records = append(v.Records, stored{Entry: e, Secret: in.Secret, Passphrase: in.Passphrase})
	if err := s.write(v); err != nil {
		return Entry{}, err
	}
	return e, nil
}

// List returns the entries, oldest first. No entry carries a secret.
func (s *Store) List() ([]Entry, error) {
	v, err := s.read()
	if err != nil {
		return nil, err
	}
	out := make([]Entry, len(v.Records))
	for i := range v.Records {
		out[i] = v.Records[i].Entry
	}
	return out, nil
}

// Get returns one entry.
func (s *Store) Get(id string) (Entry, error) {
	v, err := s.read()
	if err != nil {
		return Entry{}, err
	}
	for _, r := range v.Records {
		if r.ID == id {
			return r.Entry, nil
		}
	}
	return Entry{}, ErrNotFound
}

// Secret returns the sealed password or private key (and its passphrase) for the code that signs in. It is never sent to the page,
// a log, a case file, an export or a process argument.
func (s *Store) Secret(id string) (secret, passphrase string, err error) {
	v, err := s.read()
	if err != nil {
		return "", "", err
	}
	for _, r := range v.Records {
		if r.ID == id {
			return r.Secret, r.Passphrase, nil
		}
	}
	return "", "", ErrNotFound
}

// SetSecret replaces the sealed secret (the person typed a new password or key). The auth kind may change with it.
func (s *Store) SetSecret(id, auth, secret, passphrase string) error {
	if !validSecret(auth, secret, passphrase) {
		return ErrInvalid
	}
	return s.updateChecked(id, func(r *stored) bool {
		if !needsSecret(r.Entry) {
			return false // a Blackbox-only server keeps no sign-in secret
		}
		r.Auth, r.Secret, r.Passphrase, r.HasSecret = auth, secret, passphrase, true
		return true
	})
}

// Patch is the settings a person may edit on a saved server. A nil field is left as it is.
type Patch struct {
	Name         *string
	Transports   *[]string
	MinionIDs    *[]string
	SaltSudo     *bool
	Folders      *map[string][]string
	BlackboxAddr *string
	BlackboxID   *string
}

// Update applies a Patch. The address, port, user and host key are not editable here: a different host is a different server, and the
// host key changes only through PinHostKey and ReplaceHostKey.
func (s *Store) Update(id string, p Patch) (Entry, error) {
	var out Entry
	err := s.updateChecked(id, func(r *stored) bool {
		e := r.Entry
		if p.Name != nil {
			e.Name = *p.Name
		}
		if p.Transports != nil {
			e.Transports = *p.Transports
		}
		if p.MinionIDs != nil {
			e.MinionIDs = *p.MinionIDs
		}
		if p.SaltSudo != nil {
			e.SaltSudo = *p.SaltSudo
		}
		if p.Folders != nil {
			e.Folders = *p.Folders
		}
		if p.BlackboxAddr != nil {
			e.BlackboxAddr = *p.BlackboxAddr
		}
		if p.BlackboxID != nil {
			e.BlackboxID = *p.BlackboxID
		}
		normalise(&e)
		if validate(e) != "" || (needsSecret(e) && !r.HasSecret) {
			return false
		}
		r.Entry, out = e, e
		return true
	})
	return out, err
}

// PinHostKey records the fingerprint the person compared on first connect. It refuses to overwrite one: a changed key must go through
// ReplaceHostKey, which the caller offers only after showing both fingerprints.
func (s *Store) PinHostKey(id, fingerprint string) error {
	if grammar.Fingerprint(fingerprint) != "" {
		return ErrInvalid
	}
	var already bool
	err := s.updateChecked(id, func(r *stored) bool {
		if r.HostKey != "" {
			already = true
			return false
		}
		now := s.now().UTC()
		r.HostKey, r.HostKeyPinnedAt = fingerprint, &now
		return true
	})
	if already {
		return ErrHostKeySet
	}
	return err
}

// ReplaceHostKey swaps a pinned fingerprint for a new one, only when oldFingerprint is the one currently pinned. This is the deliberate
// "the server's key changed" path; nothing calls it without the person confirming.
func (s *Store) ReplaceHostKey(id, oldFingerprint, newFingerprint string) error {
	if grammar.Fingerprint(newFingerprint) != "" || grammar.Fingerprint(oldFingerprint) != "" {
		return ErrInvalid
	}
	var missing, mismatch bool
	err := s.updateChecked(id, func(r *stored) bool {
		if r.HostKey == "" {
			missing = true
			return false
		}
		if r.HostKey != oldFingerprint {
			mismatch = true
			return false
		}
		now := s.now().UTC()
		r.HostKey, r.HostKeyPinnedAt = newFingerprint, &now
		return true
	})
	switch {
	case missing:
		return ErrNoHostKey
	case mismatch:
		return ErrInvalid
	}
	return err
}

func (s *Store) update(id string, f func(*stored)) error {
	return s.updateChecked(id, func(r *stored) bool { f(r); return true })
}

// updateChecked runs f on the record and writes only when f returns true.
func (s *Store) updateChecked(id string, f func(*stored) bool) error {
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
	for i := range v.Records {
		if v.Records[i].ID == id {
			if !f(&v.Records[i]) {
				return ErrInvalid
			}
			return s.write(v)
		}
	}
	return ErrNotFound
}

// Remove deletes a server and its sealed secret. The server itself and anything already pulled are not touched.
func (s *Store) Remove(id string) error {
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
	for i, r := range v.Records {
		if r.ID == id {
			v.Records = append(v.Records[:i], v.Records[i+1:]...)
			return s.write(v)
		}
	}
	return ErrNotFound
}

func (s *Store) read() (vault, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	unlock, err := sealedfile.Lock(s.path)
	if err != nil {
		return vault{}, ErrUnavailable
	}
	defer unlock()
	return s.readLocked()
}

func (s *Store) readLocked() (vault, error) {
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
	dec := json.NewDecoder(bytes.NewReader(plain))
	dec.DisallowUnknownFields()
	if dec.Decode(&v) != nil || v.Version != 1 || v.Records == nil || len(v.Records) > MaxEntries {
		return vault{}, ErrCorrupt
	}
	seen := map[string]bool{}
	for _, r := range v.Records {
		if r.ID == "" || len(r.ID) > 64 || seen[r.ID] || validate(r.Entry) != "" ||
			(needsSecret(r.Entry) && (!r.HasSecret || !validSecret(r.Auth, r.Secret, r.Passphrase))) ||
			(!needsSecret(r.Entry) && (r.HasSecret || r.Secret != "" || r.Passphrase != "")) {
			return vault{}, ErrCorrupt
		}
		seen[r.ID] = true
	}
	return v, nil
}

func (s *Store) write(v vault) error {
	plain, err := json.Marshal(v)
	if err != nil || len(plain) > maxFileSize {
		return ErrCorrupt
	}
	sealed, err := s.protector.Protect(plain)
	if err != nil || len(sealed) == 0 || len(sealed) > maxFileSize {
		return ErrUnavailable
	}
	if err := sealedfile.WriteAtomic(s.path, ".servers-*.tmp", sealed); err != nil {
		return ErrUnavailable
	}
	return nil
}

func newID() (string, error) {
	var raw [12]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "srv-" + hex.EncodeToString(raw[:]), nil
}
