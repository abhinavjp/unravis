# Homestretch command line (BL-707)

One program, `homestretch`, also installed under the short name `hs`. The two names run the same code. It is the
command line of the Homestretch screen: everything the page can do, plus the release flows, from a terminal.

The code is `internal/hscli` (the commands) behind `cmd/homestretch` (a thin main). The engine commands of `unravis-ci`
(serve, run, run-pr, lint, runs, log, exclusive, claims, and the rest) live in `internal/cicli` and are reachable as
`homestretch ci <command>`. `unravis-ci` still works unchanged; round 4 item P8 turns it into a shim.

## Exit codes

| code | meaning |
|---|---|
| 0 | done |
| 1 | the workflow failed, or the change was refused (for example it would strand queued runs) |
| 2 | usage error, an I/O error, or a store that cannot be read (never treated as "free") |
| 3 | not supported: the command is known but not built yet (see "Not built yet") |
| 4 | stopped by a stop state. Reserved: nothing in the action registry can express a stop state today, so no command returns 4 yet |
| 5 | held by a freeze (`hold check`, `promote`, `release`) |

## Commands

Read commands take `--json`. Every command that looks at a store takes `--state-dir DIR`; without it the default CI
store of this PC is used. Value flags accept `--flag value` and `--flag=value`.

| command | what it does | registry action |
|---|---|---|
| `status [--project P]` | The queue, the machines, what ran last | read |
| `queue [list] [--state S]` | List the queue. S: queued, running, active, done, cancelled, superseded | read |
| `queue cancel <id\|pr> [--reason R]` | Cancel a queued or running item | `queue.cancel` |
| `queue retry <runid\|pr>` | Queue a finished run again | `queue.retry` |
| `queue top <id\|pr>` | Put a waiting item first in line | `queue.top` |
| `queue pause` | Stop machines taking new items | `queue.pause` |
| `queue resume` | Let machines take items again | `queue.resume` |
| `queue fallback <pr>` | Run a pull request on this PC through GitHub | engine command |
| `machines [list]` | The machines that run this project's work | read |
| `machines drain <name>` | Finish what runs, take nothing new | `machines.drain` |
| `machines resume <name>` | Withdraw a drain the machine has not acted on | `machines.resume` |
| `machines allow <name> --project o/r` | Let a machine run a project | `machines.allow` |
| `machines deny <name> --project o/r` | Stop a machine running a project | `machines.deny` |
| `policy show <o/r>` | Which machines may run a project | read |
| `policy set <o/r> --any\|--only m1,m2\|--except m1` | Set which machines may run a project | `policy.set` |
| `hold list [--all] [--project P]` | The freeze windows | read |
| `hold add --project P\|--all --until T --reason R` | Freeze Promote until a time | `hold.add` |
| `hold lift <id> --reason R` | End a freeze early | `hold.lift` |
| `hold check [--project P]` | Exit 5 while a freeze covers the project | read |
| `activity [--actor A] [--limit N]` | Who did what | read |
| `compare` | Local runs against what GitHub says | read |
| `promote [develop]` | Promote develop to master (see below) | none |
| `release <rc\|stable>` and `release --status` | Cut a release (see below) | none |
| `ci <engine command>` | The engine commands of unravis-ci, unchanged | n/a |
| `version [--json]` | Program and engine version | n/a |

Every registry action in `internal/hsaction` has a command above. The test `TestEveryRegistryActionHasACommand` is driven
from `hsaction.All()` and fails when an action is added without one. Changing commands call `hsaction.Run`, the same
path as the page's `POST /api/action/<id>`: the same checks, the same activity entry. The actor is `cli`, or the value of
`HOMESTRETCH_ACTOR` (`ui`, `cli` or `claude`) when it is set.

## promote and release

`promote` and `release` run the PowerShell flows that already exist, `tools/release/Promote-Develop.ps1` and
`tools/release/Release.ps1`, through `pwsh` (or Windows PowerShell when `pwsh` is missing). They are not registry
actions: they change a repository and GitHub, not the CI store.

- `hold check` runs first. A freeze that covers the project stops the command with exit 5 and the script is not started.
  A hold store that cannot be read stops it with exit 2. `release --status` only reads, so it skips the check.
- The script's exit codes 0 to 5 pass through. Any other code becomes 1.
- The script is found through `HOMESTRETCH_REPO` (the folder of the Unravis checkout) when set, otherwise by walking up
  from the current folder and then from the program's own folder until `tools/release/<script>` is found.
- Flags are checked before anything runs (project keys, repositories, commit ids, versions, branches). `promote` takes
  `--project`, `--up-to SHA`, `--repo`, `--state-dir`, `--dry-run`, `--no-merge`, `--skip-notes-check`,
  `--wait-minutes`, `--ci-wait-minutes`. `release` takes `--version`, `--requester`, `--repo`, `--branch`,
  `--wait-minutes`, `--yes`, `--dry-run` (the script's `-CheckOnly`) and `--status [--json]`.
- The signing key's passphrase is never read by the program. The script asks for it itself.

## Not built yet (exit 3)

These are in the design but have no backing code. They say so in plain words and exit 3. They do not pretend.

- `rollback <step>`
- `machines label <name> <label>`
- `machines enrol --name N --code C`
- `claim <pr> --by NAME` and `claim <pr> --fixed`
- `brief <pr>`

## Install

`go build -o homestretch ./cmd/homestretch`, then copy or link it as `hs` (on Windows `hs.exe`). The program calls
itself `hs` in messages when it was started under that name. Wiring the names into the installer and making `unravis-ci`
a shim is P8.
