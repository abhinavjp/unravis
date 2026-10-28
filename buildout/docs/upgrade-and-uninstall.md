# Upgrade and uninstall

Coming in P11.

This page explains how to move to a newer version and how to remove Unravis cleanly.

## Checking for a newer version

Screens in this part: `p12-update-none`, `p12-update-available`, `p12-update-offline`.

Unravis never checks for a new version by itself. You check when you want to: open **Glossary** in the header (the help panel) and press **Check for updates** at the foot. Unravis asks GitHub for the latest release of `abhinavjp/unravis`. The request goes to api.github.com. It sends nothing about you or your cases.

There are four answers:

- **You are on the latest version (v1.2.0).** Nothing to do.
- **Version 1.3.0 is available. You have v1.2.0.** A link, **Open the release page**, opens the release on GitHub.
- **Could not check for updates. Check your connection and try again later.** This also shows when you are offline or GitHub has rate-limited the check.
- **This is a development build, so there is nothing to compare.** Development builds do not check for updates.

Unravis makes no background calls. The check runs only when you press the button.

Installing a newer version is still your step. Download the installer from the release page, check it against `SHA256SUMS.txt` from the same release, and run it. It upgrades in place.
