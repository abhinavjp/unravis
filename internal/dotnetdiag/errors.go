package dotnetdiag

import "errors"

// ErrBadTarget is returned by Probe, before any Dial, for a process id that is
// 0 or this process. The command checks the same things first and exits 2.
var ErrBadTarget = errors.New("dotnetdiag: bad target process id")

// sentences are the only text a person sees for a Reason. They are fixed, hold
// no OS or runtime text, no path and no long number.
var sentences = map[Reason]string{
	"":                 "The diagnostic port answered, a counters session ran for the time asked and was stopped.",
	ReasonOther:        "The probe failed for a reason not in this list. See the code.",
	ReasonDenied:       "Windows refused access. Run as administrator or as SYSTEM.",
	ReasonDisabled:     "This process has no diagnostic port open. It may not be .NET 5 or newer, may still be starting, or diagnostics are switched off for it.",
	ReasonBusy:         "The diagnostic port is busy or the runtime is not ready. Try again.",
	ReasonPeerMismatch: "The other end of the port is not the process asked for. Nothing was sent.",
	ReasonUnsupported:  "The runtime does not support this request, or refused it. See the code.",
	ReasonRejected:     "The runtime does not support this request, or refused it. See the code.",
	ReasonTimeout:      "The runtime did not answer in time.",
	ReasonBadReply:     "The runtime answered with data this probe does not accept. Nothing was kept.",
	ReasonBadStream:    "The runtime answered with data this probe does not accept. Nothing was kept.",
	ReasonNotRunning:   "That process is not running.",
	ReasonStopFailed:   "The session was started but could not be stopped. The runtime ends it by itself soon after the connection closes (see the live check).",
	ReasonCancelled:    "Stopped by the person running the probe.",
}

// Sentence returns the fixed sentence for a Reason ("" is success). An unknown
// Reason gets the sentence for ReasonOther.
func Sentence(r Reason) string {
	if s, ok := sentences[r]; ok {
		return s
	}
	return sentences[ReasonOther]
}

// ExitCode maps a Reason to the process exit code of the diag-probe command.
// Success ("") is 0. Exit code 2 (usage) belongs to the command, not a Reason.
func ExitCode(r Reason) int {
	switch r {
	case "":
		return 0
	case ReasonDenied:
		return 10
	case ReasonDisabled:
		return 11
	case ReasonBusy:
		return 12
	case ReasonPeerMismatch:
		return 13
	case ReasonUnsupported, ReasonRejected:
		return 14
	case ReasonTimeout:
		return 15
	case ReasonBadReply, ReasonBadStream:
		return 16
	case ReasonNotRunning:
		return 17
	case ReasonStopFailed:
		return 18
	case ReasonCancelled:
		return 19
	}
	return 1
}
