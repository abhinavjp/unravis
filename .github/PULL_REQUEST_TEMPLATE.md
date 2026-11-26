## What and why
<!-- One or two sentences. The backlog id (BL-...) if there is one. -->

## Gate evidence (docs/ci/THREAD-GATE.md)
<!-- Paste the last lines of `bash tools/ci/prepush.sh`, or say which step could not run and why. "Checked" alone is not evidence. -->
- [ ] Branch contains the tip of `develop`; pushed once after the gate
- [ ] `bash tools/ci/prepush.sh`: ready to push
- [ ] No generated files (`web/dist`, `cmd/unravis-setup/ui`, `cmd/unravis-lite/ui`)
- [ ] New or changed tests are Windows-safe (`node tools/ci/windows-risk.mjs` is clean or each line has `// winok: <reason>`)
- [ ] Goroutines, locks, channels or timers added: `go test -race` run on these packages: <!-- list, or "none added" -->
- [ ] A test fails without this change (name it):
- [ ] Nothing added to a baseline or known-failure file (or: backlog id and reason)

## Failures that are not from this change
<!-- A test that also fails on a clean develop: name it and the backlog item. Otherwise "none". -->
