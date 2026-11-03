# TSK-10 lane-unread-input

risk: medium — a new field on the engine input, set from the server's lane-run logic.

## Outcome

`Input` carries `LaneUnread map[string]string` (lane kind → reason): the lanes that are configured but gave no usable reading. The server fills it. The engine does not read it yet.

## Write scope

- `internal/investigate/types.go`
- `internal/server/lanes.go`
- `internal/server/lanes_test.go` (append)

## Must not change

- Existing `Input` fields and their JSON tags. Engine behaviour. Goldens.
- Do not commit. Do not push. Never touch `sql/`.

## Changes

1. `types.go`: in `Input`, directly after the other lane fields (`Lanes`, `LaneOnsets`, `LaneSources`, `LaneGaps`; lines ~132-144), add `LaneUnread map[string]string \`json:"laneUnread,omitempty"\`` with the comment `// LaneUnread names a configured lane (kind → reason) that gave no usable reading, so it must not be skipped over when naming an origin.`
2. `lanes.go`, function `applyLaneRuns` (~439-486): read it first. For each lane kind that has a configured run overlapping the held day, decide "unusable" when ALL of these hold: the kind is NOT in `in.LaneOnsets`, AND NOT (the kind's run covers the whole window per `laneCovers` (~492) AND at least one of its answers is `AnswerNo`). Set `in.LaneUnread[kind] = "<reason>"`, creating the map lazily. Reason strings: `"configured but gave no readable onset"` when the kind has runs but no onset and no whole-cover answer; `"configured but does not cover the incident window"` when the run does not cover the window. A kind with no configured run at all is NOT unread (it is simply absent).
3. Do not change how `Lanes`, `LaneOnsets` or `LaneGaps` are filled.
4. Test, append to `lanes_test.go` (pattern: the existing test at ~472): `TestLaneUnreadIsSet` — a configured lane with a run that yields no onset and does not cover the window → `in.LaneUnread[kind]` is non-empty; `TestLaneUnreadNotSetWhenAnswered` — a lane with an onset (or a whole-cover run with an `AnswerNo`) → `LaneUnread` has no entry for it. Copy the setup from the existing test; change only what is needed.

## Proof

```
.dev\toolchains\go\bin\go.exe build ./...
.dev\toolchains\go\bin\go.exe test ./internal/server ./internal/investigate
```

Both PASS, goldens unchanged.

## Depends on

TSK-07.
