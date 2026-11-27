// Package keeper is the Unravis key keeper (P14, owner ruling B19).
//
// The key keeper is a small Windows service that alone holds the saved secrets of a shared install: saved SQL Server passwords, saved
// MongoDB link passwords and Blackbox read keys. They are sealed in the keeper's own folder for the keeper's service account. Unravis
// processes (one per person signed in to the PC) talk to the keeper over a local named pipe and ask it to DO things with a saved secret
// ("test saved SQL connection 7f3a", "run the fixed health checks on it", "GET /manifest from Blackbox 12be"); the keeper answers with
// the result. There is no operation that returns a password or a read key, in any form. A secret travels the other way only: from the
// person who types it, through their Unravis, into the keeper, once.
//
// The one narrow exception is the Blackbox SQL set-up hand-over (sql.handover.*, RV-SEC-2): so that a saved SQL login never has to be
// written to a file for the elevated set-up helper, the keeper holds it in memory under a random one-time id for about five minutes
// and gives it, once, only to an ELEVATED caller (an administrator's token, as Windows reports it for the pipe client) in the same
// Windows session as the person who asked for the set-up. The person's ordinary, non-elevated Unravis can ask for a hand-over but can
// never read one back; the old temp file likewise could be read only by administrators. The id travels from the person's Unravis to
// the helper on its command line; the login itself travels only over the keeper pipe, straight into the helper.
//
// Wire format (protocol version 1):
//
//   - One request per connection. Every frame is a 4-byte big-endian length followed by that many bytes, at most MaxFrame.
//   - The client sends one frame holding a JSON Request {v, op, args}.
//   - The keeper answers one frame holding a JSON Response {ok, result | error, stream}.
//   - When stream is true, chunk frames follow. The first byte of a chunk says what it is: 'D' data (the rest is body bytes), 'E' a
//     failure part-way (the rest is a JSON WireError) or 'Z' the clean end. A connection that closes before 'Z' is a broken read.
//   - The caller is identified by Windows itself (the pipe client token), per connection, so there is nothing to replay: a request
//     carries no session token, and a copied frame sent by another account is that other account's request.
//
// Operations are grouped in namespaces: ping/status, sql.*, mongo.*, bb.*, audit.*. The audit.* operations (P14b) host the MongoDB
// audit repository (internal/store) for sources opened from a saved link whose password the keeper holds: named, read-only reads that
// take the audit package's typed inputs, with every answer sent as stream frames so its size is not bounded by MaxFrame. See
// Server.Handle for the extension point.
package keeper

import (
	"io"

	"api-audit-explorer/internal/keeperwire"
)

// Error codes.
const (
	CodeNotFound      = "not-found"
	CodeFull          = "full"
	CodeInvalid       = "invalid"
	CodePasswordAgain = "password-again" // a blank-password re-save asked for weaker encryption: the password must be typed again
	CodeDuplicate     = "duplicate"
	CodeCorrupt       = "corrupt"
	CodeNotDamaged    = "not-damaged"    // mongo.setaside: the saved file reads fine, so there is nothing to set aside
	CodeSetAsideFull  = "set-aside-full" // mongo.setaside: three damaged copies are already kept
	CodeUnavailable   = "unavailable"
	CodeConnect       = "connect"       // SQL Server: Detail is {code, message} from dbhealth.ClassifyConnect
	CodeMongoConnect  = "mongo-connect" // MongoDB: Detail is {stage, code, message} (audit.ConnectionError)
	CodeMismatch      = "mismatch"      // a saved MongoDB link no longer matches the settings sent with it
	CodeRateLimited   = "rate-limited"
	CodeRefused       = "refused"
	CodeBusy          = "busy"
	CodeBBRead        = "bb-read" // Blackbox read: Detail is {kind, message}
	CodeAudit         = "audit"   // audit repository refusal: Detail is {kinds: [...]}, names from auditKinds
	CodeUnknownOp     = "unknown-op"
	CodeVersion       = "version"
	CodeInternal      = "internal"
)

// Operations.
const (
	OpPing   = "ping"
	OpStatus = "status"

	OpSQLList     = "sql.list"
	OpSQLSave     = "sql.save"
	OpSQLDelete   = "sql.delete"
	OpSQLDescribe = "sql.describe"
	OpSQLTest     = "sql.test"
	OpSQLAccess   = "sql.access"
	OpSQLHealth   = "sql.health"
	OpSQLLane     = "sql.lane"
	OpSQLImport   = "sql.import"

	// The Blackbox SQL set-up hand-over: mint (the person's Unravis), take (the elevated set-up helper), revoke (the person's Unravis,
	// when the set-up ends).
	OpSQLHandover       = "sql.handover"
	OpSQLHandoverTake   = keeperwire.OpSQLHandoverTake
	OpSQLHandoverRevoke = "sql.handover.revoke"

	OpMongoList     = "mongo.list"
	OpMongoDescribe = "mongo.describe"
	OpMongoRemember = "mongo.remember"
	OpMongoDelete   = "mongo.delete"
	OpMongoTest     = "mongo.test"
	OpMongoHealth   = "mongo.health"
	OpMongoLane     = "mongo.lane"
	OpMongoImport   = "mongo.import"
	OpMongoSetAside = "mongo.setaside"

	OpBBList        = "bb.list"
	OpBBGet         = "bb.get"
	OpBBFind        = "bb.find"
	OpBBAdd         = "bb.add"
	OpBBSetThisPC   = "bb.setThisPC"
	OpBBSetKey      = "bb.setKey"
	OpBBSetRecorder = "bb.setRecorder"
	OpBBRemove      = "bb.remove"
	OpBBHTTP        = "bb.http"
	OpBBImport      = "bb.import"

	// The MongoDB audit data plane (P14b): every name starts with ReservedPrefixP14b.
	OpAuditAdd       = "audit.add"
	OpAuditSources   = "audit.sources"
	OpAuditDelete    = "audit.delete"
	OpAuditClose     = "audit.close"
	OpAuditAnalyze   = "audit.analyze"
	OpAuditRequests  = "audit.requests"
	OpAuditPayload   = "audit.payload"
	OpAuditRecord    = "audit.record"
	OpAuditActivity  = "audit.activity"
	OpAuditBreakdown = "audit.breakdown"
	OpAuditCoverage  = "audit.coverage"
	OpAuditStats     = "audit.stats"
	OpAuditSlowest   = "audit.slowest"
	OpAuditRollup    = "audit.rollup"
	OpAuditSchema    = "audit.schema"
	OpAuditFields    = "audit.fields"
)

// ReservedPrefixP14b is the namespace of the MongoDB audit data plane (P14b).
const ReservedPrefixP14b = "audit."

// Chunk kinds of a stream.
const (
	chunkData = 'D'
	chunkErr  = 'E'
	chunkEnd  = 'Z'
)

// The wire format lives in keeperwire so the elevated Blackbox setup helper can ask for its hand-over without linking the keeper.
const (
	ProtocolVersion = keeperwire.ProtocolVersion
	MaxFrame        = keeperwire.MaxFrame
)

type (
	Request   = keeperwire.Request
	Response  = keeperwire.Response
	WireError = keeperwire.WireError
)

var errFrameTooLarge = keeperwire.ErrFrameTooLarge

// WriteFrame writes one length-prefixed frame.
func WriteFrame(w io.Writer, payload []byte) error { return keeperwire.WriteFrame(w, payload) }

// ReadFrame reads one length-prefixed frame.
func ReadFrame(r io.Reader) ([]byte, error) { return keeperwire.ReadFrame(r) }

func writeJSONFrame(w io.Writer, v any) error { return keeperwire.WriteJSONFrame(w, v) }
