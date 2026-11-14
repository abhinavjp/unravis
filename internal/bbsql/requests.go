// requests.go is the 15 s point-in-time sample of BL-101: the requests running
// on the instance right now, grouped by wait type, with the blocking counts the
// allowlisted bb:requests statement already aggregated in SQL. The only output
// is wait type, task count, max wait ms, blocked count, head-blocker count and
// max block ms — never a session id, login, host, program name, handle,
// statement offset or resource description.
//
// This sampler only decodes the rows the runner hands it: no SQL is built or
// parameterised (C1/C3), no wait-type text is invented (C9), and nothing is
// remembered between ticks — an instantaneous group-by has no baseline to
// subtract, so keeping the previous sample would only grow memory and hold rows
// for waits that no longer exist.
// Samplers are not safe for concurrent use and never are concurrent: the runner
// serialises every tick.
package bbsql

import "time"

// requestsSampler decodes bb:requests into []Requests. It is stateless.
type requestsSampler struct{}

func (requestsSampler) ID() string              { return "bb:requests" }
func (requestsSampler) Kind() string            { return "requests" }
func (requestsSampler) Interval() time.Duration { return BaseInterval }

// Decode returns the sampled groups and C6's truncated flag.
//
// decodeRows is the whole gate and is neither re-implemented nor widened here:
// it rejects every column outside the six the statement outputs (so a
// session_id, login_name, program_name, host_name, sql_text, handle, statement
// offset or resource_description cannot be decoded even if a driver returned
// one), validates each wait type against waitTypeRe, maps the empty wait type
// bb:requests emits for running requests onto the fixed WaitTypeRunning
// sentinel, and stops at CapRequests+1 rows so an over-cap reply keeps
// CapRequests and reports truncated. The row maps are passed through untouched:
// pre-filtering them here would bypass that rejection.
//
// Errors are exactly the fixed ones decodeRows returns, so no row content ever
// becomes an error (C10) and no partial sample is handed back. An empty reply
// means nothing was running: the payload is then missing (nil), never an empty
// array and never a zero.
func (s *requestsSampler) Decode(rows []Row) (any, bool, error) {
	var out []Requests
	truncated, err := decodeRows(rows, &out, CapRequests)
	if err != nil {
		return nil, false, err
	}
	if len(out) == 0 {
		return nil, false, nil
	}
	return out, truncated, nil
}

func init() {
	RegisterSampler(&requestsSampler{})
}
