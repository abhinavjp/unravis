#!/usr/bin/env bash
# The gate every thread runs BEFORE `git push` (docs/ci/THREAD-GATE.md). It repeats what the fast gate (.github/workflows/fast-gate.yml,
# required check `fast-build`) and its advisory lint job will say, plus a Windows scan, so a red run on GitHub is no surprise.
#   bash tools/ci/prepush.sh [--base origin/develop] [--no-fetch] [--skip-tests] [--skip-web]
# Works in Git Bash on Windows and in a Linux container. Run it from anywhere inside the clone. Exit 0 = safe to push, 1 = fix first.
# Judges the committed HEAD for the diff-based steps (gofmt, windows-risk, scope); vet, tests and lint run on your working tree, so
# commit first and keep it clean. It writes to a temp dir, except `go mod tidy` (step 10), which rewrites go.mod/go.sum when they
# are untidy. It never runs `npm run build` (that rewrites the generated web folders, which must not be in a pull request).
set -uo pipefail

base="origin/develop"; fetch=1; tests=1; web=1
while [ $# -gt 0 ]; do
  case "$1" in
    --base) [ $# -ge 2 ] || { echo "--base needs a value" >&2; exit 2; }; base="$2"; shift ;;
    --no-fetch) fetch=0 ;;
    --skip-tests) tests=0 ;;
    --skip-web) web=0 ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
  shift
done

cd "$(git rev-parse --show-toplevel)" || exit 2
tmp="$(mktemp -d)"; trap '[ -n "${watch_pid:-}" ] && kill "$watch_pid" 2>/dev/null; rm -rf "$tmp"' EXIT
fails=(); warns=()
step() { printf '\n== %s\n' "$1"; }
fail() { fails+=("$1"); printf 'FAIL: %s\n' "$1"; }
warn() { warns+=("$1"); printf 'WARN: %s\n' "$1"; }

# A test must never open a real window (the "Unravis test" box, 2026-11-26). On Windows a watcher records every visible window that a
# *.test.exe opens while the tests run and closes it; the gate fails when it saw one. Elsewhere there is no desktop, so it does nothing.
watch_pid=""; watch_log="$tmp/test-windows.log"; watch_stop="$tmp/test-windows.stop"
watch_start() {
  case "$(uname -s 2>/dev/null)" in MINGW*|MSYS*|CYGWIN*) ;; *) return 0 ;; esac
  command -v powershell.exe >/dev/null 2>&1 || return 0
  : >"$watch_log"; rm -f "$watch_stop" "$watch_stop.ready" "$watch_stop.done"
  powershell.exe -NoProfile -ExecutionPolicy Bypass -File "$(cygpath -w tools/ci/watch-test-windows.ps1)" -Log "$(cygpath -w "$watch_log")" -StopFile "$(cygpath -w "$watch_stop")" -RootPid "$(cat /proc/$$/winpid 2>/dev/null || echo 0)" >/dev/null 2>&1 &
  watch_pid=$!
  local i; for i in $(seq 1 100); do [ -e "$watch_stop.ready" ] && return 0; kill -0 "$watch_pid" 2>/dev/null || break; sleep 0.2; done
  [ -e "$watch_stop.ready" ] || { warn "the window watcher did not start: this run cannot catch a test that opens a window"; watch_pid=""; }
}
watch_stop_and_judge() {
  [ -n "$watch_pid" ] || return 0
  : >"$watch_stop"; wait "$watch_pid" 2>/dev/null; watch_pid=""
  [ -e "$watch_stop.done" ] || warn "the window watcher stopped early: this run cannot vouch that no test opened a window"
  if [ -s "$watch_log" ]; then
    fail "a test opened a real window on the desktop (the watcher closed it): a test must use a fake box (tools/ci/no-real-dialogs.mjs)"
    sed 's/^/    pid|program|class|title: /' "$watch_log" | head -10
  fi
}

# On Windows the agent's CI jobs and these tests share one per-user first-instance name (the app's single-instance mutex): two runs
# at once fail with "another Unravis copy owns the name". desk_lock runs a command under the same machine-wide lock the agent takes
# (resource desktop-first-instance in .github/local-ci.yml). Elsewhere, or with no Go toolchain, it just runs the command.
desk_lock() {
  case "$(uname -s 2>/dev/null)" in MINGW*|MSYS*|CYGWIN*) ;; *) "$@"; return $? ;; esac
  if [ ! -x "$tmp/homestretch.exe" ]; then
    CGO_ENABLED=0 go build -o "$tmp/homestretch.exe" ./cmd/homestretch >/dev/null 2>&1 || { warn "could not build the CI lock helper: this run may collide with the CI agent"; "$@"; return $?; }
  fi
  "$tmp/homestretch.exe" ci exclusive desktop-first-instance -- "$@"
}

step "1. Branch is current with develop"
if [ "$fetch" = 1 ]; then git fetch --no-tags --quiet origin "+refs/heads/develop:refs/remotes/origin/develop" || warn "could not fetch develop; judging against the copy you have"; fi
if ! git rev-parse --verify --quiet "$base" >/dev/null; then fail "no $base to compare with (run: git fetch origin develop)"; printf '\nprepush: cannot continue.\n'; exit 1; fi
if [ -n "$(git status --porcelain --untracked-files=no)" ]; then warn "uncommitted changes: this gate judges the committed HEAD, commit first"; fi
if git merge-base --is-ancestor HEAD "$base"; then
  fail "this branch has nothing that develop does not have"
elif ! git merge-base --is-ancestor "$base" HEAD; then
  behind="$(git rev-list --count "HEAD..$base")"
  fail "branch is $behind commit(s) behind $base: merge or rebase it first (git merge $base), then run this again. The fast gate judges the merge with develop."
else
  echo "ok: branch contains $base"
fi

changed="$(git diff --no-renames --name-only "$base" HEAD)"
if [ -z "$changed" ]; then
  echo "nothing changed against $base"
  if [ "${#fails[@]}" -gt 0 ]; then printf 'prepush: NOT ready to push.\n'; exit 1; fi
  exit 0
fi

step "2. Generated web files are not part of the change"
bad="$(printf '%s\n' "$changed" | grep -iE '^(web/dist|cmd/unravis-(setup|lite)/ui)/' || true)"
if [ -n "$bad" ]; then
  fail "generated web files changed (the promotion rebuilds them):"
  printf '%s\n' "$bad" | head -10 | sed 's/^/    /'
  echo "  restore:  git restore --source=$base --staged --worktree -- web/dist cmd/unravis-setup/ui cmd/unravis-lite/ui"
  echo "  (new untracked files there: git clean -fd -- web/dist cmd/unravis-setup/ui cmd/unravis-lite/ui, after checking git status)"
else echo "ok"; fi

step "3. Scope"
scope="$(node tools/ci/fast-scope.mjs --base "$base" --head HEAD 2>&1)" || { fail "fast-scope failed: $scope"; scope=""; }
printf '%s\n' "$scope" | head -12
getv() { printf '%s\n' "$scope" | sed -n "s/^$1=//p" | head -1; }
go_on="$(getv go)"; web_on="$(getv web)"; packages="$(getv packages)"

step "4. Tests of the CI tools"
bad_t=0
for t in tools/ci/test-*.mjs; do node "$t" >"$tmp/t.out" 2>&1 || { bad_t=1; fail "$t"; tail -5 "$tmp/t.out"; }; done
node tools/ci/no-real-dialogs.mjs >"$tmp/t.out" 2>&1 || { bad_t=1; fail "a test shows a real window (tools/ci/no-real-dialogs.mjs)"; cat "$tmp/t.out" | head -20; }
node tools/security/check-action-pins.mjs >"$tmp/t.out" 2>&1 || { bad_t=1; fail "check-action-pins (a workflow action is not pinned)"; tail -5 "$tmp/t.out"; }
[ "$bad_t" = 0 ] && echo ok

gofiles="$(git diff --no-renames --diff-filter=d --name-only "$base" HEAD -- '*.go')"
if [ -n "$gofiles" ]; then
  step "5. gofmt of changed Go files (judged on the committed text)"
  unformatted=()
  while IFS= read -r f; do [ -n "$f" ] || continue; [ -z "$(git show "HEAD:$f" | gofmt -l)" ] || unformatted+=("$f"); done <<<"$gofiles"
  if [ "${#unformatted[@]}" -gt 0 ]; then fail "not gofmt-clean: ${unformatted[*]}  (gofmt -w them)"; else echo "ok"; fi

  step "6. Windows-risk scan (report; add '// winok: <reason>' to a line that is fine)"
  if node tools/ci/windows-risk.mjs --base "$base" --head HEAD >"$tmp/wr.out" 2>&1; then cat "$tmp/wr.out"; else cat "$tmp/wr.out"; fail "windows-risk scan crashed (see above)"; fi
  grep -q ' \[' "$tmp/wr.out" && warn "windows-risk found lines that tend to fail only on Windows: they pass here and go red after the merge"
fi

if [ "$go_on" = "true" ]; then
  step "7. go vet (this OS) and cross-vet for Windows and Linux"
  # shellcheck disable=SC2086
  go vet $packages || fail "go vet"
  # shellcheck disable=SC2086
  GOOS=windows go vet $packages || fail "go vet GOOS=windows"
  # shellcheck disable=SC2086
  GOOS=linux go vet $packages || fail "go vet GOOS=linux"

  step "8. tauri_sidecar build"
  CGO_ENABLED=0 go build -tags tauri_sidecar ./... || fail "go build -tags tauri_sidecar"
  if [ "$tests" = 1 ]; then watch_start; CGO_ENABLED=0 desk_lock go test -count=1 -tags tauri_sidecar . || fail "go test -tags tauri_sidecar ."; fi

  if [ "$tests" = 1 ]; then
    step "9. Tests of the affected packages, compared with the baseline (the fast gate's step)"
    # compare-go-failures.mjs picks docs/desktop/baseline-go-tests.windows.txt on a Windows host by itself.
    # shellcheck disable=SC2086
    [ -n "$watch_pid" ] || watch_start
    CGO_ENABLED=0 desk_lock go test -count=1 -timeout 10m -skip '^TestRunHost(KeepsRecordingWhenThePortIsBusyAndTakesItLater|StopWhileWaitingForAPortReturns)$' -json $packages >"$tmp/go-test.json"
    code=$?
    watch_stop_and_judge
    if [ ! -s "$tmp/go-test.json" ] || [ "$code" -gt 1 ]; then fail "go test did not run cleanly (exit $code)"
    else node tools/ci/compare-go-failures.mjs --json "$tmp/go-test.json" --baseline docs/desktop/baseline-go-tests.txt || {
      fail "new Go test failures (compared with the baseline)"
      echo "If a failure looks unrelated: run that test again twice on a clean develop. A test that fails there too is flaky (docs/ci/THREAD-GATE.md, flaky policy): do not 'fix' it in this change."
    }; fi
  else echo "(tests skipped)"; fi

  if printf '%s\n' "$changed" | grep -Eq '^go\.(mod|sum)$'; then
    step "10. go.mod and go.sum are tidy"
    go mod tidy && git diff --exit-code go.mod go.sum >/dev/null || fail "go mod tidy changes go.mod/go.sum: commit the result"
  fi

  step "11. Lint of the new code, as the Linux CI job sees it (advisory in CI, but it shows red on the pull request)"
  gl="$(command -v golangci-lint || ls "$HOME/go/bin/golangci-lint"* 2>/dev/null | head -1 || true)"
  if [ -n "$gl" ]; then GOOS=linux "$gl" run --new-from-rev="$base" || fail "golangci-lint (same rules as the PR's lint job)"
  else warn "golangci-lint not installed: go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0"; fi
else
  echo; echo "(Go not affected: Go steps skipped)"
fi

if [ "$web" = 1 ] && [ "$web_on" = "true" ]; then
  step "12. Web: type check and tests"
  if [ ! -d web/node_modules ]; then warn "web/node_modules missing: run  (cd web && npm ci)  once"
  else
    (cd web && npm run check) || fail "web: npm run check"
    (cd web && npm test) || fail "web: npm test"
    node tools/codemode/check-dts.mjs || fail "code mode declarations"
  fi
  echo "(The web build is not run here: it writes the generated folders. The fast gate builds it.)"
fi

printf '\n========================================\n'
for w in "${warns[@]+"${warns[@]}"}"; do printf 'WARN  %s\n' "$w"; done
if [ "${#fails[@]}" -gt 0 ]; then
  for f in "${fails[@]}"; do printf 'FAIL  %s\n' "$f"; done
  printf 'prepush: NOT ready to push (%d problem(s)).\n' "${#fails[@]}"; exit 1
fi
printf 'prepush: ready to push.\n'
