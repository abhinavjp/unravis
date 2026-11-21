# Second place to read the feed (G4)

The signed feed lives on the `update-feed` GitHub release. If `github.com` is rate limited, down, or blocked for one office address, installed copies can no longer check. A copy of the same files is kept on a branch and read from a second host:

- Main: `https://github.com/abhinavjp/unravis/releases/download/update-feed/<file>`
- Backup: `https://raw.githubusercontent.com/abhinavjp/unravis/feed-mirror/<file>`

## How it works

- The `mirror-feed` workflow copies `rc.json`, `stable.json`, the two control files and their `.sig` files to the `feed-mirror` branch (one replaced commit, so it never grows). It runs every 30 minutes, and the release scripts start it after they change the feed. It holds no key and copies bytes only.
- A program tries the backup only when the main address could not be reached (network error, 403/429/503, timeout). A refused answer (bad signature, rollback, expired) never moves on to the backup.
- Whatever the backup serves goes through the same checks: signature, key role, channel, expiry, seq floor, hash pin. A forged or older-than-seen copy is ignored and shows as "could not reach".
- A backup copy can be behind, so it cannot prove that a release was not paused or withdrawn since. It may say "a newer version exists", shown as on hold, but it never starts an install. The install waits for the main address. (The release files themselves are downloaded from github.com as before, so the backup only helps the check.)
- The control file (CONTROL.md) is read from the same address as the manifest.

## Limits

- The backup can lag by up to 30 minutes (or a few minutes more for raw.githubusercontent.com caching).
- It is the same company (GitHub), so it covers rate limits and one broken host path, not a full GitHub outage. A real second company (for example a free static host) can be added to `DefaultMirrorBases` later without changing anything else.
- Not verified live yet: the first run of the workflow and a PC reading from the backup. BL-155 stays unticked.
