# BL-140: prove the folders above the SQL service's settings file

Status: built 2026-11-21 (PR for this branch). Windows proof run on a real PC is still owed.

## Problem

`install-sql` and `enable-sql` prove the settings file (`--config`) is administrator-locked: trusted owner, a DACL that gives change rights only to SYSTEM and Administrators, and no reparse point in the folder chain. The folders above the file were not checked for their access list. Anyone who may delete the file, or delete or rename a folder above it, can put a file of their own there. The SQL service reads that file again at its next start, with no proof.

## Decisions

1. **Prove the whole chain, not only the parent.** Every folder from the file's parent to the volume root must have a trusted owner (SYSTEM, Administrators, TrustedInstaller) and an access list that gives delete-child, delete, change-ACL, change-owner or generic write/all only to SYSTEM, Administrators or TrustedInstaller. Reason: renaming `C:\a` away also removes `C:\a\b\settings.json`.
2. **Creating files and folders is allowed.** Add-file and add-subdirectory cannot replace an entry that exists, and stock `C:\`, `ProgramData` give them to ordinary users. A stricter rule would refuse every normal install.
3. **Inherit-only entries are skipped** (they do not apply to the folder they sit on). The stock CREATOR OWNER and Authenticated Users entries are of that kind.
4. **Refuse, do not repair.** The proof never edits a folder's access list. A refusal tells the operator to keep the settings in the program folder (the default) or lock the folders themselves.
5. **Prove the file again at every service start**, not only at install and enable. `runSQLService` calls the file proof (`VerifySettingsFileForStart`) before it reads the file (since PR 7 this is the fallback only; see 7). The service runs as `NT SERVICE\UnravisBlackboxSql`, which holds read on the settings file but only traverse on the program folder, so it cannot read a folder's access list; the folder half of the proof therefore stays at install-sql and enable-sql, run by an administrator. A file swapped in later is owned by whoever swapped it, and the file-only proof refuses an owner that is not SYSTEM or Administrators. A failure stops the start with one fixed sentence; the event log gets the code `sql: settings-not-locked` (since PR 7 followed by the fixed word in brackets), never driver or path text. (An Opus review found the first draft ran the folder half at start too, which the service could not have read.)
6. **One rule, pure Go.** `adminOnlyDirDACL` decides from described entries, so it is unit-tested on any platform (`TestAdminOnlyDirDACLRule`). The Windows code only reads owners and lists (`verifySettingsFolderChain`) and fails closed on any read error.
7. **Read through the already-open handle: written (BL-140 PR 7), not live-verified, with a TEMPORARY fallback.** The service reads through `ReadVerifiedSettings` (one open, proof on the handle, no second open by path); a nil reader refuses. Only if the reader's own refusal is an OPEN-stage `access` or `unreadable` (the open of the file was refused: this account cannot open the file, the open unknown of probe P1; every refusal carries a fixed stage, `pre-open`, `open` or `after-open`, and `access` or `unreadable` from a handle query, the final path, the owner or list read or the read itself is `after-open` and stops the start, because the file was already open and the one-handle proof was running) does it log one line and fall back to the by-path proof plus a read by path. Every other word, `sharing`, an unknown word, an untagged error and a nil reader stop the start with `sql: settings-not-locked (<word>, <stage>)`; the decision is taken from the reader's own error, not from the word clamped for the log. The fallback is weaker than the handle proof: it checks neither the link count nor the final path, it holds no share-deny-write during the read, it reads the whole file before any size check, and it keeps the old proof-to-read window open. **The `open` stage is not live-proven** until probe P1 runs on a PC: until then it is held only by pure tests, a source scan of the package and Windows CI tests. **Residual risk:** a person who controls a folder in the path (rename or delete-child, the BL-140 hole itself, which the service account cannot re-check) can swap in a file whose list denies the service account, force `access`, swap the real file back for the by-path proof and swap again before the read. This is the race that existed before PR 7, not a new one; `sharing` is not a fallback word because a held open with an oplock would tell that person the exact moment the proof starts. The fallback is removed once P1 passes with the verified reader succeeding as the service account (BACKLOG BL-140). Before PR 7 the window was: the service account cannot change the chain once the proof passes, and the start proof runs immediately before the read; the remaining window needs an administrator to be already compromised.

8. **Refuse network and device paths; resolve the final path once.** A `\\server\share` or `\\?\` path is refused (`settingsPathProblem`): the owner and list of a file on a share belong to the file server. The path is then resolved once to its final form (`resolveSettingsPath`, links and short names resolved) and the file proof and the folder chain both read that one path; a path that is not already in its final form is refused. (Review follow-up, 2026-11-21.) Only a plain `X:\` drive path is accepted (UNC, `\\?\`, `\??\UNC\`, `\??\GLOBALROOT\`, `C:foo` are refused), and the drive letter must be a fixed local disk volume (`GetDriveType` and `QueryDosDevice`), so a mapped share or a `subst` alias is refused. The start-time proof skips the final-path step (it lists folders, which the service cannot do) and judges the exact path string the service opens, with the reparse check still on.

## Effects

- Default install (program folder under Program Files) passes: the stock lists contain only trusted trustees with change rights.
- A `--config` on a data drive root (for example `D:\x\settings.json`) is refused when `D:\` gives Authenticated Users modify on the root itself, which is the hole this item describes.
- A user-owned folder anywhere in the chain is refused. This is stricter than the SQL data folder rule, which also accepts the installing account as owner (`trustedOwner`): an administrator whose default owner is the creating user must own the folders as Administrators, or keep the settings in the program folder.

## Verified

- Linux: rule unit tests (14 cases plus the owner rule). `GOOS=windows go vet` clean for `internal/blackbox` and `cmd/unravis-blackbox`.
- Not verified: the real Windows reads on a PC (owner and list of `C:\`, `Program Files`, a data drive), a real service start under the service account (the file-only proof must pass there), and the new `cmd/unravis-blackbox` start tests, which only build on Windows.

## Windows check, 2026-11-21 (PC, not elevated)

The first Windows run found a bug that was older than this change: `describeACEs` read the second value of `SECURITY_DESCRIPTOR.DACL()` as "present", but it is "defaulted". Every real access list looked absent, so every proof refused ("has no access list"). This also affected the existing file proof used by install-sql and enable-sql, so BL-141 (the live enable-sql check) could not have passed. Fixed, with a Windows test that reads a real folder list (`TestDescribeFileDACLReadsARealList`). The pure rule tests could not see it.

With the reader fixed, the folder rule gave (owners and entries read from the real folders):

| Folder | Result | Why |
|---|---|---|
| `C:\Program Files`, `C:\Windows` | accept | TrustedInstaller owns; SYSTEM, Administrators, TrustedInstaller hold change rights; Users only read |
| `C:\ProgramData` | accept | SYSTEM owns; Users hold read and create only |
| `C:\Users\Public` | accept | no delete or delete-child for ordinary users; they may add files (decision 2: not refused) |
| `D:\` on this PC | refuse | Authenticated Users hold modify on the root, including delete |
| a new folder under `D:\` or in the user profile | refuse | owned by the user's own account |
