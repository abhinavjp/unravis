# Security

## What Unravis never does

- It never changes your SQL Server, your MongoDB or your logs. It only reads.
- It never sends anything over the internet. It listens on your own computer only.
- It never shows a saved password in the screens, in its log or in a case file.

## How you open it

Each start gives one one-time link. The link works once and expires after 10 minutes. After that, your browser holds a session for this run only.

## Keys: the PC key and a case key

Unravis can seal what it keeps on this computer with up to two kinds of key. Both are optional. The demo never asks for either.

- The PC key, which the screens call the master key. One key for all cases that do not have their own. You set it from the start screen ("Protect your cases with a master key") and type it once each time Unravis starts. If you choose Not now, those cases are sealed by your Windows sign-in only.
- A case key. One key for one case. It is separate from the PC key and from your Windows sign-in. Neither opens it.

How they are made and kept:

- A key is turned into a sealing key with Argon2id, a slow method built to make guessing hard. The key itself is never saved. Unravis keeps a short check code so it can tell a right key from a wrong one, and shows only the last four characters of it ("Key ending xxxx").
- The key is never written to the log, never shown in the screens, never put in an address and never saved in a case file.
- A wrong key changes nothing. After five wrong tries in a row, Unravis makes you wait: one second, then doubling, up to one minute.
- Master Lock in the top bar locks the PC key and every case key at once.
- There is no recovery. If you lose a case key, no one, including us, can open that case. Erase is the only way to free it. Use a key you can keep, for example in your password manager.
- Resetting a damaged PC key never touches a case that has its own key.

## Erase

Erase removes a case from this computer. You type the short case code first. It writes an Erasing mark before it removes anything, so a stop part way is finished on the next start. It leaves only a line with the case code and the time. A case file you saved is not touched. See "Erase a case" in the Cases page.

## Where a probe login is kept

Coming in P10.
