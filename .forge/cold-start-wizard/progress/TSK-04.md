# TSK-04 reachability probe - done

## Files changed
- internal/server/probe.go (new)
- internal/server/probe_test.go (new)
- internal/server/routes.go (one route entry: POST /api/probe/reachable)
- internal/server/routes_test.go (one row in routeCases; see deviations)

## API
Request:  POST /api/probe/reachable   {"host": "192.168.1.20", "port": 1433}   (JSON, X-Audit-Token, local Origin/Host checks apply, 1 MiB body cap, unknown fields rejected)
Response 200: {"reachable": true|false, "reason": ""|"refused"|"timed-out"|"no-such-host"|"blocked"}   (reason is "" when reachable)
400: bad host (not hostname/IP; scheme, path, userinfo, port, whitespace, >253 chars, bad labels, all-digit last label) or port outside 1-65535. Error text never echoes the host.
429: more than 20 probes per minute per server (same connectionTestLimiter type as testConnection).

## Behaviour
- 3 s dial timeout (probeDialTimeout; tests shorten via probeTimeout). Connects, closes at once, sends/reads nothing. Only reachable+fixed reason returned; no raw Go error text, no body.
- Blocked (reason "blocked", never dialled): unspecified, 0.0.0.0/8, link-local unicast (169.254.0.0/16 incl. 169.254.169.254, fe80::/10), link-local/other multicast, 255.255.255.255, 100.100.100.200, fd00:ec2::254, IPv4-mapped forms. Loopback and private ranges allowed.
- Hostnames are resolved first; every resolved address is checked and the checked address is the one dialled (no DNS-rebinding gap). A name resolving to any blocked address is blocked.
- Validation and 400s happen before the rate limit is charged; limit is charged before any network activity.

## Tests (probe_test.go)
Local net.Listen reachable (and zero bytes sent), closed port refused, timeout (fake dialer + real TEST-NET address, bounded), unknown name no-such-host (fake resolver), metadata/link-local/unspecified blocked with no dial, name resolving to metadata blocked, private/loopback allowed, garbage host/port/body 400, no host echo in errors, token required (401), GET 405, no content-type 415, foreign Origin 403, oversize body 413, rate limit 429 past 20 and per-handler isolation, no raw error text.
`go test ./internal/server/...` -> ok (12.2s). go vet clean.

## Deviations
1. routes_test.go (outside Write scope) got one row {"/api/probe/reachable", ..., "POST"}: TestRouteTableCoversEveryRoute fails for any route without a row.
2. Limiter is stored in a package-level sync.Map keyed by *handler (probeLimiters in probe.go) instead of a new field on the handler struct, because server.go is outside scope. Could be folded into the handler struct later.
3. Rate cap is 20/min (probesPerWindow), not the 10 of connection tests, since a wizard checks several found services.
4. Server package build was blocked for a few minutes by another agent's in-progress internal/discover/verify.go edit (Finding.Verified/Unreadable undefined); resolved on its own, nothing touched by me.
