# API Audit Log — what is stored

Source of truth: `brain-payroll/BrainPayroll.Service/Models/ApiAuditLog/ApiAuditLogServiceModel.cs`, written by
`RequestAndResponseLogHandler` (HTTP APIs) and `HangfireRequestAndResponseLogHandler` (background jobs).
Stored in Mongo collection `ApiAuditLog`. One document per API call or Hangfire job run.

## Lifecycle: two writes per document

1. **Insert** when the action starts (`OnActionExecuting`): identity, URL, user, `RequestInitiatedAt`, `CreatedOn`, `AuditCreateTime`.
2. **Update** when the action ends (`OnActionExecuted`), queued in memory and flushed in batches (default every 10 s): payloads, sizes, status, `RequestCompletedAt`, `UpdatedOn`, `ApiTime`, `PayloadRecordTime`.

A document with no `RequestCompletedAt`, `StatusCode` or `ApiTime` is a call whose second write never landed (crash, recycle, or an async job that returned 202 and never completed). It is not a fast call.

## Fields

| Field | Type | Possible values / meaning |
|---|---|---|
| `_id` | ObjectId | Generated at insert. Its embedded timestamp is the insert time. |
| `ControllerName` | string | API controller name without the `Controller` suffix (e.g. `Payroll`). For Hangfire: the job class's full type name, or `UnknownJobType`. |
| `ActionName` | string | Action method name. For Hangfire: the job method name, or `UnknownMethod`. |
| `RequestMainUrl` | string | Full absolute request URI including query string. For Hangfire: `hangfire:// AccountId: <id or Unknown>, HangfireJobId: <id or N/A>`. |
| `UserId` | int, nullable | Authenticated user from the token. `null` for anonymous calls and for jobs with no token. |
| `UserIp` | string | Client IP resolved by `ClientIpResolver` (proxy headers considered). `null` for Hangfire. |
| `UserAgent` | string | `User-Agent` header (can be empty). Always `Hangfire` for jobs. |
| `PayrollUIVersion` | string | Value of the UI-version request header; `null` if the client did not send it (API clients, jobs). |
| `RequestPollingType` | string | `Synchronous ` or `Asynchronous ` for HTTP (note the **trailing space** in the stored value), `Hangfire` for jobs. Async means the call was a long-running job tracked by async-job id. |
| `StatusCode` | string | HTTP status name from the .NET enum, e.g. `OK`, `Created`, `Accepted`, `BadRequest`, `Unauthorized`, `Forbidden`, `NotFound`, `InternalServerError`. Hangfire: `Succeeded` or `Failed`. Missing until the update lands. Stored as a **name, not a number**. |
| `RequestPayload` | string | Query params as JSON (if any) followed by the body (or bound action arguments if the body was drained). Truncated to the character limit. Placeholder strings instead of content: `Request Payload didn't capture due to the request body have files.`, `MS_HttpContext property in Request was null`, `Facing Exception while trying to capture request payload: <msg>`. `null` if not stored, if the controller/action is flagged as a sensitive entity, or for Hangfire. |
| `ResponsePayload` | string | JSON response body, truncated. Placeholders: `API configuration not allow to log response content.`, `Response Payload didn't capture due to the response content was type of the <media type>` (non-JSON), `Facing Exception while trying to capture response payload: <msg>`. Empty if no content. `null` under the same rules as the request payload. |
| `RequestLengthInKiloBytes` | double | Request body size in KB (2 decimals). `0` if not captured; this is the size before truncation. |
| `ResponseLengthInKiloBytes` | double | Response body size in KB (2 decimals). `0` if not captured, not JSON, or basic-details-only. |
| `RequestInitiatedAt` | DateTime (UTC), nullable | Moment the filter started. |
| `CreatedOn` | DateTime (UTC) | Moment the initial document was created (just after `RequestInitiatedAt`). |
| `RequestCompletedAt` | DateTime (UTC), nullable | Moment the action finished. `null` until the update lands. |
| `UpdatedOn` | DateTime (UTC), nullable | Moment the update was queued. |
| `AuditCreateTime` | double ms, nullable | `CreatedOn − RequestInitiatedAt`: cost of inserting the audit row **before** the action runs. |
| `ApiTime` | double ms, nullable | `RequestCompletedAt − CreatedOn`: the action's own execution time. |
| `PayloadRecordTime` | double ms, nullable | `UpdatedOn − RequestCompletedAt`: payload capture time, **after** the response. Not user-visible latency. |

User-felt latency ≈ `AuditCreateTime + ApiTime`.

## What can differ per call (configuration)

Per-call storage is decided by, in order: `[OverrideApiAudit]` on the action, then on the controller, then the global JSON config.

| Setting | Effect |
|---|---|
| `IgnoreAudit` | No document at all. |
| Action exclude list | No document for listed controller/actions. |
| `StoreRequestObject` / `StoreResponseObject` false | That payload is `null`. |
| `RequestObjectCharacterLimit` / `ResponseObjectCharacterLimit` | Payload cut to N characters; `-1` means no limit. Fallback defaults when the config file is missing: request 5000, response 1000. |
| `LogOnlyBasicDetails` | Response payload becomes the "not allow to log" placeholder; request arguments fallback is skipped. |
| Sensitive-entity controller/action | Both payloads forced to `null`. |
| `ThreadSleepTimeMillisecond` | Flush interval of the update queue (default 10000). Affects `PayloadRecordTime` and how long rows stay half-written. |

## Not stored

Request/response headers (except `User-Agent` and the UI version), auth tokens, DB/SQL timings, per-phase breakdown inside the action, server/host name, tenant/account id for HTTP calls (only embedded in the Hangfire URL), exception type or stack trace, and the correlation id (`AuditLogId` is only a header used internally).

## Quirks to remember

- `RequestPollingType` has a trailing space; trim before grouping.
- `StatusCode` is a string name; async submits show `Accepted` (202) and the real result is written on the later completion.
- Jobs skipped with `[SkipHangfireApiAuditLog]` produce no rows.
- Payloads are truncated, so a payload being short says nothing about request size. Use the KB fields.
