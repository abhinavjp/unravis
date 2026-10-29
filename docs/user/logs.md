# Logs

Unravis keeps a plain-text log of what it did and what went wrong, so a
problem can be traced afterwards — when a read failed, you can see which
source, which stage and why, without re-running anything.

The log lives in the app's own data folder, in a `logs` subfolder
(`unravis.log`, with up to five rotated copies kept beside it as
`unravis.1.log` … `unravis.5.log`). The **Help** section of the app shows the
exact folder and a button that copies its path.

## What is logged

- Each start: the app version, the operating system, the port and the data
  folder, and whether the dashboard opened in the browser or a link was
  printed.
- Each request the app answers, as one line: a short request id, the route
  (never the full address), the status and how long it took. The same id is
  shown in error entries, so a problem in the app can be matched to the exact
  line.
- The journey: a source added or removed, an import started and finished
  (counts only), a case created, opened, archived, restored or erased, a
  guided analysis started and finished, an export started and finished, a
  settings change, a lock and unlock.
- Every error the app or this page shows, with a short class (for example
  `source_unreachable` or `validation`) and a cleaned-up message.
- Per source during an investigation: when the read started and finished,
  how long it took, how much came back, and — when it failed — the reason,
  including which time budget ran out.

## What is never logged

- Passwords, the one-time session token, case keys and connection strings.
  Error messages are passed through a filter that replaces anything looking
  like a credential, a `password=`-style value, a bearer token or a long
  base64 secret before it can reach the file, and file paths under your
  profile are written as `%USERPROFILE%`.
- Request and response bodies, audit rows and payloads, and anything typed
  into a field. The page reports screens by name and actions by name — never
  ids, searches or field contents.

## Sending the log

Open **Help** in the app, find **Support logs**, and copy the folder path.
Send the newest `unravis.log` to your contact; it contains no passwords or
case data.
