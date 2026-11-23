package privacy

import "errors"

// Withheld is what Log and Error return when the run cannot be trusted to have hidden everything (see Incomplete).
const Withheld = "[withheld: identifiers could not all be tracked]"

// ErrIncomplete is what Result returns in the same case.
var ErrIncomplete = errors.New("privacy: identifier list incomplete, output withheld")

// Guard is what the broker holds for one run: the sweeper for identifiers and the text scrubbers. It is made once per run, so the
// tokens are the same for every host call, log line and error of that run.
//
// Fail closed: when a Learn was refused (too long, too many) the Guard is Incomplete, and Log, Error and Result return Withheld or
// ErrIncomplete instead of text that may hold a raw identifier. The broker should end the run with an error code.
type Guard struct {
	sw *Sweeper
}

// NewGuard returns the Guard for a run. allowIdentifiers is the operator's choice (the same switch as --mcp-allow-identifiers); a
// script cannot change it. With true nothing is tokenized and the run row carries allow_identifiers=1; secrets are scrubbed anyway.
func NewGuard(allowIdentifiers bool) *Guard { return &Guard{sw: NewSweeper(allowIdentifiers)} }

// AllowIdentifiers reports the mode.
func (g *Guard) AllowIdentifiers() bool { return g.sw.Allow() }

// Incomplete reports that a Learn was refused. Check it after every Learn and end the run when it is true.
func (g *Guard) Incomplete() bool { return g.sw.Incomplete() }

// Learn passes a raw identifier the broker saw to the sweeper (no effect in allow mode). It returns the error of the sweeper; the
// error means the Guard is now Incomplete.
func (g *Guard) Learn(kind Kind, raw string) error { return g.sw.Learn(kind, raw) }

// LearnAll passes a list of identifiers.
func (g *Guard) LearnAll(kind Kind, raws []string) error { return g.sw.LearnAll(kind, raws) }

// text runs the three steps in this order: the learned values first (a user agent holds a URL, and a scrubber that cuts the URL
// would break the match and leave the rest of the agent), then the scrubbers, then the whole sweep as a last net for addresses.
func (g *Guard) text(s string) string {
	if !g.sw.Allow() {
		s = escapeTokens(s)
	}
	s = g.sw.Learned(s)
	s = ScrubText(s)
	return g.sw.Text(s)
}

// Error is the text for an error that goes back to the script and into the audit row: scrubbed in every mode, identifiers
// tokenized in hidden mode, at most MaxErrorBytes.
func (g *Guard) Error(msg string) string {
	if g.sw.Incomplete() {
		return Withheld
	}
	return cutUTF8(g.text(msg), MaxErrorBytes)
}

// Log is the text for one console.log line (the broker applies the 64 KiB cap): scrubbed in every mode, tokenized in hidden mode.
func (g *Guard) Log(line string) string {
	if g.sw.Incomplete() {
		return Withheld
	}
	return g.text(line)
}

// Result is the JSON of a host call result or of the final result: tokenized in hidden mode, untouched in allow mode (a result is
// data, not free text, so the secret scrubber does not rewrite it). Invalid JSON is an error; do not pass the original on.
func (g *Guard) Result(raw []byte) ([]byte, error) {
	if g.sw.Incomplete() {
		return nil, ErrIncomplete
	}
	return g.sw.JSON(raw)
}
