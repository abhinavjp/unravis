// Package hosts holds the owner-supplied host map (hosts.json): the labels,
// aliases, roles, time zones and core counts that importers use to turn the raw
// host names inside an export into a label. Raw names never reach the case
// store; only the label does. The map ships empty: real host content is
// supplied by the owner (card T0.5), never by this repository.
package hosts

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"api-audit-explorer/internal/clock"
	"api-audit-explorer/internal/importer"
)

const (
	// Version is the only hosts.json version this build reads and writes.
	Version = 1
	// MaxFileBytes caps the hosts.json plaintext (the sealed file gets a little headroom).
	MaxFileBytes = 64 << 10
	// MaxHosts caps the number of host entries.
	MaxHosts = 200
	// MinCores and MaxCores bound logicalCores when it is given.
	MinCores = 1
	MaxCores = 1024

	maxAliases   = 32
	maxNameBytes = 253
)

// ErrUnknownHost is returned for a host name that has no entry. It is the same
// value importers use, with the text "unknown host".
var ErrUnknownHost = importer.ErrUnknownHost

// ErrInvalid wraps every reason a hosts.json document is refused. The message
// names the field and the rule, never the offending value.
var ErrInvalid = errors.New("invalid hosts file")

var (
	nameRE    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	runtimeRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,31}$`)
	roles     = map[string]bool{"iis": true, "sql": true, "mongo": true, "app": true}
)

// Entry is one host as written in hosts.json.
type Entry struct {
	Label        string   `json:"label"`
	Aliases      []string `json:"aliases,omitempty"`
	Role         string   `json:"role,omitempty"`
	TZ           string   `json:"tz,omitempty"`
	LogicalCores *int     `json:"logicalCores,omitempty"`
	Runtime      string   `json:"runtime,omitempty"`
}

type document struct {
	Version int     `json:"version"`
	Hosts   []Entry `json:"hosts"`
}

// Map is an immutable, validated host map. The zero value and a nil *Map are
// both empty. Safe for concurrent use.
type Map struct {
	entries []Entry
	hints   map[string]importer.HostHint // key: lower-cased label or alias
	labels  []string                     // sorted
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// Load reads and validates a hosts.json document. It refuses files over
// MaxFileBytes, unknown fields, trailing data, a version other than 1, more
// than MaxHosts hosts, duplicate labels or aliases, a bad tz and cores outside
// 1 to 1024.
func Load(r io.Reader) (*Map, error) {
	if r == nil {
		return nil, invalid("no input")
	}
	data, err := io.ReadAll(io.LimitReader(r, MaxFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("hosts file could not be read: %w", err)
	}
	if len(data) > MaxFileBytes {
		return nil, invalid("file is larger than %d bytes", MaxFileBytes)
	}
	return decode(data)
}

func decode(data []byte) (*Map, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var doc document
	if err := dec.Decode(&doc); err != nil {
		return nil, invalid("not a valid hosts document (%s)", jsonProblem(err))
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, invalid("unexpected data after the hosts document")
	}
	if doc.Version != Version {
		return nil, invalid("version must be %d", Version)
	}
	return build(doc.Hosts)
}

// jsonProblem describes a decode failure without echoing input. The standard
// library quotes unknown field names; those are replaced by a fixed phrase.
func jsonProblem(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "unknown field"):
		return "unknown field"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "truncated or empty"
	}
	var syn *json.SyntaxError
	if errors.As(err, &syn) {
		return "syntax error"
	}
	var typ *json.UnmarshalTypeError
	if errors.As(err, &typ) {
		return "wrong type for field " + typ.Field
	}
	return "decode error"
}

func build(in []Entry) (*Map, error) {
	if len(in) > MaxHosts {
		return nil, invalid("more than %d hosts", MaxHosts)
	}
	m := &Map{
		entries: make([]Entry, 0, len(in)),
		hints:   make(map[string]importer.HostHint, len(in)*2),
		labels:  make([]string, 0, len(in)),
	}
	for i, e := range in {
		hint, err := validate(e)
		if err != nil {
			return nil, invalid("host %d: %v", i+1, err)
		}
		keys := []string{strings.ToLower(e.Label)}
		own := map[string]bool{keys[0]: true}
		for _, a := range e.Aliases {
			k := strings.ToLower(a)
			if k == keys[0] {
				continue // an alias equal to its own label is redundant, not a duplicate
			}
			if own[k] {
				return nil, invalid("host %d: duplicate alias", i+1)
			}
			own[k] = true
			keys = append(keys, k)
		}
		for _, k := range keys {
			if _, taken := m.hints[k]; taken {
				return nil, invalid("host %d: label or alias already belongs to another host", i+1)
			}
			m.hints[k] = hint
		}
		clone := e
		clone.Aliases = append([]string(nil), e.Aliases...)
		if e.LogicalCores != nil {
			c := *e.LogicalCores
			clone.LogicalCores = &c
		}
		m.entries = append(m.entries, clone)
		m.labels = append(m.labels, e.Label)
	}
	sort.Strings(m.labels)
	return m, nil
}

func validateName(kind, s string) error {
	if s == "" {
		return fmt.Errorf("%s is empty", kind)
	}
	if len(s) > maxNameBytes {
		return fmt.Errorf("%s is longer than %d bytes", kind, maxNameBytes)
	}
	if !nameRE.MatchString(s) {
		return fmt.Errorf("%s has characters outside letters, digits, dot, dash and underscore", kind)
	}
	return nil
}

func validate(e Entry) (importer.HostHint, error) {
	var hint importer.HostHint
	if err := validateName("label", e.Label); err != nil {
		return hint, err
	}
	if len(e.Aliases) > maxAliases {
		return hint, fmt.Errorf("more than %d aliases", maxAliases)
	}
	for _, a := range e.Aliases {
		if err := validateName("alias", a); err != nil {
			return hint, err
		}
	}
	if e.Role != "" && !roles[e.Role] {
		return hint, errors.New(`role must be "iis", "sql", "mongo" or "app"`)
	}
	loc, err := resolveTZ(e.TZ)
	if err != nil {
		return hint, err
	}
	cores := 0
	if e.LogicalCores != nil {
		cores = *e.LogicalCores
		if cores < MinCores || cores > MaxCores {
			return hint, fmt.Errorf("logicalCores must be %d to %d", MinCores, MaxCores)
		}
	}
	if e.Runtime != "" && !runtimeRE.MatchString(e.Runtime) {
		return hint, errors.New("runtime is not a short lower-case token such as iis-10")
	}
	return importer.HostHint{Label: e.Label, Role: e.Role, TZ: loc, LogicalCores: cores, Runtime: e.Runtime}, nil
}

// resolveTZ accepts an IANA name or a Windows ID known to clock.WindowsZone.
// Empty means no time zone: importers then refuse host-local fields.
func resolveTZ(id string) (*time.Location, error) {
	if id == "" {
		return nil, nil
	}
	if len(id) > 64 {
		return nil, errors.New("tz is longer than 64 bytes")
	}
	if loc, ok := clock.WindowsZone(id); ok {
		return loc, nil
	}
	// "Local" would bind the file to the machine it is read on.
	if strings.EqualFold(id, "local") || strings.ContainsAny(id, " \\") {
		return nil, errors.New("tz is not a known IANA name or Windows zone ID")
	}
	loc, err := time.LoadLocation(id)
	if err != nil {
		return nil, errors.New("tz is not a known IANA name or Windows zone ID")
	}
	return loc, nil
}

// Lookup finds a host by label or alias, ignoring case, surrounding spaces and
// one trailing dot. It reports false for a name with no entry; use Resolve for
// the error form.
func (m *Map) Lookup(observed string) (importer.HostHint, bool) {
	if m == nil {
		return importer.HostHint{}, false
	}
	key := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(observed)), ".")
	hint, ok := m.hints[key]
	return hint, ok
}

// Resolve is Lookup with ErrUnknownHost for a miss. The error never contains
// the observed name.
func (m *Map) Resolve(observed string) (importer.HostHint, error) {
	hint, ok := m.Lookup(observed)
	if !ok {
		return importer.HostHint{}, ErrUnknownHost
	}
	return hint, nil
}

// Labels returns the host labels, sorted.
func (m *Map) Labels() []string {
	if m == nil {
		return []string{}
	}
	return append([]string{}, m.labels...)
}

// Len is the number of hosts.
func (m *Map) Len() int {
	if m == nil {
		return 0
	}
	return len(m.entries)
}

// Marshal returns the map as a hosts.json document that Load accepts.
func (m *Map) Marshal() ([]byte, error) {
	doc := document{Version: Version, Hosts: []Entry{}}
	if m != nil {
		doc.Hosts = m.entries
	}
	return json.Marshal(doc)
}
