# Update control file: pause, withdraw, staged rollout

A second signed file per channel, next to the manifest on the `update-feed` release:

- `rc-control.json` and `rc-control.json.sig`
- `stable-control.json` and `stable-control.json.sig`

It is a separate file because the manifest is read strictly: a new field there would make every installed copy refuse the feed. Older programs never ask for the control file, so they keep working. Versions must be written like `0.1.0-rc.48` (no `v`, no spaces); anything else is refused when signing. A missing file never lifts a hold a machine already remembers: to lift, publish a new control file with nothing set (run the script with no switches).

## What it can do

| Field | Meaning |
|---|---|
| `pause` | Offer nothing on this channel. |
| `blocked` | Versions that are never offered (a withdrawn release). |
| `rolloutVersion`, `rolloutPercent` | Only that percent of machines may take that one version. Any other version is not held back. |
| `note` | Short plain words, shown in the update status. |
| `seq` | The signing time in Unix seconds. A machine remembers the highest seq it ever accepted (even after that file expires) and ignores a lower one (replay). |
| `expires` | At most 90 days after `published`. A forgotten pause ends by itself. |

It can only hold a release back. It cannot install anything, and it cannot lower the version.

## How a machine uses it

1. The check reads and verifies the manifest as before.
2. Only if that release would be an upgrade, it reads the control file. Nothing extra is fetched when the machine is up to date.
3. The signature, the key role (an rc key signs only rc, a stable key only stable), the channel and the product are checked, with the same strict reading as the manifest.
4. Anything wrong, missing or unreachable: the last control file the machine accepted still holds until its own expiry. Never a block by itself.
5. A held release shows as "latest" with the reason in plain words (paused, withdrawn, or going out in stages). Manual "Check now" is held too.
6. Staged rollout: the machine draws a number 0 to 99 once per version and keeps it, so the answer does not flip between checks. It is never sent anywhere. Raise the percent to let more machines in; 100 lets all.

The Blackbox recorder uses the same check, so it follows the same control file.

## Use it

```
./tools/release/Set-Control.ps1 -Channel rc -KeyFile D:\secret\rc-1.key.json -Pause -Note 'checking a problem'
./tools/release/Set-Control.ps1 -Channel rc -KeyFile D:\secret\rc-1.key.json -Block 0.1.0-rc.54
./tools/release/Set-Control.ps1 -Channel stable -KeyFile D:\secret\stable-1.key.json -Rollout 0.1.0 -Percent 10
./tools/release/Set-Control.ps1 -Channel rc -KeyFile D:\secret\rc-1.key.json      # no switches: lift everything
```

Each run replaces the whole file, so say everything you want to hold in that run. The script builds the signing tool from a clean checkout equal to `origin/master`, shows the file, asks for `yes` (or `-Yes`), reads the passphrase from Proton Pass (`-ProtonPassRef`) or asks, signs, checks the signature against the keys in the checkout, and uploads.

## Limits

- A machine that has never been able to read the file cannot be held. The control file is a brake for machines that can reach the feed.
- A withdrawn version already installed stays installed. Rolling a machine back is a separate matter (see SILENT-UPDATE.md).
- Not verified on a live feed yet. The BL-155 item stays unticked until a control file is published and a PC shows the hold.
