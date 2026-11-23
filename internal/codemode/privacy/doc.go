// Package privacy is the pure part of code mode privacy (BL-365): what the broker does to text that leaves a host call or the
// sandbox, and the clock and random source the script sees. It has no dependency on the sandbox worker, the broker, the case
// store or the MCP server, so it runs and is tested on any OS. The broker (BL-363) calls it; nothing here spawns, reads files or
// opens a network.
//
// Where it sits (docs/desktop/spec/04-code-mode.md, D11 and D12):
//
//   - Hiding of user ids and user agents stays where it already is: the ten tools return "hidden-<rank>" when the operator has not
//     allowed identifiers (internal/mcp/breakdown.go). This package does NOT replace that. It is the second net: a Sweeper looks
//     through the final result, the logs and the errors for what must not be there, and gives every distinct value a token that is
//     the same for the whole run ([IP_1], [USER_2], [UA_1]), so a script that groups or counts by the token still can. A token
//     comes from the order in which values are first seen in one run, never from the value, so it says nothing across runs and
//     cannot be reversed or guessed from a dictionary.
//   - Scrub (ScrubError) removes what must never be in an error or a log line, in any mode: secrets (internal/secretscrub does
//     the secret forms), connection string parts, database and URL forms, file paths, SQL literals (internal/querytext does the
//     SQL shape), and cuts the text to a size.
//   - ScriptClock and Rand are the D12 clock and Math.random: 10 ms steps from a recorded base time, a frozen mode for replay, and
//     xorshift128+ with a recorded seed. They take their time from a function the caller gives, so tests never wait.
//
// Guard puts the three together for one run. Mode Allow turns the sweeper off (operator allowed identifiers) and nothing else:
// secrets are scrubbed in every mode.
//
// Rules the code keeps:
//
//   - Fail closed. A result that is not valid JSON is refused, never passed on raw. A JSON text nested deeper than maxDepth is
//     refused. A scrubber that cannot tell where a value ends masks to the end (secretscrub does the same).
//   - Linear time and bounded memory. Every input is cut or refused above a constant (MaxJSONBytes, maxScrubInput); no regular
//     expression here backtracks (Go regexp is RE2); the hand-written scanners move forward only. A run holds at most maxTokens
//     tokens per kind; the next distinct value gets one shared overflow token, so memory per run is bounded and nothing leaks.
//   - Errors from this package never carry the offending value.
//
// Not covered (listed so nobody assumes it): an identifier the sweeper was not told about and that is not an IP address (a plain
// user id inside free text); an IP written in a form that net/netip does not read (decimal "2130706433", octal, "1.2.3" short
// form, a v6 address inside a longer hex run); text in a Unicode look-alike; anything the tools return in allow mode.
package privacy
