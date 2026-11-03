# TSK-11 lane-unread-tier

risk: medium — changes which runs can name an origin; behaviour-neutral when `LaneUnread` is empty.

## Outcome

A lane in `Input.LaneUnread` becomes an unread tier in `tiers()`, so `unreadTiers` lists it and a later tier can no longer be named the origin or turned Red past it.

## Write scope

- `internal/investigate/facts_parts.go`
- `internal/investigate/evaluate_test.go` (append)

## Must not change

- Behaviour when `Input.LaneUnread` is nil or empty (goldens stay identical).
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. In `tiers()` (`facts_parts.go` ~586-611), find where lane tiers are added from `p.in.LaneOnsets` (calls to the local `add(tier, at, why, tested)` closure). After that loop, add a second loop over `p.in.LaneUnread` (sort the keys first so output is deterministic): for each `kind` that is NOT a key of `p.in.LaneOnsets`, call `add(kind, nil, reason, false)` where `reason` is the map value. (`at` is a nil time pointer; `tested` false marks the tier unread. Match the argument types the closure already takes — read its definition.)
2. Tests, append to `evaluate_test.go`: copy the setup of `TestLaneOnsetsMakeTheOrigin` (~143). Add `TestUnreadLaneBlocksALaterOrigin`: same input plus `in.LaneUnread = map[string]string{"sql": "configured but gave no readable onset"}` and NO sql onset. Assert the origin is not Named at a later tier (it is `OriginNone`/NotEnough) and that the colour is not Red. Add `TestUnreadLaneNeedsAConfiguredLane`: `LaneUnread` empty → result equals the no-field run.
3. If a golden or an existing test changes, BLOCKED: paste the diff into `progress.md`.

## Proof

```
.dev\toolchains\go\bin\go.exe test ./internal/...
```

PASS, goldens unchanged.

## Depends on

TSK-10.
