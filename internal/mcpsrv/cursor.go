package mcpsrv

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"
	"time"
)

// BL-349: signed cursors (spec 03 section 3.5). The cursor a host gets is not the dashboard's own cursor text: it is that text wrapped
// with who it was made for, what it pages through and when it ends, and signed with the server key (mcpaudit.DB.MAC, the key of the
// grant digests, sealed for the current user). A host that changes it, replays it on another query, hands it to another host, or uses it
// after its profile changed, after the identifiers grant changed, after a restart or after ten minutes gets "result_expired". The
// dashboard sees a cursor only after it opened.
//
//	base64url(JSON{v, ep, t, pr, rev, b, q, e, pos}) "." base64url(HMAC(serverKey, "cursor" || payload)[:16])
//
// v format, ep endpoint ("ag"), t tool, pr profile id, rev profile revision, b binding (digest of the source scope, the tier, the
// identifiers switch and the bridge's own generation), q fingerprint of the query, e expiry (unix seconds), pos the dashboard's cursor.

// CursorTTL is how long a cursor can be used after it was handed out. It is the life of the comparison result the cursors belong to.
const CursorTTL = 10 * time.Minute

const (
	cursorDomain = "cursor"
	cursorSigLen = 16
	maxCursorLen = 4096 // the bridge refuses longer input before this is reached
)

type cursorPayload struct {
	V   int    `json:"v"`
	EP  string `json:"ep"`
	T   string `json:"t"`
	PR  string `json:"pr"`
	Rev int    `json:"rev"`
	B   string `json:"b"`
	Q   string `json:"q"`
	E   int64  `json:"e"`
	Pos string `json:"pos"`
}

// cursorCodec is the codec of one bridge. The bridge is one profile at one revision, so the codec carries them.
type cursorCodec struct {
	sign    func(domain string, data []byte) []byte
	now     func() time.Time
	profile string
	rev     int
	binding string
}

// newBinding digests what besides the profile and revision a cursor must match: the source scope, the tier, the identifiers switch, and
// gen, a random value of the bridge itself (a new bridge, for any reason, ends every cursor of the old one).
func newBinding(sources []string, tier string, identifiers bool, gen string) string {
	s := slices.Clone(sources)
	slices.Sort(s)
	h := sha256.New()
	for _, part := range []string{strings.Join(s, "\x00"), tier, boolText(identifiers), gen} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil)[:12])
}

func boolText(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func newGen() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		// No random source: a generation that cannot be guessed is not available, so cursors cannot be made. Seal fails on the empty text.
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}

func (c *cursorCodec) mac(payload string) []byte {
	return c.sign(cursorDomain, []byte(payload))[:cursorSigLen]
}

// Seal implements mcp.CursorCodec.
func (c *cursorCodec) Seal(tool, fingerprint, pos string) (string, error) {
	if pos == "" || c.binding == "" {
		return "", errCursor
	}
	raw, err := json.Marshal(cursorPayload{V: 1, EP: "ag", T: tool, PR: c.profile, Rev: c.rev, B: c.binding, Q: fingerprint, E: c.now().Add(CursorTTL).Unix(), Pos: pos})
	if err != nil {
		return "", errCursor
	}
	payload := base64.RawURLEncoding.EncodeToString(raw)
	return payload + "." + base64.RawURLEncoding.EncodeToString(c.mac(payload)), nil
}

// Open implements mcp.CursorCodec. The signature is checked first, in constant time, before the payload is parsed.
func (c *cursorCodec) Open(tool, fingerprint, cursor string) (string, bool) {
	if len(cursor) > maxCursorLen || c.binding == "" {
		return "", false
	}
	payload, sigText, found := strings.Cut(cursor, ".")
	if !found {
		return "", false
	}
	sig, err := base64.RawURLEncoding.DecodeString(sigText)
	if err != nil || !hmac.Equal(sig, c.mac(payload)) {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return "", false
	}
	var p cursorPayload
	if json.Unmarshal(raw, &p) != nil {
		return "", false
	}
	if p.V != 1 || p.EP != "ag" || p.T != tool || p.PR != c.profile || p.Rev != c.rev || p.B != c.binding || p.Q != fingerprint ||
		!c.now().Before(time.Unix(p.E, 0)) || p.Pos == "" {
		return "", false
	}
	return p.Pos, true
}

type cursorError struct{}

func (cursorError) Error() string { return "mcpsrv: a cursor could not be made" }

var errCursor error = cursorError{}
