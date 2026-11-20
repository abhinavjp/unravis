package update

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Channels. A stable manifest may only be signed by a stable key; an rc manifest only by an rc key.
const (
	ChannelStable = "stable"
	ChannelRC     = "rc"
)

const (
	Product         = "unravis"
	SchemaVersion   = 1
	MaxManifestSize = 64 * 1024         // a manifest or signature file larger than this is refused unread
	MaxAssetSize    = 500 * 1024 * 1024 // no single release file may claim to be larger
	clockSkew       = 10 * time.Minute
)

// Asset names the keys of Manifest.Assets.
const (
	AssetSetup    = "setup"
	AssetApp      = "app"
	AssetBlackbox = "blackbox"
	AssetLite     = "lite"
)

// Asset is one downloadable file. The signature covers its exact name, size and SHA-256 together with the version,
// so a signed hash for one version cannot be used to install another payload (the Sparkle CVE-2025-0509 lesson).
type Asset struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Manifest is the signed description of one release.
type Manifest struct {
	Schema            int              `json:"schema"`
	Product           string           `json:"product"`
	Version           string           `json:"version"`
	Channel           string           `json:"channel"`
	Published         time.Time        `json:"published"`
	Expires           time.Time        `json:"expires"`
	Seq               uint64           `json:"seq"`
	MinUpdateFrom     string           `json:"minUpdateFrom,omitempty"`
	Assets            map[string]Asset `json:"assets"`
	BlackboxExeSHA256 string           `json:"blackboxExeSha256"`
}

var (
	assetNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	sha256RE    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	knownAssets = map[string]bool{AssetSetup: true, AssetApp: true, AssetBlackbox: true, AssetLite: true}
)

// ChannelOf is the channel a version belongs to: a pre-release is rc, anything else is stable.
func ChannelOf(v Version) string {
	if v.IsPrerelease() {
		return ChannelRC
	}
	return ChannelStable
}

// Validate checks the fields. It does not check any signature; use Verify for that.
func (m *Manifest) Validate(now time.Time) error {
	if m.Schema != SchemaVersion {
		return fmt.Errorf("unsupported manifest schema %d", m.Schema)
	}
	if m.Product != Product {
		return fmt.Errorf("manifest is for %q, not %q", m.Product, Product)
	}
	v, err := ParseVersion(m.Version)
	if err != nil {
		return fmt.Errorf("manifest version: %w", err)
	}
	if m.Version != v.String() {
		return fmt.Errorf("manifest version %q must be written as %q", m.Version, v.String())
	}
	if m.Channel != ChannelStable && m.Channel != ChannelRC {
		return fmt.Errorf("unknown channel %q", m.Channel)
	}
	if m.Channel != ChannelOf(v) {
		return fmt.Errorf("version %s does not belong to channel %s", m.Version, m.Channel)
	}
	if m.Seq == 0 {
		return errors.New("manifest seq must be above zero")
	}
	if m.Published.IsZero() || m.Expires.IsZero() || !m.Expires.After(m.Published) {
		return errors.New("manifest dates are missing or out of order")
	}
	if m.Published.After(now.Add(clockSkew)) {
		return errors.New("manifest is dated in the future")
	}
	if !now.Before(m.Expires) {
		return errors.New("manifest has expired")
	}
	if m.MinUpdateFrom != "" {
		if _, err := ParseVersion(m.MinUpdateFrom); err != nil {
			return fmt.Errorf("minUpdateFrom: %w", err)
		}
	}
	if !sha256RE.MatchString(m.BlackboxExeSHA256) {
		return errors.New("blackboxExeSha256 must be 64 lower-case hex digits")
	}
	for _, need := range []string{AssetSetup, AssetApp, AssetBlackbox} {
		if _, ok := m.Assets[need]; !ok {
			return fmt.Errorf("manifest has no %q asset", need)
		}
	}
	seen := map[string]bool{}
	for key, a := range m.Assets {
		if !knownAssets[key] {
			return fmt.Errorf("unknown asset %q", key)
		}
		if !assetNameRE.MatchString(a.Name) || strings.Contains(a.Name, "..") {
			return fmt.Errorf("asset %q has an unsafe file name", key)
		}
		if seen[a.Name] {
			return fmt.Errorf("asset file name %q is used twice", a.Name)
		}
		seen[a.Name] = true
		if !sha256RE.MatchString(a.SHA256) {
			return fmt.Errorf("asset %q hash must be 64 lower-case hex digits", key)
		}
		if a.Size <= 0 || a.Size > MaxAssetSize {
			return fmt.Errorf("asset %q size %d is not allowed", key, a.Size)
		}
	}
	return nil
}

// Key is one trusted public key. Role decides which channel it may sign.
type Key struct {
	Role string // ChannelStable or ChannelRC
	Pub  ed25519.PublicKey
}

// KeyID is the first 8 bytes of SHA-256(public key), in hex. The signature file names the key by this id.
func KeyID(pub ed25519.PublicKey) string {
	h := sha256.Sum256(pub)
	return hex.EncodeToString(h[:8])
}

// Trust is the set of keys a program accepts and what its own build allows.
type Trust struct {
	Keys []Key
	// Channel is the running build's channel. A stable build accepts only stable manifests;
	// an rc build accepts both (so it can move to a final release).
	Channel string
}

const sigPrefix = "unravis-sig-v1"

// FormatSignature writes the detached signature file text.
func FormatSignature(pub ed25519.PublicKey, sig []byte) string {
	return fmt.Sprintf("%s %s %s\n", sigPrefix, KeyID(pub), base64.StdEncoding.EncodeToString(sig))
}

func parseSignature(text []byte) (keyID string, sig []byte, err error) {
	if len(text) > 1024 {
		return "", nil, errors.New("signature file is too large")
	}
	f := strings.Fields(string(text))
	if len(f) != 3 || f[0] != sigPrefix {
		return "", nil, errors.New("signature file is not in the unravis-sig-v1 format")
	}
	sig, err = base64.StdEncoding.DecodeString(f[2])
	if err != nil || len(sig) != ed25519.SignatureSize {
		return "", nil, errors.New("signature is malformed")
	}
	return f[1], sig, nil
}

// Verify checks the detached signature over the exact manifest bytes, then the manifest fields.
// It returns the parsed manifest only when every check passes. Nothing is read from the manifest before the signature is good.
func Verify(manifest, sigFile []byte, t Trust, now time.Time) (*Manifest, error) {
	if len(manifest) == 0 || len(manifest) > MaxManifestSize {
		return nil, errors.New("manifest size is not allowed")
	}
	keyID, sig, err := parseSignature(sigFile)
	if err != nil {
		return nil, err
	}
	var key *Key
	for i := range t.Keys {
		if len(t.Keys[i].Pub) == ed25519.PublicKeySize && KeyID(t.Keys[i].Pub) == keyID {
			key = &t.Keys[i]
			break
		}
	}
	if key == nil {
		return nil, errors.New("manifest is signed by a key this program does not trust")
	}
	if t.Channel == ChannelStable && key.Role != ChannelStable {
		return nil, errors.New("a stable build accepts only stable keys")
	}
	if !ed25519.Verify(key.Pub, manifest, sig) {
		return nil, errors.New("manifest signature does not match")
	}
	dec := json.NewDecoder(strings.NewReader(string(manifest)))
	dec.DisallowUnknownFields()
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("manifest is not valid: %w", err)
	}
	if dec.More() {
		return nil, errors.New("manifest has extra data after the object")
	}
	if err := m.Validate(now); err != nil {
		return nil, err
	}
	// The key's role must match the channel it signs: an rc key can never publish a stable manifest.
	if m.Channel != key.Role {
		return nil, fmt.Errorf("a %s key cannot sign a %s manifest", key.Role, m.Channel)
	}
	if t.Channel == ChannelStable && m.Channel != ChannelStable {
		return nil, errors.New("a stable build accepts only stable manifests")
	}
	return &m, nil
}

// Decision is what a program does with a verified manifest.
type Decision int

const (
	DecisionIgnore  Decision = iota // same or older, or not for this build
	DecisionInstall                 // newer and allowed
)

// Judge decides whether a verified manifest is an upgrade for the running build.
// lastSeq is the highest seq this machine has already accepted (stored by the caller).
// It never allows a downgrade, an equal version, or a replayed (lower or equal seq) manifest.
func Judge(m *Manifest, current string, lastSeq uint64) (Decision, string, error) {
	cur, err := ParseVersion(current)
	if err != nil {
		return DecisionIgnore, "this is not a release build", nil
	}
	next, err := ParseVersion(m.Version)
	if err != nil {
		return DecisionIgnore, "", err
	}
	if m.Seq <= lastSeq {
		return DecisionIgnore, "manifest is older than one already seen", nil
	}
	if Compare(next, cur) <= 0 {
		return DecisionIgnore, "already up to date", nil
	}
	if m.MinUpdateFrom != "" {
		floor, _ := ParseVersion(m.MinUpdateFrom)
		if Compare(cur, floor) < 0 {
			return DecisionIgnore, "this version is too old to update in one step; install " + m.MinUpdateFrom + " or newer first", nil
		}
	}
	return DecisionInstall, "", nil
}
