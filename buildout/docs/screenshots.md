# Screenshots (for contributors)

The pictures in these pages are made by a script, so they match the program. They live in `buildout/screenshots/<phase>/`, one file for each screen and theme, for example `p1-home-dark.png`.

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

The first saved pictures were made on a developer computer, so until pictures from the runner are committed the continuous check only warns when a screenshot differs. To get the runner's own pictures: in GitHub, open Actions, then CI, then Run workflow, and tick "Regenerate screenshot baselines". The run takes new pictures on the runner and keeps them as the artifact `screenshot-baselines`. Unzip it over `buildout\screenshots` in a clone and commit it. It includes `RUNNER-BASELINES.txt`; once that file is committed, a difference fails the check.

## Add a screen

Add an entry to `tools\screens\screens.json`:

- `id`: the name, starting with the phase, for example `p2-close`.
- `phase`: the folder, for example `P2`.
- `doc`: the page in `buildout\docs` that shows it.
- `narrow`: `true` to also take a narrow picture.
- `steps`: what to do, in order: `goto`, `click`, `fill`, `waitFor`, `scrollTo` or `theme`. A step may add `"when": "narrow"` or `"wide"`.

Then link the pictures from the page, using the id in the file name. Run the screens with `-Update`, then check the docs:

```
node tools\screens\docs-check.mjs
```

The check fails if an image link points to a missing file, if a screen is not mentioned in its page, or if a page shows a screen that is no longer captured.
