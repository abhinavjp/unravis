package main

// The owner-run SQL grant and revoke scripts (C14). The Blackbox PRINTS these
// for the owner to run in SSMS; it never executes them — not from install-sql,
// not from remove-sql, not through the elevate helper. The SQL service runs
// under its own virtual account, so no password exists anywhere (BBX-5-01).

const grantScript = `-- Run in SSMS as an admin. The Blackbox never runs this for you.
CREATE LOGIN [NT SERVICE\UnravisBlackboxSql] FROM WINDOWS WITH DEFAULT_DATABASE=master;
GRANT VIEW SERVER PERFORMANCE STATE TO [NT SERVICE\UnravisBlackboxSql];
-- SQL Server 2019 and older: use VIEW SERVER STATE instead.`

const revokeScript = `-- Stop the service first, then run in SSMS:
REVOKE VIEW SERVER PERFORMANCE STATE FROM [NT SERVICE\UnravisBlackboxSql];
DROP LOGIN [NT SERVICE\UnravisBlackboxSql];`