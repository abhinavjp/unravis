# Owner report: Slow call full-page screen, build W0 to W4 (2026-11-13)

## State
Built and unit-tested: models, copy, backend plumbing for lanes and light totals, the screen components, navigation (Esc, Back, Forward, reload) and the mount in the timeline page. The pop-up code is still in the repo (retirement is P-49, needs your go).
Gate after W4 (re-run by the coordinator): go build and go vet clean; svelte-check 488 files, 0 errors; web tests 2996 pass, 0 fail (baseline 2779); npm run build OK.
Reviews: RB-1 SHIP; RB-5 SHIP (2 rounds); RB-4 SHIP (2 rounds, Opus). Minor findings are in reviews/deferred.md.

## Verified on live data: NOTHING
All of the above is unit tests, source-text tests and demo only. Nobody has seen the screen in a browser yet (needs the Go backend, so P-42/P-46 after the local install).

## Blocked / skipped
- P-01 live field probe: the live source's field sample timed out at the 40 s server limit twice. Until it runs, live database, Redis and web-call time columns say "not recorded". P-34, P-36, P-38 skipped for that reason.
- P-40 live half needs a database with Query Store off.

## Questions for you
1. Confirm the live source database is reachable so P-01 can be re-run (your step).
2. User id 0 is refused by default in the nearby-user filter: ok?
3. Nearby calls and All sources stay separate (assumption); one IIS record may show twice in All sources, to look at on live data.
4. Back from the screen uses a history mark with no visible address change: ok?
5. Overview strip: one hour each side: ok?
6. Pop-up deletion (P-49): go or not?
7. Order and go for the later packets L-20..L-25.

## Next (needs your OK)
W5 to W11: server lanes (P-21, P-39), e2e specs, Opus security review (P-43), full gates, build rc.35 and local install, live browser acceptance (P-46), clumsy-user pass (P-47), backlog ticks (only when live-verified). Nothing committed, tagged, pushed or stashed: HEAD f6c819c, 17 tags, 0 stashes.
