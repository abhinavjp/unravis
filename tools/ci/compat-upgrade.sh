#!/usr/bin/env bash
# BL-573: the upgrade path of the audit store, checked against the last promoted build.
#
#   tools/ci/compat-upgrade.sh [base-ref]        base-ref defaults to origin/develop
#
# 1. a case is written by THIS tree (TestCompatWriteCase: audit runs, calls, chain, code.hash_key, a few events);
# 2. the base ref is checked out in a scratch git worktree and gets a copy of internal/casestore/sqlite/compat_test.go;
# 3. the base build opens that case, checks integrity, reads, imports into it, closes and reopens it
#    (TestCompatReadCase), and the audit tables must still hold what the writer recorded.
# Prints one "COMPAT-UPGRADE PASS" or "COMPAT-UPGRADE FAIL: <reason>" line and exits 0 or 1. It never passes by
# skipping: both tests must report PASS, not SKIP, and a base tree that cannot compile the reader fails loudly.
# Run it from anywhere inside the repository. Needs git and go on PATH.
set -uo pipefail

BASE_REF="${1:-origin/develop}"
PKG=./internal/casestore/sqlite
SRC_REL=internal/casestore/sqlite/compat_test.go
WRITER_REL=internal/casestore/sqlite/compat_write_test.go

ROOT="$(git rev-parse --show-toplevel)" || { echo "COMPAT-UPGRADE FAIL: not inside a git repository"; exit 1; }
SCRATCH="$(mktemp -d)"
WT="$SCRATCH/base"
DATA="$SCRATCH/data"

cleanup() {
  git -C "$ROOT" worktree remove --force "$WT" >/dev/null 2>&1 || true
  git -C "$ROOT" worktree prune >/dev/null 2>&1 || true
  rm -rf "$SCRATCH"
}
trap cleanup EXIT

fail() {
  echo "COMPAT-UPGRADE FAIL: $1"
  exit 1
}

# A path the Go toolchain understands (Git Bash on Windows needs the mixed form; elsewhere it is a no-op).
native() { if command -v cygpath >/dev/null 2>&1; then cygpath -m "$1"; else printf '%s' "$1"; fi; }

[ -f "$ROOT/$SRC_REL" ] || fail "$SRC_REL is missing in the tree under test"
[ -f "$ROOT/$WRITER_REL" ] || fail "$WRITER_REL is missing in the tree under test"
BASE_SHA="$(git -C "$ROOT" rev-parse --verify --quiet "$BASE_REF^{commit}")" || fail "base ref '$BASE_REF' does not exist (fetch it, or pass another ref)"
echo "== base:   $BASE_REF ($BASE_SHA)"
echo "== branch: $(git -C "$ROOT" rev-parse HEAD)"

mkdir -p "$DATA"
git -C "$ROOT" worktree add --detach "$WT" "$BASE_SHA" >/dev/null 2>&1 || fail "git worktree add of $BASE_REF failed"

# The reader uses storetest.NewProtector with a fixed key; without it in the base tree the check cannot run.
grep -q 'func NewProtector(' "$WT"/internal/casestore/storetest/*.go 2>/dev/null \
  || fail "the base tree has no storetest.NewProtector (internal/casestore/storetest), so the fixed-key reader cannot run there"

echo "== 1/2 write the case with the branch under test"
WLOG="$SCRATCH/write.log"
( cd "$ROOT" && UNRAVIS_COMPAT_WRITE="$(native "$DATA")" go test "$PKG" -run '^TestCompatWriteCase$' -count=1 -v ) >"$WLOG" 2>&1
WRC=$?
cat "$WLOG"
[ $WRC -eq 0 ] || fail "the branch could not write its test case (exit $WRC, see the log above)"
grep -q -- '--- PASS: TestCompatWriteCase' "$WLOG" || fail "TestCompatWriteCase did not report PASS (skipped?)"
[ -f "$DATA/case-id.txt" ] && [ -f "$DATA/compat-expect.txt" ] || fail "the writer left no case-id.txt / compat-expect.txt"
echo "-- expectations recorded by the writer:"
cat "$DATA/compat-expect.txt"

echo "== 2/2 read the case with the base build ($BASE_REF)"
cp "$ROOT/$SRC_REL" "$WT/$SRC_REL"
# The base tree may carry an older writer test; it is not part of the check and may not compile against this reader.
rm -f "$WT/$WRITER_REL"
RLOG="$SCRATCH/read.log"
( cd "$WT" && UNRAVIS_COMPAT_READ="$(native "$DATA")" go test "$PKG" -run '^TestCompatReadCase$' -count=1 -v ) >"$RLOG" 2>&1
RRC=$?
cat "$RLOG"
if [ $RRC -ne 0 ]; then
  if grep -qE '\[build failed\]|\[setup failed\]|undefined:|cannot find package|imported and not used|declared and not used' "$RLOG"; then
    fail "the base build ($BASE_REF) cannot compile $SRC_REL: the file uses something only the branch has (see the compile errors above)"
  fi
  fail "the base build ($BASE_REF) could not use the case written by this branch (see the failure above)"
fi
grep -q -- '--- PASS: TestCompatReadCase' "$RLOG" || fail "TestCompatReadCase did not report PASS (skipped?)"

echo "COMPAT-UPGRADE PASS: base $BASE_REF ($BASE_SHA) opened, checked, imported into, closed and reopened a case written by this branch"
