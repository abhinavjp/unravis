package privacy

import "errors"

// Withheld is what Log and the error calls return when the run cannot be trusted to have hidden everything (see Incomplete).
const Withheld = "[withheld: identifiers could not all be tracked]"

// ErrIncomplete is what the result calls return in the same case.
var ErrIncomplete = errors.New("privacy: identifier list incomplete, output withheld")

// IDs are the raw identifiers the broker found in one answer of a tool, before it hands the answer to the Guard. The broker takes
// them from the raw tool answer, not from the swept one.
type IDs struct {
	Users      []string // user ids, logins, e-mail addresses
	UserAgents []string
}

// Guard is what the broker holds for one run. It is the only way to the sweeper and the scrubbers, and it ties the two together:
// every call that brings host data in takes the identifiers of that data with it and learns them before it makes any output, so
// nothing can leave before its values were learned.
//
// Two sides, with different rules for text that looks like a token ("[IP_1]"):
//
//   - In (HostResult, HostError): raw data from a tool or a driver. Text that looks like a token is escaped ("[IP_1]" becomes
//     "[_IP_1]", "[_IP_1]" becomes "[__IP_1]", any letter case), once, so data cannot pass for a token of this run.
//   - Out (Output, Log, ScriptError): what the script made from what it got. Not escaped, because everything the script received was
//     escaped on the way in, so a token in it is a real one, and a replay that serves the stored swept answers gives the same text.
//     Stored swept answers must not be passed through HostResult again.
//
// Fail closed: when a Learn was refused (too long, too many) the Guard is Incomplete, and every call returns Withheld or
// ErrIncomplete instead of text that may hold a raw identifier. The broker should end the run with its own error code
// ("identifier_limit"), not the one for a script error.
type Guard struct {
	sw *sweeper
}

// NewGuard returns the Guard for a run. allowIdentifiers is the operator's choice (the same switch as --mcp-allow-identifiers); a
// script cannot change it. With true nothing is tokenized and the run row carries allow_identifiers=1; secrets are scrubbed anyway.
func NewGuard(allowIdentifiers bool) *Guard { return &Guard{sw: newSweeper(allowIdentifiers)} }

// AllowIdentifiers reports the mode.
func (g *Guard) AllowIdentifiers() bool { return g.sw.isAllow() }

// Incomplete reports that a Learn was refused. Every output call then withholds.
func (g *Guard) Incomplete() bool { return g.sw.isIncomplete() }

// learnIDs takes the identifiers in (no effect in allow mode) and returns the first error; the Guard is then Incomplete.
func (g *Guard) learnIDs(ids IDs) error {
	var first error
	for _, u := range ids.Users {
		if err := g.sw.learn(KindUser, u); err != nil && first == nil {
			first = err
		}
	}
	for _, u := range ids.UserAgents {
		if err := g.sw.learn(KindUA, u); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// HostResult is the JSON answer of a host call, on its way to the script: the identifiers are learned first, then the text is
// escaped and swept. Invalid JSON, invalid UTF-8, a text over MaxJSONBytes or a refused Learn is an error; do not pass the original
// on. In allow mode the JSON is only validated.
func (g *Guard) HostResult(raw []byte, ids IDs) ([]byte, error) {
	if err := g.learnIDs(ids); err != nil {
		return nil, err
	}
	if g.sw.isIncomplete() {
		return nil, ErrIncomplete
	}
	return g.sw.json(raw, true)
}

// HostError is the text of an error a host call or a driver gave, on its way to the script and into the audit row: identifiers
// learned first, then escaped, swept, scrubbed in every mode and cut to MaxErrorBytes.
func (g *Guard) HostError(msg string, ids IDs) string {
	if err := g.learnIDs(ids); err != nil || g.sw.isIncomplete() {
		return Withheld
	}
	return cutUTF8(g.text(msg, true), MaxErrorBytes)
}

// Output is the final result of the script (or any JSON it hands back): swept, not escaped. See Guard for why. Invalid JSON is an
// error. Allow mode validates only.
func (g *Guard) Output(raw []byte) ([]byte, error) {
	if g.sw.isIncomplete() {
		return nil, ErrIncomplete
	}
	return g.sw.json(raw, false)
}

// Log is one console.log line of the script (the broker applies the 64 KiB cap): scrubbed in every mode, tokenized in hidden mode.
func (g *Guard) Log(line string) string {
	if g.sw.isIncomplete() {
		return Withheld
	}
	return g.text(line, false)
}

// ScriptError is the message of an error the script itself raised, at most MaxErrorBytes.
func (g *Guard) ScriptError(msg string) string {
	if g.sw.isIncomplete() {
		return Withheld
	}
	return cutUTF8(g.text(msg, false), MaxErrorBytes)
}

// text runs the steps in this order. The input is cut first (at a place that cannot split an address, see capInput), so no step
// costs more than a constant per byte of the cap and a cut cannot leave the front of an address. Then the one normalisation
// (invisible characters, invalid bytes: see normalize), so every later step, and the learned values, see the same text. Then, in
// hidden mode, text that looks like a token is escaped (host data only), after the normalisation so an invisible character cannot
// hide a look-alike. The learned values go next (a user agent holds a URL, and a scrubber that cut the URL would break the match
// and leave the rest of the agent); then the addresses, before the scrubbers, so a scrubber that stops at a ":" (a UNC host) cannot
// cut the front off an address; then the scrubbers; then the addresses again as a last net, for what a scrubber joined.
func (g *Guard) text(s string, host bool) string {
	out := !host
	s = normalize(capInput(s))
	if host && !g.sw.isAllow() {
		s = escapeTokens(s)
	}
	s = g.sw.learned(s, out)
	s = g.sw.addresses(s, out)
	s = ScrubText(s)
	return g.sw.addresses(s, out)
}
