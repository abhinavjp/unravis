# Update signing keys: how to make and keep them

Free. Uses only the Go standard library. Do this once, on your own PC (not CI, not a cloud machine).

## Make the keys

Four keys: rc current (`rc-1`), rc backup (`rc-2`), stable current (`stable-1`), stable backup (`stable-2`).
**Use a different passphrase for each of the two backup keys and for stable vs rc.** Ask twice so a typo cannot lock you out.

Build the key tool first from a clean checkout of a commit you have reviewed (this is the one moment the raw private key exists, so do not run it from a folder with uncommitted code):

```powershell
git clone https://github.com/abhinavjp/unravis $env:TEMP\unravis-keys; cd $env:TEMP\unravis-keys; git checkout <reviewed-commit>
go build -mod=readonly -o D:\secret\sigtool.exe ./tools/release/sigtool
```

```powershell
function New-UpdateKey($role, $name) {
    $a = Read-Host "Passphrase for $name (12+ characters, plain keyboard characters only: letters, digits, punctuation)" -AsSecureString
    $b = Read-Host "Same passphrase again" -AsSecureString
    $pa = [System.Net.NetworkCredential]::new('', $a).Password
    if ($pa -ne [System.Net.NetworkCredential]::new('', $b).Password) { throw 'The two passphrases differ.' }
    $pa | & D:\secret\sigtool.exe keygen -role $role -out "D:\secret\$name.key.json" -pub "D:\secret\$name.pub"
}
New-UpdateKey rc rc-1
New-UpdateKey rc rc-2
New-UpdateKey stable stable-1
New-UpdateKey stable stable-2
```

(The passphrase goes in on standard input, never on the command line.)

- The `.key.json` files are the private keys, encrypted (PBKDF2-SHA256 600,000 rounds + AES-256-GCM). The tool refuses to overwrite a file.
- Keep the `-1` keys on two USB sticks. Keep the `-2` keys on a **different** stick kept somewhere else. Do not keep a passphrase with a stick.
- Never commit a `.key.json`. Never put one in CI or a cloud drive.

## Tell the programs to trust the public keys

Append the lines of every `.pub` file to `internal/update/trusted_keys.txt` (a line looks like `rc 3fa9...`) and commit it. Public keys are safe to publish. Day to day you sign with `-1`. A build with no keys in that file accepts no update at all.

## Sign a release

`tools/release/Sign-Release.ps1`: step 1 signs before Publish, step 2 updates the feed after Publish. The script builds the signing tool from the tag in a clean copy, so other code in your folder never sees the passphrase.

## If a private key leaks

1. Stop using it. Make a release signed with the backup key (`-2`) whose `trusted_keys.txt` **removes the leaked public key** and adds a new one. Copies that update to it no longer trust the leaked key.
2. Until a copy has taken that update, it still trusts the leaked key. A copy that never updates must be reinstalled by hand. This is the honest limit.
3. Rotate the other role's keys only if they were stored together with the leaked one.
