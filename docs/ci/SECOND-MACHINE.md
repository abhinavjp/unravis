# Second machine for the local pipeline (BL-586, slice 6 follow-up)

A second PC doubles the throughput (today: 2 slots on one PC, 7 to 14 minutes of queue per PR) and removes the "PC asleep means PRs wait" weakness. The claim and lease code already exists and was tested with two `serve` processes on one PC; nothing has run on a real second PC yet.

## What the owner must approve on the second PC

1. Log in to the PC (or give a remote session) and allow installing the build tools below.
2. Sign in to GitHub with `gh auth login` as the owner (a human step; the token needs Contents: write on the repository because claims are git refs, plus the status permission the agent already uses).
3. Allow a scheduled task or startup entry that starts `unravis-ci serve` hidden at logon.
4. Confirm the PC may be busy: the agent runs two jobs at a time, and pauses while a release holds the lock on that PC.

The secrets rule is unchanged: steps never see the token; only `serve` does.

## Install, in order

| Need | Why | How |
|---|---|---|
| Git for Windows (includes git bash) | the local "Linux" steps run in git bash | `winget install Git.Git` |
| Go (version in `go.mod`) | build and tests | `winget install GoLang.Go` |
| Node LTS | web build and its tests | `winget install OpenJS.NodeJS.LTS` |
| GitHub CLI | login, status posting | `winget install GitHub.cli` |
| A C compiler (gcc) | `go-race` needs cgo | `winget install BrechtSanders.WinLibs.POSIX.UCRT`, then check `gcc --version` in a new shell |
| golangci-lint | the lint job | same version as `ci.yml` |

Then: `gh auth login`, clone the repository (any folder), build the runner (`go build -o unravis-ci.exe ./cmd/unravis-ci`), and start it.

## Start it

```powershell
unravis-ci serve --repo abhinavjp/unravis --label second-pc
```

Add `--post-required --context ci.yml=local/build` only on the PC that should post the required checks after cutover; claims make sure one commit runs on one PC. Leave `--no-status` off only once phase 1 is on.

## Check it works

1. `unravis-ci claims --repo abhinavjp/unravis` lists both PCs (an id like `m-1a2b3c4d` each).
2. The Machines tab in the Releases app appears once two PCs are seen.
3. Open two throwaway pull requests; one run should show on each PC and no commit should run twice.
4. Tick BL-585 only after step 3 is seen on real hardware.

## Known limits

- The cross-PC view lags about 60 s.
- Done claims stay for about 7 days as tombstones.
- If the second PC lacks gcc, its `ci.yml` runs fail on `go-race`. Install gcc before enrolling it.
