# Diff coverage (BL-451)

Question it answers: of the lines a pull request adds or changes, how many does a test run? Old code is never judged.

Tool: `tools/ci/diff-cover.mjs` (self-test: `node tools/ci/test-diff-cover.mjs`, runs in the fast gate).

## Run it by hand

```
go test -coverprofile=go.cover ./internal/foo/...
node tools/ci/diff-cover.mjs --base origin/develop --head HEAD --go go.cover --min 80 --min-lines 10
```

The same command runs in the fast gate (`fast-build`, step "Go - diff coverage (report only)"), so the result is the same locally and in CI on the same diff and profile. `--lcov file` adds a Rust (`cargo llvm-cov --lcov`) or JS profile; `--diff file` reads a saved `git diff -U0`.

## Rules

- Only lines the profile knows as code count. Comments, blank lines, docs, and files not in the profile (Windows-only files on the Linux runner) are not counted. So a Windows-only change is not judged here; the Windows job runs those tests.
- Test files, `testdata`, and generated files (`// Code generated ... DO NOT EDIT.`, `@generated`) are left out.
- Fails when coverage of counted lines is under `--min` (80) and there are at least `--min-lines` (10) of them. A 3-line change is not judged on a ratio. A docs-only change has no counted line and passes.
- The fast gate runs it with `--report-only`: the table goes to the job summary and a `::warning::`, nothing blocks. Switching to blocking is removing one flag in `.github/workflows/fast-gate.yml`, after a few weeks of real reports show the threshold is fair.

## Not done yet

- Rust: the lcov reader is built and tested; no `cargo-llvm-cov` job feeds it (the desktop Rust is built on the Windows promotion job only).
- Web: the web tests are `node --test` with no coverage output, so there is no JS profile to read. Nothing to gate until a coverage reporter is added.
- Assertion-free tests can game any line-coverage gate; that is BL-452 (mutation tests).
