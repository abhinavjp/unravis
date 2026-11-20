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
