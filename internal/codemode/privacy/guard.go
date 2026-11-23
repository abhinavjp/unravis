package privacy

// Guard is what the broker holds for one run: the sweeper for identifiers and the text scrubbers. It is made once per run, so the
// tokens are the same for every host call, log line and error of that run.
type Guard struct {
	sw *Sweeper
}

// NewGuard returns the Guard for a run. allowIdentifiers is the operator's choice (the same switch as --mcp-allow-identifiers); a
// script cannot change it. With true nothing is tokenized and the run row carries allow_identifiers=1; secrets are scrubbed anyway.
func NewGuard(allowIdentifiers bool) *Guard { return &Guard{sw: NewSweeper(allowIdentifiers)} }

// AllowIdentifiers reports the mode.
func (g *Guard) AllowIdentifiers() bool { return g.sw.Allow() }

// Learn passes a raw identifier the broker saw to the sweeper (no effect in allow mode).
func (g *Guard) Learn(kind Kind, raw string) { g.sw.Learn(kind, raw) }

// Error is the text for an error that goes back to the script and into the audit row: scrubbed in every mode, identifiers
// tokenized in hidden mode, at most MaxErrorBytes. The sweep runs after the scrub, so a token never lands inside a path that is then
// removed, and the cut keeps the result inside the cap.
func (g *Guard) Error(msg string) string {
	return cutUTF8(g.sw.Text(ScrubText(msg)), MaxErrorBytes)
}

// Log is the text for one console.log line (the broker applies the 64 KiB cap): scrubbed in every mode, tokenized in hidden mode.
func (g *Guard) Log(line string) string { return g.sw.Text(ScrubText(line)) }

// Result is the JSON of a host call result or of the final result: tokenized in hidden mode, untouched in allow mode (a result is
// data, not free text, so the secret scrubber does not rewrite it). Invalid JSON is an error; do not pass the original on.
func (g *Guard) Result(raw []byte) ([]byte, error) { return g.sw.JSON(raw) }
