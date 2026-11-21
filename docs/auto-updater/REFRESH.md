# Keeping the signed feed from running out

Every signed manifest has an `expires` date (180 days by default, 400 at most). After that date installed copies refuse it, so a long quiet time with no release freezes updates for everyone (the "freeze" problem). The status then reads "unverified", and the log says the feed was not refreshed.

## Fix: refresh the same release

```
./tools/release/Refresh-Feed.ps1 -Channel stable -KeyFile D:\secret\stable-1.key.json
./tools/release/Refresh-Feed.ps1 -Channel rc -KeyFile D:\secret\rc-1.key.json
```

- Reads the pair now on the `update-feed` release. It must be the newest published release of the channel, name the commit of its tag, verify against the keys in this checkout AND the keys of that release, and its files must still be published and match the hashes. The dates are not checked, so an already-expired file can be refreshed.
- Refuses if the channel is paused or the version is withdrawn (CONTROL.md), so a refresh never outlives the brake.
- Writes the same release again: same version, files, hashes and commit. Only `published`, `expires` (default 180 days) and `seq` (+1) change.
- Shows the signing-code and trusted-key changes since that release and asks you to confirm, like `Sign-Release.ps1`.
- If anything fails after signing, the working folder is kept (it holds the signed pair) and its path is printed.
- Signs (Proton Pass with `-ProtonPassRef`, else typed), checks the new pair, uploads, reads it back and verifies it.

Run it about every 90 days, or before a long quiet time. A normal release also resets the clock.

## Why this is safe

- Nothing new is installed: machines on this version see "latest", machines on an older one are offered the same release as before.
- `seq` goes up by one, so a machine that saw the old file accepts the new one, and an old file can never be played back over it. A real release's seq (CI run x 100) is always higher than any refresh of an earlier release, about 90 refreshes.
- The pause or withdraw brake is separate (CONTROL.md) and unaffected.

## Not verified live

Built and unit-tested only. BL-155 stays unticked until a refresh is published and a PC accepts it.
