# Homestretch actions (BL-705)

Every change a person can make in Homestretch is one entry in `internal/hsaction`. The `unravis-ci` commands and the
page's `POST /api/action/<id>` both call `hsaction.Run`, so the checks, the activity entry and the change are the same
code either way. There is no promote or release action. The `homestretch` command line (`docs/homestretch/CLI.md`) has a command for every action below.

| id | CLI command | what it does |
|---|---|---|
| `queue.top` | `unravis-ci queue top <id\|pr>` | Move a waiting request to the front of the line. |
| `queue.cancel` | `unravis-ci cancel <id\|pr> [--reason R]` | Cancel a queued or running request. |
| `queue.retry` | `unravis-ci rerun <runid>` | Queue a finished run again for the same commit (the page: by run id only; the CLI also takes a PR number, which needs GitHub). |
| `queue.pause` | `unravis-ci queue pause` | Stop agents from taking new requests; running ones go on. |
| `queue.resume` | `unravis-ci queue resume` | Let agents take requests again. |
| `machines.drain` | `unravis-ci drain --name N` | Ask a machine to finish what it runs, take nothing new, then exit. |
| `machines.resume` | `unravis-ci machines resume N` | Withdraw a drain the machine has not acted on yet. |
| `machines.allow` | `unravis-ci machines allow N --project o/r` | Let a machine run a project. |
| `machines.deny` | `unravis-ci machines deny N --project o/r` | Stop a machine from running a project. |
| `policy.set` | `unravis-ci policy set <owner/name> --any\|--only m1,m2\|--except m1 [--dry-run] [--confirm]` | Set which machines may run a project. |
| `hold.add` | `unravis-ci hold add --project P\|--all --until T --reason R` | Hold Promote until a time. |
| `hold.lift` | `unravis-ci hold lift <id> --reason R` | End a hold early. |

`/api/session` lists the same table (`actions`) and `actionsOn` (whether this launch takes actions). It never serves the
action key (see step 4 below).

## Rules every action follows

- Machine names and project keys go through the same validators as the CLI.
- An edit that leaves queued runs with no machine (`policy.set`, `machines.allow`, `machines.deny`) is refused with the
  impact: the runs and how many. A dry run only measures. A stranding dry run also answers "refused", with the impact.
  `confirm` applies the edit anyway, and the activity entry says "confirmed although ...". The impact carries
  `stranded`, `strandedCount` and `base` (a stamp of the policy it was measured against). A confirm from the page must
  send `base` and `stranded` back from its dry run (400 without `base`, at most 500 ids). Inside the policy lock the edit
  is measured again, and it is refused (409, with the new impact) when the policy or the stranded runs changed since. The
  CLI's `--force` measures and applies in one step under the lock, so it sends neither.
- Each attempt that reaches an action writes one activity entry, refused, failed and invalid ones included, with an
  outcome (`done`, `refused`, `failed`, `invalid`, `error`). Secrets in the target, reason and detail are masked. A dry
  run writes nothing. The actor is `ui` for the page. For the CLI it is `--actor` where a command has it, else
  `HOMESTRETCH_ACTOR`, else `cli`. The value must be `cli`, `claude` or `ui` (any case, trimmed). Anything else is a
  usage error (exit 2) before the command runs.
- Text limits. A cancel reason is one line of at most 300 characters (no newline, tab or other control character); an
  empty one becomes "cancelled by <actor>". A longer or multi-line reason is invalid (exit 2, HTTP 400). A rerun id is at
  most 100 characters; a longer one is answered like an unknown id, "no run" (exit 1, HTTP 409), as before.
- A hold lasts at most 3650 days (`Nd` up to `3650d`, a duration up to `87600h`, a date up to 3650 days ahead); a longer
  one is invalid.
- A `policy.json` or `holds.json` that cannot be read as the store file it should be (not JSON, wrong types, a newer
  version, a hold with no id or end) is an error of the store (`error`, exit 2, HTTP 500), not invalid input.
- The answer is `{ok, message, impact?, error?}` in plain words. Exit codes: 0 done, 1 refused or failed, 2 invalid or
  I/O. HTTP: 200, 409, 400 and 500.

## The page's write channel

`POST /api/action/<id>` with `{"project": "<page project id>", "args": {...}}`. It is refused unless:

1. it is a POST (405 and `Allow: POST` otherwise) to a loopback Host (a DNS rebind gets 404);
2. it carries the page's session cookie (401);
3. `Origin` is the page's own `http://127.0.0.1:<port>`. With no Origin, `Sec-Fetch-Site` must be absent, `same-origin`
   or `none` (403);
4. `X-Homestretch-Token` matches the per-launch action key (32 bytes from crypto/rand, compared in constant time; 403).
   The key is not the session code and no route serves it. It travels only in the start link's fragment
   (`http://127.0.0.1:<port>/?t=<session>#a=<key>`), which a browser never sends to a server. The page reads it, keeps it
   in `sessionStorage` and removes it from the address bar. So anything that gets the session cookie (another loopback
   listener the browser sends it to) still cannot act. This holds for a POST with no `Origin` and no `Sec-Fetch-Site`
   too. A launch with no key answers 503;
5. `Content-Type` is `application/json` (415). The body is at most 16 KiB (413), is one JSON object, and has no unknown
   field in the envelope or the args (400);
6. the id is in the registry and the project is known (404).

The channel sends no CORS headers. Every other route stays GET or HEAD only. The refusals in this list never reach an
action, so they write no activity entry (the project is not known yet). The cookie, Origin and key refusals are written
as one line to the program's log (stderr): method, path (quoted, at most 120 characters) and remote address, never a
header value. When an action changed nothing but its activity entry could not be written, the answer says so ("The
activity log could not be written.") and the program logs it; when it changed something, the answer is 500. Messages on the page show "the CI store" in place of the folder path, and machine
and user names are redacted as on the other tabs.
