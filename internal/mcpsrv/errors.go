package mcpsrv

import (
	"errors"
	"fmt"
)

// Wire codes this package answers with (the names are the ones of internal/mcp/coded.go and spec 03 section 3.4).
const CodePermissionRequired = "permission_required"

// Reasons of a denial. The text is fixed: it never repeats a profile id, a path or the client's input.
const (
	ReasonPending  = "pending"
	ReasonDenied   = "denied"
	ReasonRevoked  = "revoked"
	ReasonExpired  = "expired"
	ReasonStopped  = "stopped"
	ReasonRevision = "changed"
)

// Denied is a profile that may not call now. The host and the model see Message and Action; the reason is for the ledger.
type Denied struct {
	Code    string
	Reason  string
	Message string
	Action  string
}

func (d *Denied) Error() string { return d.Message }

func denied(reason string) *Denied {
	d := &Denied{Code: CodePermissionRequired, Reason: reason}
	switch reason {
	case ReasonPending:
		d.Message, d.Action = "This host is waiting for approval in Unravis.", "Open Unravis > AI access and approve this host."
	case ReasonDenied:
		d.Message, d.Action = "This host was not approved in Unravis.", "Open Unravis > AI access to add it again."
	case ReasonRevoked:
		d.Message, d.Action = "This host was revoked in Unravis.", "Open Unravis > AI access to approve it again."
	case ReasonExpired:
		d.Message, d.Action = "This host's access expired after a period of no use.", "Open Unravis > AI access and renew it."
	case ReasonStopped:
		d.Message, d.Action = "AI access is stopped in Unravis.", "Open Unravis > AI access and resume it."
	default:
		d.Message, d.Action = "This host's access changed while the call ran.", "Retry the call."
	}
	return d
}

// Errors of the enrollment and claim routes. The HTTP status of each is in handler.go.
var (
	ErrUnauthenticated = errors.New("mcpsrv: credential not accepted")
	ErrBadRequest      = errors.New("mcpsrv: bad request")
	ErrTooManyProfiles = errors.New("mcpsrv: too many profiles")
	ErrClaimPending    = errors.New("mcpsrv: still waiting for approval")
	ErrClaimGone       = errors.New("mcpsrv: nothing to claim")
	ErrWrongNonce      = errors.New("mcpsrv: claim nonce does not match")
	ErrNotApprovable   = errors.New("mcpsrv: profile cannot be approved now")
	ErrNoHostYet       = errors.New("mcpsrv: no host has connected to this profile yet")
	ErrBadApproval     = errors.New("mcpsrv: approval not valid")
	ErrStaleApproval   = errors.New("mcpsrv: the profile changed since the approval card was shown")
	ErrContested       = errors.New("mcpsrv: another process tried to enroll this host, reissue it or accept the warning")
	// ErrStopNotSaved: the global stop is in force in this run, but the flag could not be stored, so a restart of the owner lifts it.
	ErrStopNotSaved = errors.New("mcpsrv: the stop is active but could not be saved")
)

func badRequest(what string) error { return fmt.Errorf("%w: %s", ErrBadRequest, what) }
