# BL-140: prove the folders above the SQL service's settings file

Status: built 2026-11-21 (PR for this branch). Windows proof run on a real PC is still owed.

## Problem

`install-sql` and `enable-sql` prove the settings file (`--config`) is administrator-locked: trusted owner, a DACL that gives change rights only to SYSTEM and Administrators, and no reparse point in the folder chain. The folders above the file were not checked for their access list. Anyone who may delete the file, or delete or rename a folder above it, can put a file of their own there. The SQL service reads that file again at its next start, with no proof.

## Decisions

1. **Prove the whole chain, not only the parent.** Every folder from the file's parent to the volume root must have a trusted owner (SYSTEM, Administrators, TrustedInstaller) and an access list that gives delete-child, delete, change-ACL, change-owner or generic write/all only to SYSTEM, Administrators or TrustedInstaller. Reason: renaming `C:\a` away also removes `C:\a\b\settings.json`.
2. **Creating files and folders is allowed.** Add-file and add-subdirectory cannot replace an entry that exists, and stock `C:\`, `ProgramData` give them to ordinary users. A stricter rule would refuse every normal install.
3. **Inherit-only entries are skipped** (they do not apply to the folder they sit on). The stock CREATOR OWNER and Authenticated Users entries are of that kind.
4. **Refuse, do not repair.** The proof never edits a folder's access list. A refusal tells the operator to keep the settings in the program folder (the default) or lock the folders themselves.
5. **Prove the file again at every service start**, not only at install and enable. `runSQLService` calls the file proof (`VerifySettingsFileForStart`) before it reads the file. The service runs as `NT SERVICE\UnravisBlackboxSql`, which holds read on the settings file but only traverse on the program folder, so it cannot read a folder's access list; the folder half of the proof therefore stays at install-sql and enable-sql, run by an administrator. A file swapped in later is owned by whoever swapped it, and the file-only proof refuses an owner that is not SYSTEM or Administrators. A failure stops the start with one fixed sentence; the event log gets the code `sql: settings-not-locked`, never driver or path text. (An Opus review found the first draft ran the folder half at start too, which the service could not have read.)
6. **One rule, pure Go.** `adminOnlyDirDACL` decides from described entries, so it is unit-tested on any platform (`TestAdminOnlyDirDACLRule`). The Windows code only reads owners and lists (`verifySettingsFolderChain`) and fails closed on any read error.
7. **Not done:** a read through an already-open handle to close the window between proof and read. The service account cannot change the chain once the proof passes, and the start proof runs immediately before the read; the remaining window needs an administrator to be already compromised.

## Effects

- Default install (program folder under Program Files) passes: the stock lists contain only trusted trustees with change rights.
- A `--config` on a data drive root (for example `D:\x\settings.json`) is refused when `D:\` gives Authenticated Users modify on the root itself, which is the hole this item describes.
- A user-owned folder anywhere in the chain is refused. This is stricter than the SQL data folder rule, which also accepts the installing account as owner (`trustedOwner`): an administrator whose default owner is the creating user must own the folders as Administrators, or keep the settings in the program folder.

## Verified

- Linux: rule unit tests (14 cases plus the owner rule). `GOOS=windows go vet` clean for `internal/blackbox` and `cmd/unravis-blackbox`.
- Not verified: the real Windows reads on a PC (owner and list of `C:\`, `Program Files`, a data drive), a real service start under the service account (the file-only proof must pass there), and the new `cmd/unravis-blackbox` start tests, which only build on Windows.
