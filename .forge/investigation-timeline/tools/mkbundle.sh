#!/usr/bin/env bash
# usage: mkbundle.sh <outdir> <file-or-dir>...   (paths relative to repo root; builds a single-commit slim repo + bundle)
set -e
out="$1"; shift
rm -rf "$out"; mkdir -p "$out/repo"
for p in "$@"; do
  if [ -d "$p" ]; then (tar -c --exclude=node_modules --exclude=dist --exclude=.svelte-kit "$p" | tar -x -C "$out/repo"); else mkdir -p "$out/repo/$(dirname "$p")"; cp "$p" "$out/repo/$p"; fi
done
cd "$out/repo"
git init -q -b main
git add -A
git -c user.name=unravis -c user.email=noreply@example.invalid commit -q -m "slim snapshot"
git bundle create "$out/repo.bundle" HEAD main >/dev/null 2>&1
ls -l "$out/repo.bundle" | awk '{print $5" bytes"}'
