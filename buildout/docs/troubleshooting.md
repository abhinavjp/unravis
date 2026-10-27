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

Coming in P3.
