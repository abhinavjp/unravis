# Troubleshooting

## The browser did not open

Start Unravis with `--no-browser`, then paste the one-time link from its window into your browser.

## The link says it has expired or was already used

The link works once and expires after 10 minutes. Restart Unravis to get a new one.

## Unravis will not start on my port

Leave out `--port` and Windows picks a free port for you.

## Health checks say they need a real server

In the demo, the health checks are off, because the demo has no SQL Server or MongoDB. Connect a real source to use them.

## Which version do I have?

Run `unravis.exe --version`.

## Files left behind after an archive or restore

Case files that an archive or a restore finished but did not commit are called left-over files. They appear under "Case files left over" in the health checks of the case. Unravis lists the name, the folder, when it was made and its size. It never deletes, moves or adopts one. Keep it, move it, or delete it yourself in File Explorer.

A temporary folder from a check (`.verify`) that could not be removed is reported as "Cleanup pending" and is removed the next time Unravis starts.

If an archive was stopped by a crash or a power cut, start Unravis again. The case comes back as it was; a half-written `.part` file is not a case file and can be deleted.
