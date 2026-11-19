# TSK-03 Folder and keep rules in the client
Risk: medium (validation mismatch with the server would block installs). Worker size: small. Reason: one regex change plus helpers mirroring config.go.

## Outcome
The client accepts UNC folders the server accepts (not a share root), and gives a folder notice kind for a path.

## Write scope
- `web/src/lib/blackbox.ts`
- `web/tests/blackbox.folder.test.cjs` (new)

## Must not change
`internal/blackbox/config.go`, existing exported names and their meaning in `blackbox.ts`, the Apply flow behaviour except UNC now passing.

## Changes
- `settingsProblem`: accept `X:\...` or `\\server\share\...`; keep `unsafeFolder` refusing a drive root, share root and Windows folders.
- Add `folderKind(path): 'drive' | 'unc' | 'invalid'`.
- Keep ranges as config.go: keep 1-365 whole days, maxGB 0.1-10000, minFree 1-50 (check that `settingsProblem` already matches; fix if not).

## Proof
`cd web && node --test tests/blackbox.folder.test.cjs tests/blackbox.test.cjs && npm test && npm run check`. Cases: `D:\x` ok, `\\srv\share\x` ok, `\\srv\share` refused, `C:\` refused, `C:\Windows\x` refused, `relative\x` refused.

## Depends on
None.
