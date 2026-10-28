package setup

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// SilentConfig is setup.json for unravis-setup.exe --silent --config setup.json (installer-and-releases.md section 6).
type SilentConfig struct {
	Scope         Scope            `json:"scope"`
	Location      string           `json:"location"`
	Blackbox      *BlackboxOptions `json:"blackbox"`
	Firewall      *FirewallOptions `json:"firewall"`
	StartAtSignIn *bool            `json:"startAtSignIn"`
	OpenWhenDone  *bool            `json:"openWhenDone"`
	// ReadKeyFile is where the Blackbox read key is written once, readable by the current user only. It is required when a Blackbox
	// is placed, because silent mode has no window to show the key in and never writes it to the log.
	ReadKeyFile string           `json:"readKeyFile"`
	Uninstall   *SilentUninstall `json:"uninstall"`
}

// SilentUninstall is the "uninstall" object. Each deletion happens only with an explicit JSON true.
type SilentUninstall struct {
	DeleteCases      json.RawMessage `json:"deleteCases"`
	DeleteRecordings json.RawMessage `json:"deleteRecordings"`
}

// maxSilentConfig is the largest setup.json read; a real one is well under a kilobyte.
const maxSilentConfig = 64 << 10

// ParseSilentConfig reads setup.json strictly: unknown keys, a wrong type, a second JSON value after the first, or a missing choice
// all fail with a plain sentence that names the key. Nothing is guessed.
func ParseSilentConfig(data []byte) (SilentConfig, error) {
	var c SilentConfig
	if len(data) > maxSilentConfig {
		return c, fmt.Errorf("setup.json is %d bytes; it must be under %d", len(data), maxSilentConfig)
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")) // Notepad's UTF-8 mark
	if len(bytes.TrimSpace(data)) == 0 {
		return c, errors.New("setup.json is empty")
	}
	// encoding/json matches keys without regard to case; setup does not guess, so "blackBox" is an unknown key.
	if err := checkExactKeys(data); err != nil {
		return c, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return c, explainJSON(data, err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return c, errors.New("setup.json has more after its closing brace; it must hold one JSON object")
	}
	return c, nil
}

// explainJSON turns a decoder error into a sentence with the line and column, or the key with the wrong type.
func explainJSON(data []byte, err error) error {
	var syn *json.SyntaxError
	var typ *json.UnmarshalTypeError
	switch {
	case errors.As(err, &syn):
		line, col := lineCol(data, syn.Offset)
		return fmt.Errorf("setup.json is not valid JSON at line %d, column %d: %v", line, col, syn)
	case errors.As(err, &typ):
		key := typ.Field
		if key == "" {
			return fmt.Errorf("setup.json must be one JSON object, not a %s", typ.Value)
		}
		return fmt.Errorf("setup.json: %q must be %s, not a %s", key, jsonKind(typ.Type.Kind().String()), typ.Value)
	case errors.Is(err, io.ErrUnexpectedEOF):
		return errors.New("setup.json ends too early (a brace or quote is missing)")
	}
	msg := err.Error()
	if strings.HasPrefix(msg, "json: unknown field ") {
		return fmt.Errorf("setup.json has the key %s, which setup does not know. Remove it or check its spelling (keys are case sensitive)",
			strings.TrimPrefix(msg, "json: unknown field "))
	}
	return fmt.Errorf("setup.json cannot be read: %v", err)
}

func jsonKind(k string) string {
	switch k {
	case "string":
		return "text in quotes"
	case "bool", "ptr":
		return "true or false"
	case "int", "float64":
		return "a number"
	case "slice":
		return "a list"
	case "struct":
		return "an object or null"
	}
	return "another type"
}

func lineCol(data []byte, off int64) (int, int) {
	if off > int64(len(data)) {
		off = int64(len(data))
	}
	line, col := 1, 1
	for _, b := range data[:off] {
		if b == '\n' {
			line, col = line+1, 1
		} else {
			col++
		}
	}
	return line, col
}

// explicitTrue reads an uninstall deletion: absent or false is false, true is true, anything else is a mistake.
func explicitTrue(key string, raw json.RawMessage) (bool, error) {
	s := string(bytes.TrimSpace(raw))
	switch s {
	case "", "null", "false":
		return false, nil
	case "true":
		return true, nil
	}
	return false, fmt.Errorf("setup.json: uninstall.%s must be true or false (without quotes), not %s", key, s)
}

// Request turns the config into what the run does. op is install unless --uninstall, --repair or --upgrade was given.
func (c SilentConfig) Request(op Op) (WorkRequest, error) {
	req := WorkRequest{Op: op, Silent: true}
	if op == OpUninstall {
		if c.Uninstall == nil {
			return req, nil
		}
		var err error
		if req.Uninstall.DeleteCases, err = explicitTrue("deleteCases", c.Uninstall.DeleteCases); err != nil {
			return req, err
		}
		if req.Uninstall.DeleteRecordings, err = explicitTrue("deleteRecordings", c.Uninstall.DeleteRecordings); err != nil {
			return req, err
		}
		// The explicit true in the file is the typed confirmation: the window would ask for DELETE and for the Blackbox name.
		if req.Uninstall.DeleteCases {
			req.Uninstall.ConfirmCases = ConfirmDeleteWord
		}
		req.Uninstall.CloseRunningApp = false
		return req, nil
	}
	if c.Uninstall != nil {
		return req, errors.New(`setup.json has an "uninstall" object, but this run installs. Run unravis-setup.exe --uninstall --silent --config setup.json to remove Unravis`)
	}
	o := Options{Scope: c.Scope, Location: strings.TrimSpace(c.Location), Blackbox: c.Blackbox, Firewall: c.Firewall,
		ReadKeyFile: strings.TrimSpace(c.ReadKeyFile)}
	if op == OpInstall {
		switch c.Scope {
		case ScopeUser, ScopeMachine:
		case "":
			return req, errors.New(`setup.json needs "scope": "user" (just for the person running setup) or "machine" (everyone on this PC)`)
		default:
			return req, fmt.Errorf(`setup.json: "scope" must be "user" or "machine", not %q`, c.Scope)
		}
		o.StartAtSignIn = true
		if c.StartAtSignIn != nil {
			o.StartAtSignIn = *c.StartAtSignIn
		}
		if c.Firewall != nil && c.Blackbox == nil {
			return req, errors.New(`setup.json has "firewall" but no "blackbox": the firewall rule only opens the Blackbox port`)
		}
		if c.Blackbox != nil && o.ReadKeyFile == "" {
			return req, errors.New(`setup.json places a Blackbox, so it needs "readKeyFile": the file the read key is written to (silent mode shows it nowhere else)`)
		}
	}
	// Silent mode never opens a window, so openWhenDone is ignored whatever the file says.
	o.OpenWhenDone = false
	req.Options = o
	return req, nil
}

// FillRecordingsConfirmation completes a silent uninstall's confirmation with the installed Blackbox's name (known only after the
// install is found).
func FillRecordingsConfirmation(req *WorkRequest, old *Installed) {
	if req.Uninstall.DeleteRecordings && old != nil && old.State.Blackbox != nil {
		req.Uninstall.ConfirmRecordings = old.State.Blackbox.Name
	}
}

var silentKeys = map[string]map[string]bool{
	"":          {"scope": true, "location": true, "blackbox": true, "firewall": true, "startAtSignIn": true, "openWhenDone": true, "readKeyFile": true, "uninstall": true},
	"blackbox":  {"name": true, "folder": true, "keepDays": true, "maxGB": true, "minFreePercent": true, "port": true},
	"firewall":  {"remoteAddresses": true},
	"uninstall": {"deleteCases": true, "deleteRecordings": true},
}

// checkExactKeys refuses a key that is not spelt exactly as setup knows it. Shapes it cannot read are left to the decoder, which
// explains them.
func checkExactKeys(data []byte) error {
	var top map[string]json.RawMessage
	if json.Unmarshal(data, &top) != nil {
		return nil
	}
	for k, v := range top {
		if !silentKeys[""][k] {
			return fmt.Errorf("setup.json has the key %q, which setup does not know. Remove it or check its spelling (keys are case sensitive)", k)
		}
		inner, ok := silentKeys[k]
		if !ok {
			continue
		}
		var obj map[string]json.RawMessage
		if json.Unmarshal(v, &obj) != nil {
			continue
		}
		for ik := range obj {
			if !inner[ik] {
				return fmt.Errorf("setup.json has the key %q inside %q, which setup does not know. Remove it or check its spelling (keys are case sensitive)", ik, k)
			}
		}
	}
	return nil
}
