# P-01 Live field-name probe

- Date: 2026-11-13
- Result: live-verified on 1 source
- Sources sampled: 1 (the only source connected; the app was not in demo mode)
- Documents sampled on that source: 5 (newest)
- Field paths returned: 46. Truncated: no.
- Field names and types only. No values were read.

Filter used on the path names (case-insensitive):
`sql|db|database|redis|cache|http|outbound|external|filter|hit|step|stage|pressure|cpu|memory|thread`

## Matching field paths

A type list such as `int, null` means some of the 5 documents hold a number and some hold null.

| path | types |
|---|---|
| BusyWorkerThreadsAtStart | int, null |
| HttpCallCount | int, null |
| HttpMilliseconds | double, null |
| PreActionFilterTimings | array, null |
| PreActionFilterTimings[] | object |
| PreActionFilterTimings[].CreatedOn | date |
| PreActionFilterTimings[].ElapsedMilliseconds | double |
| PreActionFilterTimings[].FilterName | string |
| PreActionFilterTimings[].HitCount | int |
| PreActionFilterTimings[].UpdatedOn | date |
| PreActionFilterTotalMilliseconds | double, null |
| RedisCallCount | int, null |
| RedisMilliseconds | double, null |
| RequestHitCount | int, null |
| SlowHttpCalls | null |
| SlowSqlCommands | null |
| SqlCommandCount | int, null |
| SqlMilliseconds | double, null |
| StepTimings | null |

## Perf roles

| role | chosen path | why |
|---|---|---|
| _perfHitCount | RequestHitCount | Name says hit count for the call; stored as a whole number. |
| _perfSqlCount | SqlCommandCount | Name says number of SQL commands; stored as a whole number. |
| _perfSqlMs | SqlMilliseconds | Name says SQL time in milliseconds; stored as a number. |
| _perfRedisCount | RedisCallCount | Name says number of Redis calls; stored as a whole number. |
| _perfRedisMs | RedisMilliseconds | Name says Redis time in milliseconds; stored as a number. |
| _perfHttpCount | HttpCallCount | Name says number of outbound HTTP calls; stored as a whole number. |
| _perfHttpMs | HttpMilliseconds | Name says HTTP time in milliseconds; stored as a number. |
| _perfSlowSql | none found | `SlowSqlCommands` fits by name, but it is null in all 5 documents, so the list shape and its element fields are not recorded. |
| _perfSlowHttp | none found | `SlowHttpCalls` fits by name, but it is null in all 5 documents, so the list shape and its element fields are not recorded. |
| _perfSteps | none found | `StepTimings` fits by name, but it is null in all 5 documents, so the list shape and its element fields are not recorded. |
| _perfPressure | none found | The role needs a list of name and value pairs. No such list was seen. `BusyWorkerThreadsAtStart` is a single number, not a list. |
| _perfTextStored | none found | No true/false field with a matching name was seen. |

## Notes

- The two roles already wired in `recordPerfFields` match the live source: `PreActionFilterTotalMilliseconds` is a number, and `PreActionFilterTimings` is a list whose elements hold `FilterName` (text) and `ElapsedMilliseconds` (number).
- `PreActionFilterTimings[].HitCount` is a per-filter count inside the filter list. It is not chosen for `_perfHitCount`; the call-level field `RequestHitCount` is.
- Other load-related single numbers exist on the same documents but fall outside the name filter: `InFlightRequestsAtStart`, `PendingWorkItemsAtStart`, `GcPauseMilliseconds`, `Gen2Collections`. None is a name and value list.
- `SlowSqlCommands`, `SlowHttpCalls` and `StepTimings` should be probed again on documents where they hold a value before any of the three is wired.
