package update

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"reflect"
	"time"
)

// A control file is a second, small, signed file per channel ("<channel>-control.json" + ".sig") that lets the owner pause
// updates, withdraw a bad version, or roll a release out to part of the machines. It is separate from the manifest on purpose:
// the manifest is decoded strictly, so a new field there would make every installed copy refuse the feed. Older programs never
// ask for this file. A missing file means "no control": updates flow as before.
const (
	ControlSchema     = 1
	maxControlLife    = 90 * 24 * time.Hour // a control file may not claim to be valid longer than this, so a stale pause ends by itself
	maxControlBlocked = 32
	maxControlNote    = 200
	maxControlSeq     = 1 << 40
)

// Control is the signed answer to "may this channel be installed right now, and by whom".
type Control struct {
	Schema    int       `json:"schema"`
	Product   string    `json:"product"`
	Channel   string    `json:"channel"`
	Seq       uint64    `json:"seq"` // ever-rising (the signing time in Unix seconds); an older one is a replay
	Published time.Time `json:"published"`
	Expires   time.Time `json:"expires"`
	Pause     bool      `json:"pause"`   // true: offer nothing on this channel
	Blocked   []string  `json:"blocked"` // versions that must never be offered (withdrawn)
	// Rollout limits one version to a share of machines. It only applies when RolloutVersion is the version being offered, so
	// an old setting cannot hold back the next release.
	RolloutVersion string `json:"rolloutVersion"`
	RolloutPercent int    `json:"rolloutPercent"` // 1..100; 0 is read as 100 when RolloutVersion is empty
	Note           string `json:"note"`           // short plain words shown to the person
}

// Validate checks the fields. It does not look at the signature.
func (c *Control) Validate(now time.Time) error {
	if c.Schema != ControlSchema {
		return errors.New("control file: unsupported schema")
	}
	if c.Product != Product {
		return errors.New("control file: wrong product")
	}
	if c.Channel != ChannelStable && c.Channel != ChannelRC {
		return errors.New("control file: unknown channel")
	}
	if c.Seq == 0 || c.Seq > maxControlSeq {
		return errors.New("control file: seq out of range")
	}
	if c.Published.IsZero() || c.Expires.IsZero() {
		return errors.New("control file: published and expires are required")
	}
	if c.Published.After(now.Add(clockSkew)) {
		return errors.New("control file: published in the future")
	}
	if !c.Expires.After(c.Published) || c.Expires.Sub(c.Published) > maxControlLife {
		return errors.New("control file: expiry is not within 90 days of publishing")
	}
	if !c.Expires.After(now) {
		return errors.New("control file: has expired")
	}
	if len(c.Blocked) > maxControlBlocked {
		return errors.New("control file: too many blocked versions")
	}
	for _, v := range c.Blocked {
		if _, err := ParseVersion(v); err != nil {
			return errors.New("control file: a blocked version is not valid")
		}
	}
	if c.RolloutVersion == "" {
		if c.RolloutPercent != 0 && c.RolloutPercent != 100 {
			return errors.New("control file: a rollout needs a version")
		}
	} else {
		if _, err := ParseVersion(c.RolloutVersion); err != nil {
			return errors.New("control file: the rollout version is not valid")
		}
		if c.RolloutPercent < 1 || c.RolloutPercent > 100 {
			return errors.New("control file: rollout percent must be 1 to 100")
		}
	}
	if len(c.Note) > maxControlNote {
		return errors.New("control file: note is too long")
	}
	for _, r := range c.Note {
		if r < 0x20 || r == 0x7f {
			return errors.New("control file: note has control characters")
		}
	}
	return nil
}

// ParseControl decodes strictly, the same way as ParseManifest.
func ParseControl(b []byte) (*Control, error) {
	if err := rejectDuplicateKeys(b); err != nil {
		return nil, fmt.Errorf("control file is not valid: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var c Control
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("control file is not valid: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("control file has extra data after the object")
	}
	canon, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	var a, d any
	if err := json.Unmarshal(b, &a); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(canon, &d); err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(a, d) {
		return nil, errors.New("control file has keys that are misspelled or in the wrong letter case")
	}
	return &c, nil
}

// VerifyControl checks the signature over the exact bytes, then the fields. The key's role must match the control file's
// channel, and the channel must be the one asked for.
func VerifyControl(body, sigFile []byte, t Trust, now time.Time) (*Control, error) {
	if t.Channel != ChannelStable && t.Channel != ChannelRC {
		return nil, errors.New("the build's channel must be stable or rc")
	}
	if t.Want != ChannelStable && t.Want != ChannelRC {
		return nil, errors.New("the wanted channel must be stable or rc")
	}
	if len(body) == 0 || len(body) > MaxManifestSize {
		return nil, errors.New("control file size is not allowed")
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
		return nil, errors.New("control file is signed by a key this program does not trust")
	}
	if t.Channel == ChannelStable && key.Role != ChannelStable {
		return nil, errors.New("a stable build accepts only stable keys")
	}
	if !ed25519.Verify(key.Pub, body, sig) {
		return nil, errors.New("control file signature does not match")
	}
	c, err := ParseControl(body)
	if err != nil {
		return nil, err
	}
	if err := c.Validate(now); err != nil {
		return nil, err
	}
	if c.Channel != key.Role {
		return nil, fmt.Errorf("a %s key cannot sign a %s control file", key.Role, c.Channel)
	}
	if c.Channel != t.Want {
		return nil, fmt.Errorf("this is the %s channel, but %s was asked for", c.Channel, t.Want)
	}
	return c, nil
}

// Hold says whether this control file keeps the given version from this machine, and why in plain words. draw is this
// machine's number from 0 to 99 for the version (see Drawer); it is only read when a partial rollout applies.
func (c *Control) Hold(version string, draw func() int) (bool, string) {
	if c == nil {
		return false, ""
	}
	why := func(s string) string {
		if c.Note != "" {
			return s + " (" + c.Note + ")"
		}
		return s
	}
	if c.Pause {
		return true, why("updates are paused for now")
	}
	for _, b := range c.Blocked {
		if b == version {
			return true, why("version " + version + " was withdrawn")
		}
	}
	if c.RolloutVersion == version && c.RolloutPercent > 0 && c.RolloutPercent < 100 && draw() >= c.RolloutPercent {
		return true, why(fmt.Sprintf("version %s is going out in stages (%d%% so far); this PC will get it soon", version, c.RolloutPercent))
	}
	return false, ""
}

// newDraw returns a random whole number from 0 to 99. It is never sent anywhere: it only decides which side of the
// rollout line this machine is on, and it is kept so the answer does not change from one check to the next.
func newDraw() int {
	n, err := rand.Int(rand.Reader, big.NewInt(100))
	if err != nil {
		return 0 // cannot draw: treat as first in line rather than blocking forever
	}
	return int(n.Int64())
}

// ctlLive reports whether a remembered control file still counts.
func ctlLive(c *Control, now time.Time) bool {
	return c != nil && c.Expires.After(now)
}
