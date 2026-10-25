package ledger

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"api-audit-explorer/internal/sealedfile"
)

const fileVersion = 1

// reuseNote is the history note on an entry reused from an earlier incident.
// The recorded state is history only and never counts as support.
const reuseNote = "earlier incident"

var (
	evidenceIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9:_.\-]{0,119}$`)
	objectIDPattern   = regexp.MustCompile(`[0-9a-fA-F]{24}`)
	emailPattern      = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9\-]+(\.[A-Za-z0-9\-]+)*\.[A-Za-z]{2,}`)
	ipv4Pattern       = regexp.MustCompile(`\b(25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9]?[0-9])(\.(25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9]?[0-9])){3}\b`)
	// ipv6Pattern finds tokens of three or more colon-separated groups of up to
	// four hex digits ("::" allowed). looksLikeIPv6 then lets clock times through.
	ipv6Pattern = regexp.MustCompile(`(?i)(?:^|[^0-9a-z:])((?:[0-9a-f]{1,4})?(?::(?:[0-9a-f]{1,4})?){2,})(?:$|[^0-9a-z:])`)
	decimalTime = regexp.MustCompile(`^[0-9]{1,4}(?::[0-9]{1,4})+$`)

	// isoDatePattern finds calendar dates (2026-10-24, also as the start of
	// 2026-10-24T14:45:00Z) so their digits are not read as a phone number.
	isoDatePattern = regexp.MustCompile(`\b[0-9]{4}-[0-9]{2}-[0-9]{2}(?:[Tt]|\b)`)
	// slashDatePattern finds 2026/10/24, 24/10/2026 and 24.10.2026 for the same reason.
	slashDatePattern = regexp.MustCompile(`\b(?:[0-9]{4}/[0-9]{1,2}/[0-9]{1,2}|[0-9]{1,2}[/.][0-9]{1,2}[/.][0-9]{4})\b`)
	// phoneCandidate is a digit run that may be split by spaces, dashes, dots,
	// slashes, underscores and parentheses, with an optional leading plus.
	// looksLikePhone applies the count.
	phoneCandidate = regexp.MustCompile(`\+?[0-9(][0-9 ()\-./_]*[0-9]`)
	// jwtPattern allows spaces around the dots, which a pasted token can gain.
	jwtPattern     = regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]+ *\. *[A-Za-z0-9_\-]+ *\. *[A-Za-z0-9_\-]*`)
	longHexPattern = regexp.MustCompile(`[0-9a-fA-F]{32,}`)
	base64Pattern  = regexp.MustCompile(`[A-Za-z0-9+/_=\-]{40,}`)
	// shortSecretPattern catches 32 to 39 character keys (an AWS secret is 40,
	// an access key pair pasted together 39) that have no slash, so paths pass.
	shortSecretPattern = regexp.MustCompile(`[A-Za-z0-9+_=\-]{32,}`)
	urlPattern         = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.\-]*://|\bwww\.[a-z0-9]`)
	digitGroup         = regexp.MustCompile(`[0-9]+`)
)

// scanForm is the text the identifier checks read. It applies NFKC (fullwidth
// digits, letters and "@" become ASCII), deletes format characters (zero-width
// spaces and joiners, soft hyphens), turns every Unicode space (NBSP included)
// into an ASCII space and every other decimal digit (Arabic-Indic and the like)
// into its ASCII digit. The checks are still best effort.
func scanForm(s string) string {
	s = norm.NFKC.String(s)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Cf, r):
		case r > unicode.MaxASCII && unicode.Is(unicode.Z, r):
			b.WriteByte(' ')
		case r > unicode.MaxASCII && unicode.IsDigit(r):
			// Decimal digits come in runs of ten from zero to nine.
			zero := r
			for unicode.IsDigit(zero - 1) {
				zero--
			}
			b.WriteByte(byte('0' + (r-zero)%10))
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// looksLikePhone reports whether s holds a phone-number-like digit run: seven or
// more digits, contiguous or split by spaces, dashes, dots or parentheses, with
// an optional leading plus. Dates (2026-10-24) and clock times (14:45) pass, as
// do lists of one- and two-digit numbers ("12 14 15 16") and decimals (3.14159265).
func looksLikePhone(s string) bool {
	s = isoDatePattern.ReplaceAllString(s, " ")
	s = slashDatePattern.ReplaceAllString(s, " ")
	for _, c := range phoneCandidate.FindAllString(s, -1) {
		groups := digitGroup.FindAllString(c, -1)
		digits, longest := 0, 0
		for _, g := range groups {
			digits += len(g)
			if len(g) > longest {
				longest = len(g)
			}
		}
		if digits < 7 {
			continue
		}
		if len(groups) == 1 {
			return true
		}
		if longest <= 2 && !strings.HasPrefix(c, "+") && !strings.ContainsAny(c, "()") {
			continue // a list of small numbers, not a phone number
		}
		if !strings.ContainsAny(c, " -()+") && len(groups) < 3 {
			continue // a plain decimal such as 3.14159265
		}
		return true
	}
	return false
}

// looksLikeSecret reports whether s holds a JWT, a long hex string or a long
// base64-looking string. The base64 test wants upper case, lower case and a
// digit together, so long hyphenated words and plain identifiers pass.
func looksLikeSecret(s string) bool {
	if jwtPattern.MatchString(s) || longHexPattern.MatchString(s) {
		return true
	}
	matches := append(base64Pattern.FindAllString(s, -1), shortSecretPattern.FindAllString(s, -1)...)
	for _, m := range matches {
		var upper, lower, digit bool
		for _, r := range m {
			switch {
			case r >= 'A' && r <= 'Z':
				upper = true
			case r >= 'a' && r <= 'z':
				lower = true
			case r >= '0' && r <= '9':
				digit = true
			}
		}
		if upper && lower && digit {
			return true
		}
	}
	return false
}

// looksLikeIPv6 reports whether s holds an IPv6-looking token. A token of
// decimal digits only and no "::" (a clock time such as 09:14:05) does not count.
func looksLikeIPv6(s string) bool {
	for _, m := range ipv6Pattern.FindAllStringSubmatch(s, -1) {
		token := m[1]
		if strings.Count(token, ":") < 2 || strings.Trim(token, ":") == "" {
			continue
		}
		if !strings.Contains(token, "::") && decimalTime.MatchString(token) {
			continue
		}
		return true
	}
	return false
}

// hasURL reports whether s contains a URL: a scheme followed by "://", or a
// bare www. host.
func hasURL(s string) bool {
	return urlPattern.MatchString(s)
}

type fileData struct {
	Version    int          `json:"version"`
	Hypotheses []Hypothesis `json:"hypotheses"`
}

type store struct {
	mu   sync.Mutex
	path string
	// seal is nil when the file at path is plain JSON (Open) and set when it is sealed (OpenSealed).
	seal  sealedfile.Protector
	items []Hypothesis // stored entries are never mutated in place; edits replace them
}

// Open returns a Store backed by the plain JSON file at path ("" = in memory).
// The application uses OpenSealed, which keeps the ledger encrypted at rest. A
// missing file starts an empty ledger. A file that cannot be read as a ledger is
// left untouched and Open returns an error: the ledger never overwrites it.
func Open(path string) (Store, error) {
	s := &store{path: path, items: []Hypothesis{}}
	if path == "" {
		return s, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("the ledger file %s could not be read: %w", path, err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return s, nil
	}
	items, err := decode(data)
	if err != nil {
		return nil, fmt.Errorf("the ledger file %s is not a valid ledger and was left untouched: %v", path, err)
	}
	s.items = items
	return s, nil
}

func decode(data []byte) ([]Hypothesis, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var fd fileData
	if err := dec.Decode(&fd); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, errors.New("unexpected data after the ledger")
	}
	if fd.Version != fileVersion {
		return nil, fmt.Errorf("unsupported version %d", fd.Version)
	}
	if len(fd.Hypotheses) > MaxHypotheses {
		return nil, fmt.Errorf("more than %d hypotheses", MaxHypotheses)
	}
	seen := map[string]bool{}
	out := make([]Hypothesis, 0, len(fd.Hypotheses))
	for _, h := range fd.Hypotheses {
		if h.ID == "" || seen[h.ID] {
			return nil, errors.New("missing or repeated hypothesis id")
		}
		seen[h.ID] = true
		if !validState(h.State) {
			return nil, fmt.Errorf("unknown state %q", h.State)
		}
		for _, e := range h.History {
			if !validState(e.State) {
				return nil, fmt.Errorf("unknown history state %q", e.State)
			}
		}
		h.Guards = nil
		out = append(out, normalize(h))
	}
	return out, nil
}

func (s *store) List() ([]Hypothesis, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Hypothesis, 0, len(s.items))
	for _, h := range s.items {
		out = append(out, present(h))
	}
	return out, nil
}

func (s *store) Create(text string) (Hypothesis, error) {
	text = strings.TrimSpace(text)
	if err := checkHypothesisText(text); err != nil {
		return Hypothesis{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.items) >= MaxHypotheses {
		return Hypothesis{}, fullErr()
	}
	h := s.newEntry(text)
	next := append(s.snapshot(), h)
	if err := s.checkBudget(next); err != nil {
		return Hypothesis{}, err
	}
	if err := s.commit(next); err != nil {
		return Hypothesis{}, err
	}
	return present(h), nil
}

func (s *store) Update(id string, in Input) (Hypothesis, error) {
	in, err := validateInput(in)
	if err != nil {
		return Hypothesis{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.index(id)
	if i < 0 {
		return Hypothesis{}, ErrNotFound
	}
	if in.State == StateStrong {
		if reasons := Guards(in.Facts, in.EvidenceIDs); len(reasons) > 0 {
			return Hypothesis{}, &GuardError{Reasons: reasons}
		}
	}
	h := s.items[i]
	h.Text = in.Text
	h.State = in.State
	h.EvidenceIDs = in.EvidenceIDs
	h.Notes = in.Notes
	h.SettlingObservation = in.SettlingObservation
	h.Facts = in.Facts
	h.UpdatedAt = time.Now().UTC()
	next := s.snapshot()
	next[i] = h
	if err := s.checkBudget(next); err != nil {
		return Hypothesis{}, err
	}
	if err := s.commit(next); err != nil {
		return Hypothesis{}, err
	}
	return present(h), nil
}

func (s *store) Reuse(id string) (Hypothesis, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.index(id)
	if i < 0 {
		return Hypothesis{}, ErrNotFound
	}
	if len(s.items) >= MaxHypotheses {
		return Hypothesis{}, fullErr()
	}
	old := s.items[i]
	h := s.newEntry(old.Text)
	h.SettlingObservation = old.SettlingObservation
	h.History = []HistoryEntry{{State: old.State, At: h.CreatedAt, Note: reuseNote}}
	next := append(s.snapshot(), h)
	if err := s.checkBudget(next); err != nil {
		return Hypothesis{}, err
	}
	if err := s.commit(next); err != nil {
		return Hypothesis{}, err
	}
	return present(h), nil
}

func (s *store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.index(id)
	if i < 0 {
		return ErrNotFound
	}
	next := append(s.snapshot()[:i], s.items[i+1:]...)
	return s.commit(next)
}

// newEntry builds a fresh open entry with no facts. Callers hold s.mu.
func (s *store) newEntry(text string) Hypothesis {
	now := time.Now().UTC()
	return normalize(Hypothesis{
		ID:        s.newID(),
		Text:      text,
		State:     StateOpen,
		CreatedAt: now,
		UpdatedAt: now,
	})
}

func (s *store) newID() string {
	for {
		var b [6]byte
		if _, err := rand.Read(b[:]); err != nil {
			panic("ledger: no randomness: " + err.Error())
		}
		id := hex.EncodeToString(b[:])
		if s.index(id) < 0 {
			return id
		}
	}
}

func (s *store) index(id string) int {
	for i := range s.items {
		if s.items[i].ID == id {
			return i
		}
	}
	return -1
}

// snapshot is a copy of the entry list that a change can edit before commit.
func (s *store) snapshot() []Hypothesis {
	return append(make([]Hypothesis, 0, len(s.items)+1), s.items...)
}

// commit persists next and, only if that succeeds, makes it the ledger.
// Callers hold s.mu.
func (s *store) commit(next []Hypothesis) error {
	if err := s.save(next); err != nil {
		return err
	}
	s.items = next
	return nil
}

func (s *store) save(items []Hypothesis) error {
	if s.path == "" {
		return nil
	}
	if s.seal != nil {
		return s.saveSealed(items)
	}
	data, err := json.MarshalIndent(fileData{Version: fileVersion, Hypotheses: items}, "", "  ")
	if err != nil {
		return saveErr(err)
	}
	if err := sealedfile.WriteAtomic(s.path, ".ledger-*.tmp", append(data, '\n')); err != nil {
		return saveErr(err)
	}
	return nil
}

func saveErr(err error) error { return fmt.Errorf("the ledger could not be saved: %w", err) }

func fullErr() error {
	return invalid(fmt.Sprintf("the ledger already holds %d hypotheses; delete one first", MaxHypotheses))
}

func budgetErr() error {
	return invalid("The ledger is full. Delete or shorten entries before adding more.")
}

// totalBytes is the exact size of the entries as GET /api/ledger serialises
// them (with current guards), including the array brackets and commas. It never
// stops early: checkBudget compares two totals, and a truncated one would let
// an oversized ledger grow and refuse edits that shrink it.
func totalBytes(items []Hypothesis) int {
	total := 2
	for i, h := range items {
		b, err := json.Marshal(present(h))
		if err != nil {
			return math.MaxInt
		}
		total += len(b)
		if i > 0 {
			total++
		}
	}
	return total
}

// checkBudget refuses next when it is over MaxTotalBytes, unless it is no larger
// than the current ledger, so an oversized ledger (from an older file) can still
// be edited down but never grows. Callers hold s.mu.
func (s *store) checkBudget(next []Hypothesis) error {
	n := totalBytes(next)
	if n <= MaxTotalBytes || n <= totalBytes(s.items) {
		return nil
	}
	return budgetErr()
}

// present returns a copy safe to hand out, with the current guard reasons.
func present(h Hypothesis) Hypothesis {
	h = normalize(clone(h))
	h.Guards = Guards(h.Facts, h.EvidenceIDs)
	return h
}

func clone(h Hypothesis) Hypothesis {
	h.EvidenceIDs = append([]string(nil), h.EvidenceIDs...)
	h.History = append([]HistoryEntry(nil), h.History...)
	h.Facts = cloneFacts(h.Facts)
	h.Guards = nil
	return h
}

func cloneFacts(f Facts) Facts {
	f.Counterexamples = append([]string(nil), f.Counterexamples...)
	f.Alternatives = append([]Alternative(nil), f.Alternatives...)
	if f.JoinAmbiguousPct != nil {
		v := *f.JoinAmbiguousPct
		f.JoinAmbiguousPct = &v
	}
	return f
}

// normalize replaces nil lists with empty ones so JSON shows [] rather than null.
func normalize(h Hypothesis) Hypothesis {
	if h.EvidenceIDs == nil {
		h.EvidenceIDs = []string{}
	}
	if h.History == nil {
		h.History = []HistoryEntry{}
	}
	if h.Facts.Counterexamples == nil {
		h.Facts.Counterexamples = []string{}
	}
	if h.Facts.Alternatives == nil {
		h.Facts.Alternatives = []Alternative{}
	}
	return h
}

func validState(s string) bool {
	switch s {
	case StateOpen, StateSupported, StateStrong, StateContradicted, StateNotTestable:
		return true
	}
	return false
}

// validateInput checks an analyst edit and returns a private, normalized copy.
func validateInput(in Input) (Input, error) {
	in.Text = strings.TrimSpace(in.Text)
	if err := checkHypothesisText(in.Text); err != nil {
		return Input{}, err
	}
	if !validState(in.State) {
		return Input{}, invalid("the state must be one of: open, supported by correlation, supported by strong correlation, contradicted, not testable with available evidence")
	}
	if err := checkText("the notes", in.Notes); err != nil {
		return Input{}, err
	}
	in.SettlingObservation = strings.TrimSpace(in.SettlingObservation)
	if err := checkSettling(in.SettlingObservation); err != nil {
		return Input{}, err
	}
	if len(in.EvidenceIDs) > MaxListLen {
		return Input{}, invalid(fmt.Sprintf("at most %d evidence references are allowed", MaxListLen))
	}
	for _, id := range in.EvidenceIDs {
		if !evidenceIDPattern.MatchString(id) {
			return Input{}, invalid("an evidence reference must be a provenance ID: lowercase letters, digits and : _ . - only, up to 120 characters")
		}
		if objectIDPattern.MatchString(id) {
			return Input{}, invalid("an evidence reference cannot contain a record ID (24 hex characters); use the provenance ID instead")
		}
	}
	in.EvidenceIDs = append([]string{}, in.EvidenceIDs...)

	f := cloneFacts(in.Facts)
	if f.TotalN < 0 || f.PresentK < 0 {
		return Input{}, invalid("episode counts cannot be negative")
	}
	if p := f.JoinAmbiguousPct; p != nil && (math.IsNaN(*p) || math.IsInf(*p, 0) || *p < 0 || *p > 100) {
		return Input{}, invalid("the share of rows with an ambiguous join must be between 0 and 100 percent")
	}
	if len(f.Counterexamples) > MaxListLen {
		return Input{}, invalid(fmt.Sprintf("at most %d counterexamples are allowed", MaxListLen))
	}
	for _, c := range f.Counterexamples {
		if err := checkText("a counterexample", c); err != nil {
			return Input{}, err
		}
	}
	if len(f.Alternatives) > MaxListLen {
		return Input{}, invalid(fmt.Sprintf("at most %d alternative explanations are allowed", MaxListLen))
	}
	for _, a := range f.Alternatives {
		if err := checkText("an alternative explanation", a.Text); err != nil {
			return Input{}, err
		}
		if err := checkText("an alternative's status", a.Status); err != nil {
			return Input{}, err
		}
	}
	in.Facts = f
	return in, nil
}

func checkHypothesisText(text string) error {
	if text == "" {
		return invalid("the hypothesis text cannot be empty")
	}
	return checkText("the hypothesis text", text)
}

// checkSettling validates the settling observation: at most MaxSettlingLen
// characters, no control characters (a line break or tab is allowed), and the
// same identifier checks as the notes. s is already trimmed.
func checkSettling(s string) error {
	const what = "the settling observation"
	if utf8.RuneCountInString(s) > MaxSettlingLen {
		return invalid(fmt.Sprintf("%s is longer than %d characters", what, MaxSettlingLen))
	}
	for _, r := range s {
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			return invalid(what + " contains a control character")
		}
	}
	return checkText(what, s)
}

// checkText enforces the length limit and keeps identifiers out of the ledger.
func checkText(what, s string) error {
	if utf8.RuneCountInString(s) > MaxTextLen {
		return invalid(fmt.Sprintf("%s is longer than %d characters", what, MaxTextLen))
	}
	for _, r := range s {
		if (unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t') || isBidiControl(r) {
			return invalid(what + " contains a control character")
		}
	}
	s = scanForm(s)
	switch {
	case objectIDPattern.MatchString(s):
		return invalid(what + " contains what looks like a record ID (24 hex characters); the ledger holds no identifiers, so refer to evidence by its provenance ID instead")
	case emailPattern.MatchString(s):
		return invalid(what + " contains an email address; the ledger holds no identifiers")
	case ipv4Pattern.MatchString(s), looksLikeIPv6(s):
		return invalid(what + " contains an IP address; the ledger holds no identifiers")
	case hasURL(s):
		return invalid(what + " contains a URL; the ledger holds no identifiers, so refer to evidence by its provenance ID instead")
	case looksLikePhone(s):
		return invalid(what + " contains what looks like a phone number or numeric ID; the ledger holds no identifiers")
	case looksLikeSecret(s):
		return invalid(what + " contains what looks like a token or secret; the ledger holds no identifiers")
	}
	return nil
}

func invalid(msg string) error { return fmt.Errorf("%w: %s", ErrInvalid, msg) }

// isBidiControl reports the bidirectional embedding, override and isolate
// characters: invisible, and they let text read in a different order than it is
// stored, so an exported ledger line could mislead.
func isBidiControl(r rune) bool {
	return (r >= 0x202A && r <= 0x202E) || (r >= 0x2066 && r <= 0x2069)
}
