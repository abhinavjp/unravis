package iis

// verify_notes.go holds the VF-01 verification notes (wave plan section 5).
// Each line below records one claim the IIS and Query Store importers rest
// on, and the verdict a real sample would give it:
//
//   - CONFIRMED  the claim held on a measured sample; code built on it may
//     drop its gap text once the owning packet decides to.
//   - REFUTED    the claim did not hold on a measured sample; the importer
//     must change to match reality, not the other way round.
//   - UNMEASURED no sample was available to check the claim; the code keeps
//     its current, conservative behaviour (the gap text stays, the
//     narrower reading is assumed, the division stays as coded) until a
//     real sample arrives.
//
// A note's Reason never carries a path, host name, IP, cookie or log line;
// see verify_test.go for the rule that enforces this.

// VerifyStatus is the verdict recorded for one VF-01 claim.
type VerifyStatus string

const (
	VerifyConfirmed  VerifyStatus = "CONFIRMED"
	VerifyRefuted    VerifyStatus = "REFUTED"
	VerifyUnmeasured VerifyStatus = "UNMEASURED"
)

// VerifyNote is one VF-01 claim and its current verdict.
type VerifyNote struct {
	ID     string
	Claim  string
	Status VerifyStatus
	Reason string
}

// noSampleReason is used by every claim a log or export sample would answer.
const noSampleReason = "no real sample or paired capture on this machine (looked in UNRAVIS_VF01_SAMPLES and .dev/samples/vf01)"

// liveServiceReason is used by claims only a live Mongo or SQL Server
// instance can answer; a file sample cannot measure them.
const liveServiceReason = "needs a live Mongo or SQL Server and a deliberate read-only check; not a file sample"

var Vf01a = VerifyNote{ID: "VF-01a", Claim: "IIS time-taken includes network time to the last send completion",
	Status: VerifyUnmeasured, Reason: noSampleReason}
var Vf01b = VerifyNote{ID: "VF-01b", Claim: "W3C time is the request end",
	Status: VerifyUnmeasured, Reason: noSampleReason}
var Vf01c = VerifyNote{ID: "VF-01c", Claim: "HTTPERR files carry no #Fields header",
	Status: VerifyUnmeasured, Reason: noSampleReason}
var Vf01d = VerifyNote{ID: "VF-01d", Claim: "Trailing HTTPERR field is s-queuename and varies by IIS version",
	Status: VerifyUnmeasured, Reason: noSampleReason}
var Vf01e = VerifyNote{ID: "VF-01e", Claim: "First W3C row of a file has an inflated time-taken",
	Status: VerifyUnmeasured, Reason: noSampleReason}
var Vf01f = VerifyNote{ID: "VF-01f", Claim: "Mongo system.profile needs a write to enable",
	Status: VerifyUnmeasured, Reason: liveServiceReason}
var Vf01g = VerifyNote{ID: "VF-01g", Claim: "dm_os_wait_stats is cumulative",
	Status: VerifyUnmeasured, Reason: liveServiceReason}
var Vf01h = VerifyNote{ID: "VF-01h", Claim: "Query Store durations are microseconds",
	Status: VerifyUnmeasured, Reason: noSampleReason}

// VerifyNotes returns the eight VF-01 notes, a to h, in that order.
func VerifyNotes() []VerifyNote {
	return []VerifyNote{Vf01a, Vf01b, Vf01c, Vf01d, Vf01e, Vf01f, Vf01g, Vf01h}
}

// verifiedTimeTakenScope is true only once Vf01a is CONFIRMED; WP-20 and the tooltip text read it later.
const verifiedTimeTakenScope = false
