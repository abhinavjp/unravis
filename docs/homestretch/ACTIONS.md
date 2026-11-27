# Homestretch actions (BL-705)

Every change a person can make in Homestretch is one entry in `internal/hsaction`. The `unravis-ci` commands and the
page's `POST /api/action/<id>` both call `hsaction.Run`, so the checks, the activity entry and the change are the same
code either way. There is no promote or release action.

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

`/api/session` lists the same table (`actions`) with the page's token.

## Rules every action follows

- Machine names and project keys go through the same validators as the CLI.
- An edit that leaves queued runs with no machine (`policy.set`, `machines.allow`, `machines.deny`) is refused with the
  impact: the runs and how many. A dry run only measures. A stranding dry run also answers "refused", with the impact.
  `confirm` applies the edit anyway, and the activity entry says "confirmed although ...".
- Each attempt that reaches an action writes one activity entry, refused, failed and invalid ones included, with an
  outcome (`done`, `refused`, `failed`, `invalid`, `error`). Secrets in the target, reason and detail are masked. A dry
  run writes nothing. The actor is `ui` for the page. For the CLI it is `cli`, or `HOMESTRETCH_ACTOR` (`cli` or `claude`)
  when that is set.
- The answer is `{ok, message, impact?, error?}` in plain words. Exit codes: 0 done, 1 refused or failed, 2 invalid or
  I/O. HTTP: 200, 409, 400 and 500.

## The page's write channel

`POST /api/action/<id>` with `{"project": "<page project id>", "args": {...}}`. It is refused unless:

1. it is a POST (405 and `Allow: POST` otherwise) to a loopback Host (a DNS rebind gets 404);
2. it carries the page's session cookie (401);
3. `Origin` is the page's own `http://127.0.0.1:<port>`. With no Origin, `Sec-Fetch-Site` must be absent, `same-origin`
   or `none` (403);
4. `X-Homestretch-Token` matches the per-launch token from `/api/session` (crypto/rand, compared in constant time; 403);
5. `Content-Type` is `application/json` (415). The body is at most 16 KiB (413), is one JSON object, and has no unknown
   field in the envelope or the args (400);
6. the id is in the registry and the project is known (404).

The channel sends no CORS headers. Every other route stays GET or HEAD only. The refusals in this list are not logged,
because they never reach an action. Messages on the page show "the CI store" in place of the folder path, and machine
and user names are redacted as on the other tabs.
