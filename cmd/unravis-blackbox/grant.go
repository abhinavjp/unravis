package main

// The owner-run SQL grant and revoke scripts (C14). The Blackbox PRINTS these
// for the owner to run in SSMS; it never executes them — not from install-sql,
// not from remove-sql, not through the elevate helper. The SQL service runs
// under its own virtual account, so no password exists anywhere (BBX-5-01).

const grantScript = `-- Run in SSMS as an admin. The Blackbox never runs this for you.
CREATE LOGIN [NT SERVICE\UnravisBlackboxSql] FROM WINDOWS WITH DEFAULT_DATABASE=master;
GRANT VIEW SERVER PERFORMANCE STATE TO [NT SERVICE\UnravisBlackboxSql];
-- SQL Server 2019 and older: use VIEW SERVER STATE instead.
GRANT VIEW ANY DATABASE TO [NT SERVICE\UnravisBlackboxSql];
-- VIEW ANY DATABASE is what makes the per-database file statistics (BL-119)
-- answer for every database instead of only the ones this login can already
-- see. It is a visibility right, not a data right: it names no database to
-- anyone and the Blackbox reads counts and ids only. It is inside the C8
-- ceiling, which proves only that the login holds no more SERVER-LEVEL
-- rights: the ceiling queries read the effective server rights from
-- sys.fn_my_permissions(NULL, N'SERVER') and the fixed server-role flags,
-- so a login with more server rights is refused at every connect and no
-- sample runs on an unproven connection. DATABASE-LEVEL rights are OUTSIDE
-- that server-scope proof: db_owner of one database, for example, passes it
-- unchanged. The per-database ceiling check is DEFERRED TO THE OWNER - it
-- needs a new allowlisted statement and a live SQL Server. Nothing here
-- checks it.`

const revokeScript = `-- Stop the service first, then run in SSMS:
REVOKE VIEW ANY DATABASE FROM [NT SERVICE\UnravisBlackboxSql];
REVOKE VIEW SERVER PERFORMANCE STATE FROM [NT SERVICE\UnravisBlackboxSql];
-- SQL Server 2019 and older have no VIEW SERVER PERFORMANCE STATE: the grant
-- used VIEW SERVER STATE there. Revoke whichever right was granted (uncomment
-- the matching line and remove the other):
-- REVOKE VIEW SERVER STATE FROM [NT SERVICE\UnravisBlackboxSql];
DROP LOGIN [NT SERVICE\UnravisBlackboxSql];`
