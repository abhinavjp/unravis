# Blackbox sources manifest (`sources.json`)

Blackbox finds where the logs of its server are and writes them to a small read-only file in its recordings folder. Servers (Unravis) reads it, so nobody types a folder by hand.

- **File:** `<recordings folder>\sources.json`. Also served at `GET /sources` (read key, same guard as `/manifest`).
- **Cost:** built in the background on start, then every 15 minutes. Only folder listings and a few small settings files are read (applicationHost.config, Redis settings, the registry keys SQL Server already uses). No log line is ever read. Each folder listing stops at 20000 entries (`truncated`). At most 200 entries.
- **Never contains:** log content, passwords, connection strings. Only paths, counts, sizes and times.

## Shape (schema 1)

```json
{
  "schema": 1,
  "host": "BG-SRV-UK-01",
  "version": "0.9.0-rc.68",
  "generated": "2026-11-27T20:00:00Z",
  "sources": [
    {"id":"recordings","kind":"blackbox","label":"Blackbox recordings","dir":"D:\UnravisBlackbox","pattern":"bb-*.ndjson",
     "origin":"settings","exists":true,"readable":true,"files":42,"bytes":1234567,"newestUnix":1795000000},
    {"id":"iis.site.1","kind":"iis","label":"IIS site Default Web Site","dir":"D:\Logs\IIS\W3SVC1","pattern":"*.log",
     "origin":"applicationHost.config","exists":true,"readable":true,"files":30,"bytes":9999,"newestUnix":1795000000}
  ]
}
```

| Field | Meaning |
|---|---|
| `id` | Stable between runs: `recordings`, `iis.site.<siteId>`, `iis.central`, `httperr`, `sql.<instance>`, `redis.<n>`, `evt.<channel>`, `app.<n>` |
| `kind` | `blackbox`, `iis`, `httperr`, `sqlserver`, `redis`, `winevents`, `applog` (the same kinds the pull job uses) |
| `dir`, `pattern` | The exact folder and the file name pattern (several joined with `|`). **The folder is the one to list, not its parent.** |
| `origin` | `applicationHost.config`, `registry`, `redis settings`, `settings`, `default` |
| `exists`, `readable` | The folder is there; Blackbox's own account could list it. `readable:false` with `err:"access-denied"` means the account, not your login. |
| `files`, `bytes`, `newestUnix` | Files that match `pattern`: count, total size, newest write time (Unix seconds, 0 or absent when none) |
| `err` | `not-found`, `access-denied` or `error` |
| `truncated` | The listing stopped at the scan limit |

## Rules for readers

- Use `dir` and `pattern` as they are. The pull lists one folder and does not go into sub-folders, so the IIS root `LogFiles` shows 0 files: the logs are in `W3SVC<id>` sub-folders, and each has its own entry.
- Unknown fields and unknown kinds are ignored. A change a reader cannot ignore raises `schema`.
- Treat the file as a hint: pull still checks the folder against its own allow list.
- A missing file means an older Blackbox (before this feature): fall back to the settings you have.
