# Update signing keys: how to make and keep them

Free. Uses only the Go standard library. Do this once, on your own PC (not CI, not a cloud machine).

## Make the keys (4 keys: rc current + next, stable current + next)

```powershell
$env:UNRAVIS_KEY_PASSPHRASE = Read-Host "Passphrase (12+ characters)"   # type it; it is not saved
go run ./tools/release/sigtool keygen -role rc     -out D:\secret\rc-1.key.json     -pub D:\secret\rc-1.pub
go run ./tools/release/sigtool keygen -role rc     -out D:\secret\rc-2.key.json     -pub D:\secret\rc-2.pub
go run ./tools/release/sigtool keygen -role stable -out D:\secret\stable-1.key.json -pub D:\secret\stable-1.pub
go run ./tools/release/sigtool keygen -role stable -out D:\secret\stable-2.key.json -pub D:\secret\stable-2.pub
Remove-Item Env:UNRAVIS_KEY_PASSPHRASE
```

- Use a different passphrase for stable than for rc.
- The `.key.json` files are the private keys, encrypted (PBKDF2-SHA256 600,000 rounds + AES-256-GCM). The tool refuses to overwrite a file.
- Copy all `.key.json` files to two USB sticks and keep them apart. Do not keep the passphrase with them.
- Never commit a `.key.json`. Never put one in CI or a cloud drive.

## Tell the programs to trust the public keys

Append the lines of every `.pub` file to `internal/update/trusted_keys.txt` (a line looks like `rc 3fa9...`). Commit that file. Public keys are safe to publish. Only the `-1` and `-2` public keys are listed; day to day you sign with `-1`. If `-1` is ever lost or leaked, ship a release signed with `-2` that carries a new `-3` public key.

A build with no keys in that file accepts no update at all.

## Sign a release

See `tools/release/Sign-Release.ps1`: step 1 signs before Publish, step 2 updates the feed after Publish.

## If a private key leaks

1. Stop signing with it. Sign the next release with the `-2` key and list a new `-3` public key.
2. A copy that never updated and trusts only the leaked key must be reinstalled by hand. This is the honest limit.
