# Spec for packet CPU (id cpu)
Goal (SQL evidence cards):
1. The CPU card heading and nutshell use the wording "CPU is high; here are the top waits and queries" (find the card by grep in `parts/13-*.js`/`parts/14-*.js` fixtures and scenario data; the card id contains `cpu`).
2. Query tables get a Copy SQL / query-ID action: use `evQueryId` and `evCopy` (parts 12-evidence-g/h). Plan XML is NOT shown: show the query ID with a Copy button and the text "Open the plan with showplan in SSMS or Azure Data Studio using this query ID."
3. Per-query wait column only when the card's Query Store wait stats are available; otherwise omit the column and show the note "Per-query wait types require SQL Server 2017+ with Query Store wait stats enabled."
4. Query Store off: banner only (existing dep banner).
Read the table renderer `evTable` in `parts/12-evidence-*.js` and the dependency data `EVDEP_MAP` in `parts/14-deps-data.js`. Packet id `CPU`, file `CPU-wording-waits.md`, check `data/chk-cpu.js`.
