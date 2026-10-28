# Security

## What Unravis never does

- It never changes your SQL Server, your MongoDB or your logs. It only reads.
- It never sends anything over the internet. It listens on your own computer only.
- It never shows a saved password in the screens, in its log or in a case file. On a PC set up for everyone, it does not even hold one: the key keeper does (see below).

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

## Saved passwords on a PC set up for everyone (the key keeper)

When the installer sets Unravis up for everyone on a PC, it makes the shared data folder `C:\ProgramData\Unravis` and the Unravis Key Keeper service. From then on Unravis keeps its cases there, sealed for this PC, and it no longer keeps saved passwords itself.

- The key keeper holds every saved SQL Server password, every saved MongoDB link that has a password, and every Blackbox read key. It keeps them in its own folder (`C:\ProgramData\Unravis\keeper`), sealed with Windows data protection for the keeper's own service account, so no person's sign-in opens them.
- Unravis asks the keeper to do a fixed job by name: test this saved connection, run the fixed SQL Server or MongoDB checks with it, read these Blackbox files with this read key, save or forget this connection. The keeper does the job and sends back only the result. There is no request that hands a password or a read key back, and the keeper refuses any request it does not know.
- Unravis and the keeper talk over a pipe on this PC only (`\\.\pipe\UnravisKeeper`). The pipe refuses callers from the network. Before it sends anything, Unravis checks that the pipe is served from the services session, so a program in someone's desktop session cannot pretend to be the keeper and collect a password you type.
- The keeper writes one line per request to its own log (`audit.log` in its folder): when, which Windows account, which job, which saved connection by its id, and how it went. It never writes a password, a key, an address or a query window. The log is rotated at 4 MB, and one older file is kept.
- The keeper limits how often one Windows account may ask for each kind of job in a minute (for example 30 connections to a server a minute). Past the limit it answers "too many requests" and does nothing.
- A password you type for this session only is still allowed. It is used for that one check and is not saved.
- In this setup, Show and Copy for a saved Blackbox read key are refused with a plain message: the key stays in the keeper.
- If the keeper is not running, the connections page says so and nothing that needs a saved password runs. Connections that need no password still work.
- Saved connections you made before the PC was set up for everyone stay in your own folder until you move them. The connections page offers the move. Your old files are removed only when you press Remove old copies and confirm, and only once every one of them is in the keeper.

### Until P14b: audit sources from a saved MongoDB link

The MongoDB audit source (the audit log itself, not the fixed checks) is not yet read through the keeper. Until it is (P14b), adding an audit source from a saved MongoDB link goes through a temporary path: if your old per-user saved file still holds that link, Unravis opens it from there, as before; if only the keeper holds the password, Unravis asks you to type it for this session. The keeper never hands the password over for this. In P14b the keeper will read the audit source itself, and the temporary path goes away.

## Where a probe login is kept

Coming in P10.
