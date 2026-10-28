# Screenshots (for contributors)

The pictures in these pages are made by a script, so they match the program. They live in `docs/user/screenshots/<phase>/`, one file for each screen and theme, for example `p1-home-dark.png`.

## What you need

- Node.js 22.
- A built `dist\unravis.exe`. Build it with `.\build.ps1`.
- The tools, once: in `tools\screens`, run `npm ci`, then `npx playwright install chromium`.

## Take the pictures

From the main folder of the project:

```
.\tools\screens\run-screens.ps1
```

This starts the demo with a fixed clock and a set of made-up cases, in a temporary folder that is deleted afterwards. It opens every screen listed in `tools\screens\screens.json` and compares it with the saved picture. Any difference fails the run, and the differences are saved in `tools\screens\test-results`.

| Option | What it does |
|---|---|
| `-Update` | Writes new pictures instead of comparing. |
| `-Only p1-home,p1-glossary` | Runs only the screens you name. |
| `-Port 8081` | Uses a fixed port instead of one Windows picks. |

## Which computer makes the saved pictures

Pictures differ a little between computers, because of fonts and graphics drivers. The reference is the Windows runner used by the continuous check (`.github\workflows\ci.yml`). Use `-Update` on your own computer to look at the result, not to commit, unless the team agreed that your computer is the reference.

The first saved pictures were made on a developer computer, so until pictures from the runner are committed the continuous check only warns when a screenshot differs. To get the runner's own pictures: in GitHub, open Actions, then CI, then Run workflow, and tick "Regenerate screenshot baselines". The run takes new pictures on the runner and keeps them as the artifact `screenshot-baselines`. Unzip it over `docs\user\screenshots` in a clone and commit it. It includes `RUNNER-BASELINES.txt`; once that file is committed, a difference fails the check.

## Add a screen

Add an entry to `tools\screens\screens.json`:

- `id`: the name, starting with the phase, for example `p2-close`.
- `phase`: the folder, for example `P2`.
- `doc`: the page in `docs\user` that shows it.
- `narrow`: `true` to also take a narrow picture.
- `steps`: what to do, in order: `goto`, `click`, `fill`, `waitFor`, `scrollTo` or `theme`. A step may add `"when": "narrow"` or `"wide"`.

Then link the pictures from the page, using the id in the file name. Run the screens with `-Update`, then check the docs:

```
node tools\screens\docs-check.mjs
```

The check fails if an image link points to a missing file, if a screen is not mentioned in its page, or if a page shows a screen that is no longer captured.

## The contract screens are written against

Later phases write their screen steps against exactly this, so it does not change without a decision.

### Demo settings

`run-screens.ps1` sets these for the demo it starts. Both count only with `--demo`; without it they are ignored with a line in the log.

| Variable | Value | What it does |
|---|---|---|
| `UNRAVIS_DEMO_NOW` | `2026-10-20T09:00:00Z` | The demo clock stands still at this time. It is written in three places that must agree: `run-screens.ps1`, the default in `capture.spec.ts`, and the plan (decision B8). A value that is not a time stops the start. |
| `UNRAVIS_DEMO_CASES` | `1` | Seeds the capture-only cases below. It needs `AUDIT_CONFIG_DIR` to point at an empty folder (no real cases, no master key), and the start refuses otherwise. Plain `--demo` writes nothing. |

### The seeded cases

Screens click these by their title, for example `role=button[name="Open case Checkout slowdown, 17 October"]`.

| Title | ID starts with | State |
|---|---|---|
| Checkout slowdown, 17 October | `demoopen` | Open, one import |
| Night batch overrun, 10 October | `democlosed` | Closed 4 days before the demo time |
| Report export timeouts, 18 October | `demofailed` | Open, one import and one file the importer refused |
| Case from another folder | `demountrusted` | Shown as untrusted (the legacy check refuses it) |

Each ID is the name padded with `a` to 26 characters. A case made during a capture run (a screen that presses "Create case") gets `demonew` followed by a counter: the first is `demonewaaa...`, then `demonewbaa...`. They are the same on every run and never repeat one already in the folder.

### Narrow and wide

The header folds into a "More" menu below 1100 px. A step may add `"when": "narrow"` or `"when": "wide"` to run only on that side, so one screen can reach the same page both ways. The wide pictures are 1440 by 900 and the narrow ones 390 by 844.

### Exit codes of `run-screens.ps1`

| Code | Meaning |
|---|---|
| 0 | Every screen matched (or was written, with `-Update`). |
| 1 | The capture failed or a picture differed. |
| 2 | `dist\unravis.exe` is missing, or the tools are not installed. |
| 3 | The demo did not start. The log is printed. |
| 4 | Another screenshot run is going on this computer. |

### Adding a seeded state

Seeded cases are made by the `steps` list in `demoSeeder.seed` (`cmd\audit-explorer\demo_cases.go`). To add a state, add an entry there with a fixed ID from `demoCaseID`, a title, an age before the demo time, and a `fill` function that imports or changes the case. The first eight characters of every ID must differ from the others (evidence IDs carry only those), and `TestDemoCaseIDsAreValidAndDistinguishedByTheirFirstEightCharacters` checks it. Add the ID to `demoCaseIDs`, and a line to `TestDemoCasesSeedFourCasesWithFixedIDsTitlesAndTimes`.
