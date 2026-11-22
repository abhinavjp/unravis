# BL-016 live harness: do the Query Store views return rows while the state is OFF?

One command, run on a PC that has a real SQL Server. It uses the app's own read (`internal/dbhealth` `RunSQLLane`) and prints a plain answer. It only reads: it sends only the app's fixed read texts, never runs `ALTER DATABASE`, never switches Query Store on or off, never writes. As a backstop, every text also passes an allowlist guard (the three error-log reads, otherwise one plain `SELECT` per text; nothing with `INTO`, `EXEC`, `sp_`/`xp_` names, a second comment or a semicolon). The guard is a second line, not a proof: the safety comes from the probe sending only the app's own fixed texts.

Nothing here is run by CI. The probe does nothing unless `UNRAVIS_LIVE_SQL_SERVER` is set.

## 1. Prepare an OFF database (you run these, by hand)

Do this on a test or dev SQL Server instance, never production, with a throwaway or restored database that does not matter. In SSMS, connected as a login that may alter the database:

```sql
-- 1. A scratch database.
CREATE DATABASE [unravis_qs_off_test];
GO
-- 2. Switch Query Store on, with short blocks so history appears quickly.
ALTER DATABASE [unravis_qs_off_test] SET QUERY_STORE = ON
  (OPERATION_MODE = READ_WRITE, INTERVAL_LENGTH_MINUTES = 1, QUERY_CAPTURE_MODE = ALL, DATA_FLUSH_INTERVAL_SECONDS = 60);
GO
-- 3. Make some queries, in that database, on a table of your own. Run this block twice, a minute or two apart.
USE [unravis_qs_off_test];
IF OBJECT_ID('dbo.t') IS NULL CREATE TABLE dbo.t (i int);
INSERT dbo.t VALUES (1);
SELECT * FROM dbo.t WHERE i = 1;
SELECT COUNT(*) FROM dbo.t;
EXEC sp_query_store_flush_db;
GO
-- 3b. Still ON, after the second run of step 3: check that history was captured. Both must show data; write the numbers down.
USE [unravis_qs_off_test];
SELECT COUNT(*) AS stat_rows FROM sys.query_store_runtime_stats;                       -- must be > 0
SELECT MIN(start_time) AS first_block, MAX(start_time) AS last_block FROM sys.query_store_runtime_stats_interval;  -- must not be NULL
GO
-- 4. Switch it OFF. History stays on disk; whether the views still return it is what the probe checks.
ALTER DATABASE [unravis_qs_off_test] SET QUERY_STORE = OFF;
GO
-- 5. Confirm (read only): desired and actual state should both be OFF.
USE [unravis_qs_off_test];
SELECT desired_state_desc, actual_state_desc FROM sys.database_query_store_options;
```

Optional second case, `OFF (FORCED)`: use `SET QUERY_STORE = OFF (FORCED)` in step 4 on a second scratch database. It is not available on every build; if the server rejects it, skip it.

A third case is a database that never had Query Store on: expect "NO" (nothing was ever captured, so that NO says nothing about OFF history).

When done, in SSMS (yours to run; leave the database first): `USE master; DROP DATABASE [unravis_qs_off_test];`

## 2. Run the probe

Needs Go installed on that PC (`go version`), and the repo checked out at the commit of the PR. From the repo root, in PowerShell.

Windows sign-in (the account running the command; it needs `VIEW DATABASE STATE`, or `VIEW DATABASE PERFORMANCE STATE` on 2022, in that database; a sysadmin has it):

```powershell
$env:UNRAVIS_LIVE_SQL_SERVER = 'localhost\SQLEXPRESS'   # your instance; 'host,1433' also works
$env:UNRAVIS_LIVE_SQL_TRUST_CERT = '1'                  # only if the server has a self-signed certificate
go run ./tools/dev/qsoffprobe -database unravis_qs_off_test
```

SQL login instead:

```powershell
$env:UNRAVIS_LIVE_SQL_SERVER = 'localhost\SQLEXPRESS'
$env:UNRAVIS_LIVE_SQL_USER = 'unravis_ro'
$s = Read-Host 'password' -AsSecureString               # not echoed
$env:UNRAVIS_LIVE_SQL_PASS = [Runtime.InteropServices.Marshal]::PtrToStringAuto([Runtime.InteropServices.Marshal]::SecureStringToBSTR($s))
go run ./tools/dev/qsoffprobe -database unravis_qs_off_test
Remove-Item Env:UNRAVIS_LIVE_SQL_PASS                    # when done
```

Without `UNRAVIS_LIVE_SQL_SERVER` it prints "refusing to run" and does nothing.

## 3. Read the answer

It prints: server version and edition, the database, Query Store desired and actual state, first and last kept block, rows read (blocks and their executions), the sentence the app would show on the Slow call screen, and a last line `VERDICT:`.

| Verdict | Meaning |
| --- | --- |
| `YES: the Query Store views returned rows while the state is OFF on this build.` | The OFF history read works on this build. |
| `NO: Query Store is OFF here and the views returned no history on this build.` | The views returned no rows while OFF. This means "not readable on this build" only if step 3b showed rows before the switch-off; otherwise nothing was captured, redo step 3. The app keeps today's message. |
| `NO ROWS READ: Query Store is OFF and reports kept blocks, but no rows came back for them.` | Odd case: send the whole output. |
| `NOT ANSWERED: this database is not in the OFF state ...` | The state was read and it is not OFF (wrong database, or step 4 was not run); redo step 1. |
| `NOT ANSWERED: the history could not be read ...` | The state or the history could not be read (permission, connection, timeout); the sentence above says which. Not a statement about the data. |

Send the whole output back, with the SQL Server version it prints. Record it in `docs/BACKLOG.md` under BL-016. BL-016 is ticked only after a real OFF run says YES (or "does not stay readable on build X" is agreed with the owner).

## What is verified

Verified without a server (fake runner): the printing, the allowlist guard (including bypass strings), the opt-in refusal and each verdict. NOT verified: any real SQL Server. Not run on a real OFF database yet.
