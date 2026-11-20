package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"api-audit-explorer/internal/setup"
	"api-audit-explorer/internal/update"
)

var expectSHARE = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (o options) hasExpect() bool { return o.expectSHA != "" || o.expectVersion != "" }

// checkExpectFlags checks the shape of --expect-sha256 and --expect-version. They go together, and they are for an upgrade the
// update check started (the elevated worker gets them too).
func (o options) checkExpectFlags() error {
	if !o.hasExpect() {
		return nil
	}
	if o.expectSHA == "" || o.expectVersion == "" {
		return errors.New("--expect-sha256 and --expect-version go together")
	}
	if !expectSHARE.MatchString(o.expectSHA) {
		return errors.New("--expect-sha256 must be 64 lower-case hex digits")
	}
	if v, err := update.ParseVersion(o.expectVersion); err != nil || v.String() != o.expectVersion {
		return errors.New("--expect-version is not a version (write it like 1.2.3 or 1.2.3-rc.4)")
	}
	if !o.upgrade && !o.worker {
		return errors.New("--expect-sha256 and --expect-version are for --upgrade")
	}
	return nil
}

// expectArgs is what the elevated worker is started with, so it makes the same check.
func (o options) expectArgs() []string {
	if !o.hasExpect() {
		return nil
	}
	return []string{"--expect-sha256", o.expectSHA, "--expect-version", o.expectVersion}
}

// verifyExpect refuses to go on unless this running setup program is exactly the file the update check verified and installs
// exactly the version it announced. It opens its own file with no write, rename or delete sharing and hashes that handle; the caller
// keeps the returned release function until the program ends, so the file at this path cannot be swapped while the window runs and
// starts the elevated worker from the same path. payloadVersion is the version of the program inside this setup.
func verifyExpect(o options, e env, payloadVersion string) (release func(), err error) {
	noop := func() {}
	if !o.hasExpect() {
		return noop, nil
	}
	exe, err := e.setupExe()
	if err != nil {
		return noop, errors.New("setup cannot find its own file to check it")
	}
	f, err := openHeld(exe)
	if err != nil {
		return noop, errors.New("setup cannot read its own file to check it")
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		f.Close()
		return noop, errors.New("setup cannot read its own file to check it")
	}
	if hex.EncodeToString(h.Sum(nil)) != o.expectSHA {
		f.Close()
		return noop, errors.New("this setup file is not the one the update check verified (its SHA-256 differs); nothing was changed")
	}
	if strings.TrimPrefix(strings.TrimSpace(payloadVersion), "v") != o.expectVersion {
		f.Close()
		return noop, fmt.Errorf("this setup installs %q, not the version the update check announced; nothing was changed", payloadVersion)
	}
	return func() { f.Close() }, nil
}

// payloadVersionOf is the version of the program inside this setup ("" when the payload could not be read).
func payloadVersionOf(r *setup.Runner) string {
	if r == nil || r.Payload == nil {
		return ""
	}
	return r.Payload.Manifest.Version
}
