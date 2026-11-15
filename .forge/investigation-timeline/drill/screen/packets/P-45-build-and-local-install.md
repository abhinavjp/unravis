# P-45 Build rc.35 and install it on this PC

model: sonnet, effort: low. Reason: fixed commands, but an installer run needs care when something is not as expected.
risk: medium. Review batch: RB-7. Wave: W8. Requirement: R-18 (owner rule: local install before any release).

HARD LIMITS: no git tag, no release, no push, no release CI, no signing service. Do not edit source files. Do not print the contents of `setup.json` if it holds a key or password.

## Outcome
The owner's installed Unravis on this PC is version `0.1.0-rc.35` built from this tree, and it starts.

## Write scope
- `dist/` build output (generated)
- `.forge/investigation-timeline/drill/screen/reviews/install.md` (new; the report)

## Must not change
- The repo sources, `build.ps1`, the owner's saved connections and cases (an upgrade keeps them; a fresh install must NOT be forced over an existing one).

## Changes
1. Read `docs/user/install.md` and `docs/user/upgrade-and-uninstall.md` for the silent keys and the upgrade switch. Use exactly what they document.
2. Check the last used version: `git tag --list "v0.1.0-rc.*" | tail -3` and the installed version (`unravis.exe --version` from the install folder the docs name). If rc.35 already exists as a tag or is already installed, use the next free number and say so.
3. Build: `powershell -File build.ps1 -Version 0.1.0-rc.35` from the repo root. Expected: exit code 0 and `dist/unravis-setup.exe` newer than the start time.
4. Close the running app the documented way (`unravis.exe --quit`), then upgrade: `dist/unravis-setup.exe --silent --upgrade` (add `--config setup.json` only if the docs say an upgrade needs it). If the installer asks for elevation, the owner approves it; do not work around it.
5. Start the app the documented way and check `--version` prints `0.1.0-rc.35`.
6. Write `install.md`: build command, exit codes, installed version, start result, the time. No secrets.

## Proof
- [ ] `build.ps1` exit code 0.
- [ ] Installed `unravis.exe --version` → `0.1.0-rc.35` (or the next free number, stated).
- [ ] The app opens and the owner's existing cases are still listed (count before = count after; counts only, no names in the report).
- [ ] `git tag --list` is unchanged from step 2; `git status --short` shows no new source change.

## Depends on
P-44 all PASS, P-43 verdict SHIP (or its blockers fixed).
