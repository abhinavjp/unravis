# TSK-04: Reachability probe for found services
risk: medium — opens a network connection (security-sensitive), user-triggered only.

## Outcome
`POST /api/probe/reachable` with `{host, port}` answers "can Unravis open a TCP connection to this address?" with `{reachable, reason}` (R11). The wizard uses it to confirm a found SQL Server or Redis, or an address typed for another computer. The scan itself still opens no connection (I1).

## Write scope
- `internal/server/probe.go` (new), `internal/server/probe_test.go` (new)
- `internal/server/routes.go` (one route entry)

## Must not change
- `/api/connections/test` behaviour.
- The scan's no-network rule.

## Changes
1. Handler `probeReachable`: validate host (hostname or IP, 1–253 chars, no scheme, path or userinfo) and port (1–65535); 3 s dial timeout; close immediately; send and read nothing; return only `reachable` plus one of a fixed set of reasons (`refused`, `timed-out`, `no-such-host`, `blocked`), never raw Go error text.
2. Rate limited with the same limiter family as `testConnection`; requires the session token.
3. Host rules: loopback and private ranges are allowed (the purpose is a LAN server); the cloud metadata address `169.254.169.254` and the unspecified address are refused.

## Proof
- Tests: a local `net.Listen` listener (reachable), a closed port (refused), a non-routable address with a short test timeout (timed-out), garbage host (400), metadata address (refused), rapid repeat calls past the limit (429), missing token (denied); the response never echoes the host inside an error string.
- `go test ./internal/server/...` green.

## Depends on
none
