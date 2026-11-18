package blackbox

import (
	"errors"
	"fmt"
	"strings"
)

// PinnedHelperSHA256 is the SHA-256 (hex) of the unravis-blackbox.exe that was
// built together with this program. A release build sets it with
// -ldflags "-X api-audit-explorer/internal/blackbox.PinnedHelperSHA256=<hex>"
// (SEC-01). When empty (a developer build) the helper is accepted if it is
// signed and valid, or not signed at all; a signature that is present but wrong is
// always refused.
var PinnedHelperSHA256 string

// Authenticode results the check treats as "this file carries no signature".
const (
	trustNoSignature      = 0x800B0100 // TRUST_E_NOSIGNATURE
	trustProviderUnknown  = 0x800B0001 // TRUST_E_PROVIDER_UNKNOWN
	trustSubjectFormatUnk = 0x800B0003 // TRUST_E_SUBJECT_FORM_UNKNOWN
)

// errHelperRefused is why the helper may not be run.
var errHelperRefused = errors.New("the Blackbox program failed its integrity check")

// helperDecision is the rule behind GuardHelper. sum is the SHA-256 of the file
// that is locked, trust is the Authenticode result (0 is a valid signature).
func helperDecision(pinned, sum string, trust uint32) error {
	pinned = strings.TrimSpace(pinned)
	if pinned != "" {
		if !strings.EqualFold(pinned, sum) {
			return fmt.Errorf("%w: it is not the program built with this Unravis", errHelperRefused)
		}
		return nil
	}
	switch trust {
	case 0, trustNoSignature, trustProviderUnknown, trustSubjectFormatUnk:
		return nil
	}
	return fmt.Errorf("%w: its signature is not valid (0x%08X)", errHelperRefused, trust)
}
