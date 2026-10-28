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
- Unravis and the keeper talk over a pipe on this PC only (`\\.\pipe\UnravisKeeper`). The pipe refuses callers from the network. Before it sends anything, Unravis checks that the pipe is served from the services session and is owned by a Windows service account, so a program in someone's desktop session cannot pretend to be the keeper and collect a password you type.
- The keeper writes one line per request to its own log (`audit.log` in its folder): when, which Windows account, which job, which saved connection by its id, and how it went. It never writes a password, a key, an address or a query window. The log is rotated at 4 MB, and eight older files are kept. The same refusal repeated many times is written once with a count, and each line is written before the answer goes back, so a flood of requests from one account cannot push older lines out of the log quickly. A request the keeper will not run (one it does not understand, from another version, or for a job it does not know) counts against a limit of 30 a minute per account, and only a short, cleaned form of its name, at most 32 letters, is written.
- The keeper limits how often one Windows account may ask for each kind of job in a minute (for example 30 connections to a server a minute). Past the limit it answers "too many requests" and does nothing. One Windows session may hold only half of the keeper's open connections, so one person cannot keep everyone else waiting. Connections whose session cannot be told share one such half between them.
- A password you type for this session only is still allowed. It is used for that one check and is not saved.
- In this setup, Show and Copy for a saved Blackbox read key are refused with a plain message: the key stays in the keeper.
- If the keeper is not running, the connections page says so and nothing that needs a saved password runs. Connections that need no password still work.
- Saved connections you made before the PC was set up for everyone stay in your own folder until you move them. The connections page offers the move. Your old files are removed only when you press Remove old copies and confirm, and only once every one of them is in the keeper under its own name. If someone else on this PC had already saved the same connection, the keeper keeps theirs, and the page asks you to confirm again before your old copies go.
- The keeper opens an audit source from a saved MongoDB link itself, with the password it stores, and reads the audit log for it. The Unravis process never holds a keeper-held MongoDB password, not even for a moment: Unravis asks for named, read-only reads (the analysis, a page of requests, one record, one payload, the timeline) and gets back only the answers. There is no request that runs a query you write, and none that returns a password. Before an answer or an error goes back, the keeper checks it for the stored password (as typed and as written inside JSON): an answer that holds it is refused, and an error has it cut out.
- Audit reads have their own limit of 600 a minute per Windows account, apart from the other jobs. The keeper's log writes them once a minute per account, job and outcome with a count, so a busy analysis cannot flood the log.
- The keeper keeps each Unravis window's audit sources apart: a window can read only the sources it opened. Sources of a window that has not asked for anything in 8 hours are closed. At most 16 windows, and 4 per Windows account, may have sources open in the keeper at once.
- The keeper runs the MongoDB checks, and opens audit sources, only for a saved link that signs in with a user name and password and names no certificate or key file on this PC. Any other saved MongoDB link is refused by the keeper with a plain message, so the keeper's service account is never used to reach MongoDB in a way a person could not.
- Cases you made before the PC was set up for everyone stay in your own Windows folder, sealed for you alone. Unravis in this setup does not open, show or move them, and it does not delete them. The start screen says how many there are.

### What the key keeper does not protect against

- An administrator of this PC, or a program running as SYSTEM, can stop or replace the keeper, run code as its service account, and read every saved password. The keeper protects saved passwords from the other people who sign in to this PC, not from its administrators.
- Cases, the host map, the erased lines and the ledger in the shared folder are sealed for this PC, so any account on the PC could unseal them. The folder's permissions are the only thing that keeps one person out of another's cases. For that reason Unravis uses the shared folder only when it is the very folder the installer made:
  - The installer writes a record under `HKEY_LOCAL_MACHINE\SOFTWARE\Unravis` that names the folder and the file IDs of the folder and of its `cases` folder. Only an administrator can write there, and Unravis and the keeper only read it. A folder that was moved or renamed into place, copied, or replaced has another ID or another path, and is not used. With no record there is no shared mode. The record itself must be owned by SYSTEM, Administrators or TrustedInstaller and must not let anyone else change it.
  - The folder and its `cases` folder must also be owned by SYSTEM, Administrators or TrustedInstaller, must not be a link to somewhere else, and must not let anyone else change their permissions, take them over, or delete or replace what is in them.
  - When any of this fails, Unravis keeps your files in your own folder instead and writes why in its log, and the keeper does not start.
- The installer must set exactly these permissions, or shared mode never turns on. On `C:\ProgramData\Unravis`: owner Administrators; SYSTEM, Administrators and the keeper's service account full control; Users read and add files and folders on the folder itself; Users modify only through entries that apply to what is inside (inherit only). On `cases`: the same, with inheritance from the parent turned off (a protected list), so the parent's modify entry does not arrive as a plain entry on `cases` itself, which the check refuses because modify includes delete. On `keeper`: SYSTEM, Administrators and the keeper's service account only.
- Every person on the PC can use, save again and forget any saved connection in the keeper. The keeper keeps the passwords from being read, not from being used.
- Files in the shared folder are locked machine-wide while one person writes them, so two people signed in at once take turns. Someone who takes that lock first, or makes it before Unravis does, can stop the others writing shared files (the host map, the ledger, adding or erasing a case) while they are signed in. They cannot read or change anything through it.

## Where a probe login is kept

Coming in P10.
