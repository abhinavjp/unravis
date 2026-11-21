# Update a PC with no internet

Use this when a server cannot reach GitHub. You check the release on a PC that can, carry it on a USB stick, and check it again on the server before you install.

## 1. Get the files (on a PC with internet)

From the GitHub release page of the version you want, download into one empty folder:

- `update-manifest.json`
- `update-manifest.json.sig`
- `unravis-setup-vX.Y.Z-windows-x64.exe`
- any other file you need (`unravis-blackbox-…zip`, `unravis-lite-…zip`)

Copy the folder to the USB stick.

## 2. Check the folder (on the server)

Copy the folder to the server. Do not run anything yet. Open PowerShell in the folder and run the setup program that is **already installed** (or an older one you trust):

    .\unravis-setup.exe --verify .

It checks, offline, the same things an online update checks:

- the signature on `update-manifest.json` is from a key built into this program
- the release is not expired and is on the right channel (a stable build only accepts stable releases)
- the release is newer than this setup
- each file in the folder has exactly the size and SHA-256 the signed manifest lists

It prints `OK` for each file that matches and `not here` for a listed file that is not in the folder. It installs nothing and writes nothing. Exit code 0 means good. Any other result means **do not use the folder**. There is no option to skip a check.

## 3. Install

Run the checked `unravis-setup-vX.Y.Z-windows-x64.exe` from the same folder. For a silent upgrade add the values `--verify` printed:

    .\unravis-setup-vX.Y.Z-windows-x64.exe --silent --upgrade --config setup.json --expect-sha256 <sha256 printed> --expect-version X.Y.Z

## Extra check by hand

    Get-FileHash .\unravis-setup-vX.Y.Z-windows-x64.exe -Algorithm SHA256

The hash must equal the one `--verify` printed (and the one in `update-manifest.json`).

## Limits

- `--verify` does not remember what it has seen, so it cannot catch an old but validly signed release shown to you again. It does say when the release is not newer than the setup you run it with.
- A build with no trusted keys (a development build) refuses to verify anything.
- A proxy that breaks TLS only affects the online check. Offline import has no network step.

## 6. Automatic updates of a Blackbox from a folder or share (no internet)

A recorder on a server that cannot reach GitHub can still update itself, from a folder you fill:

1. On a PC with internet, download from the GitHub release page, into one folder: `rc.json` or `stable.json` and its `.sig` (from the `update-feed` release), and the `unravis-blackbox-vX.Y.Z-windows-x64.zip` of that release. Also put the channel's control file in the same folder: `rc-control.json` / `stable-control.json` and their `.sig` (from the `update-feed` release; if there is none, run `Set-Control.ps1` with no switches to publish a plain one). Without a valid, unexpired control file the recorder holds every new release, so deleting that file from a share cannot undo a pause or a withdrawal.
2. Put the folder where the server can read it, for example `\\fileserver\unravis-feed`. The recorder runs as the computer account, so that account needs read access to the share.
3. On the server, create `update-source.txt` in the recorder's program folder (administrators only can write there). Its first line is the folder, for example `\\fileserver\unravis-feed`.

Restart the recorder service once (the file is read when it starts). From then on it checks that folder on its normal timer. It checks exactly what an online recorder checks: the signature on the feed file, the size and SHA-256 of the zip against the signed manifest, the program hash inside the zip, and the rule that it never goes back to an older release. The signed files expire (feed files up to 180 days, control files up to 90), so refresh the folder from the release page at least that often. Only administrators should be able to write to the share: the checks stop a forged file, but anyone who can write there can still withhold updates. A wrong or tampered file in the folder is refused; a missing file shows as "Could not read the update folder" on its status page. The folder is not trusted because of where it is.

A folder-only server cannot see newer control files you did not copy in: someone who can write the share can serve it an older, still unexpired control file (up to 90 days) and so undo a later pause or withdrawal. Keep the share admin-only and copy the newest control file with each release. Setting `update-source.txt` also switches off the "managed by Unravis" handover on that server.

Updating the folder is all it takes to roll a new version out to every server that points at it. To stop, delete `update-source.txt` and restart the service: the recorder reads the internet again (or is updated by Unravis, if Unravis manages it).

Not verified on a live server yet (needs a Windows server and a share); unit-tested with a local folder only.
