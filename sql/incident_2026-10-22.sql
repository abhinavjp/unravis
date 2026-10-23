-- API slowness analysis, 22 Oct 2026 (Dubai time, UTC+4). Read-only; SQL Server 2016+.
-- Run in the API's database (Query Store is per-database). Windows A/B = suspect peaks, C = baseline guess; edit below.
SET NOCOUNT ON;
SET QUOTED_IDENTIFIER ON;

DECLARE @day_from datetimeoffset = '2026-10-22 00:00:00 +04:00',
        @day_to   datetimeoffset = '2026-10-23 00:00:00 +04:00';

CREATE TABLE #w (label char(1), f datetimeoffset, t datetimeoffset);
INSERT #w VALUES
  ('A', '2026-10-22 14:00:00 +04:00', '2026-10-22 17:00:00 +04:00'),
  ('B', '2026-10-22 18:30:00 +04:00', '2026-10-22 20:30:00 +04:00'),
  ('C', '2026-10-22 10:00:00 +04:00', '2026-10-22 12:00:00 +04:00');

-- 0. Environment + Query Store health (if actual_state_desc <> READ_WRITE or qs_oldest is after 22 Oct, history is missing)
SELECT DB_NAME() db, CAST(SERVERPROPERTY('ProductVersion') AS varchar(20)) ver, SERVERPROPERTY('EngineEdition') edition,
       DATEDIFF(MINUTE, GETUTCDATE(), GETDATE()) / 60.0 server_utc_offset_h,
       o.actual_state_desc, o.query_capture_mode_desc, o.interval_length_minutes, o.current_storage_size_mb,
       (SELECT MIN(start_time) FROM sys.query_store_runtime_stats_interval) qs_oldest
FROM sys.database_query_store_options o;

-- 1. Hourly load (Dubai). Look for the hour where avg_ms/max_ms/aborted jump. aborted = client-side timeouts/cancels.
SELECT CONVERT(char(13), SWITCHOFFSET(i.start_time, '+04:00'), 120) dubai_hr,
       SUM(rs.count_executions) execs,
       CAST(SUM(rs.count_executions * rs.avg_duration) / NULLIF(SUM(rs.count_executions), 0) / 1000 AS decimal(12,1)) avg_ms,
       CAST(MAX(rs.max_duration) / 1000 AS decimal(12,0)) max_ms,
       CAST(SUM(rs.count_executions * rs.avg_cpu_time) / 1000000 AS decimal(12,0)) cpu_s,
       CAST(SUM(rs.count_executions * rs.avg_logical_io_reads) / 1000 AS decimal(14,0)) reads_k,
       SUM(CASE WHEN rs.execution_type = 3 THEN rs.count_executions END) aborted,
       SUM(CASE WHEN rs.execution_type = 4 THEN rs.count_executions END) exceptions
FROM sys.query_store_runtime_stats rs
JOIN sys.query_store_runtime_stats_interval i ON i.runtime_stats_interval_id = rs.runtime_stats_interval_id
WHERE i.start_time >= @day_from AND i.start_time < @day_to
GROUP BY CONVERT(char(13), SWITCHOFFSET(i.start_time, '+04:00'), 120)
ORDER BY 1;

-- 2. Top 15 queries by duration in A+B, compared with baseline C. plans>1 hints at a plan change/regression.
;WITH x AS (
  SELECT rs.plan_id, w.label, rs.count_executions n, rs.count_executions * rs.avg_duration d
  FROM sys.query_store_runtime_stats rs
  JOIN sys.query_store_runtime_stats_interval i ON i.runtime_stats_interval_id = rs.runtime_stats_interval_id
  JOIN #w w ON i.start_time >= w.f AND i.start_time < w.t
), y AS (
  SELECT p.query_id, x.plan_id, x.label, x.n, x.d,
         LEFT(REPLACE(REPLACE(qt.query_sql_text, CHAR(13), ' '), CHAR(10), ' '), 110) txt
  FROM x
  JOIN sys.query_store_plan p ON p.plan_id = x.plan_id
  JOIN sys.query_store_query q ON q.query_id = p.query_id
  JOIN sys.query_store_query_text qt ON qt.query_text_id = q.query_text_id
)
SELECT TOP 15 query_id, txt, COUNT(DISTINCT plan_id) plans,
       SUM(CASE WHEN label = 'A' THEN n END) A_n,
       CAST(SUM(CASE WHEN label = 'A' THEN d END) / NULLIF(SUM(CASE WHEN label = 'A' THEN n END), 0) / 1000 AS decimal(12,1)) A_avg_ms,
       SUM(CASE WHEN label = 'B' THEN n END) B_n,
       CAST(SUM(CASE WHEN label = 'B' THEN d END) / NULLIF(SUM(CASE WHEN label = 'B' THEN n END), 0) / 1000 AS decimal(12,1)) B_avg_ms,
       SUM(CASE WHEN label = 'C' THEN n END) C_n,
       CAST(SUM(CASE WHEN label = 'C' THEN d END) / NULLIF(SUM(CASE WHEN label = 'C' THEN n END), 0) / 1000 AS decimal(12,1)) C_avg_ms
FROM y
GROUP BY query_id, txt
ORDER BY SUM(CASE WHEN label IN ('A','B') THEN d END) DESC;

-- 3. Wait categories by window, seconds (Query Store wait stats, SQL 2017+).
IF OBJECT_ID('sys.query_store_wait_stats') IS NOT NULL
  EXEC('SELECT ws.wait_category_desc,
          CAST(SUM(CASE WHEN w.label = ''A'' THEN ws.total_query_wait_time_ms END) / 1000.0 AS decimal(14,1)) A_wait_s,
          CAST(SUM(CASE WHEN w.label = ''B'' THEN ws.total_query_wait_time_ms END) / 1000.0 AS decimal(14,1)) B_wait_s,
          CAST(SUM(CASE WHEN w.label = ''C'' THEN ws.total_query_wait_time_ms END) / 1000.0 AS decimal(14,1)) C_wait_s
        FROM sys.query_store_wait_stats ws
        JOIN sys.query_store_runtime_stats_interval i ON i.runtime_stats_interval_id = ws.runtime_stats_interval_id
        JOIN #w w ON i.start_time >= w.f AND i.start_time < w.t
        GROUP BY ws.wait_category_desc
        ORDER BY SUM(ws.total_query_wait_time_ms) DESC');
ELSE PRINT 'No Query Store wait stats on this version.';

-- 4. system_health events per Dubai hour (deadlocks, long waits, errors). Not available on Azure SQL Database.
BEGIN TRY
  DECLARE @ufrom datetime2 = CAST(SWITCHOFFSET(@day_from, '+00:00') AS datetime2),
          @uto   datetime2 = CAST(SWITCHOFFSET(@day_to,   '+00:00') AS datetime2);
  ;WITH e AS (
    SELECT object_name n, CAST(event_data AS xml).value('(event/@timestamp)[1]', 'datetime2') ts
    FROM sys.fn_xe_file_target_read_file('system_health*.xel', NULL, NULL, NULL)
    WHERE object_name IN ('xml_deadlock_report','wait_info','wait_info_external','error_reported','blocked_process_report')
  )
  SELECT n event_name, CONVERT(char(13), DATEADD(HOUR, 4, ts), 120) dubai_hr, COUNT(*) cnt
  FROM e WHERE ts >= @ufrom AND ts < @uto
  GROUP BY n, CONVERT(char(13), DATEADD(HOUR, 4, ts), 120)
  ORDER BY 2, 1;
END TRY
BEGIN CATCH
  PRINT 'system_health read failed: ' + ERROR_MESSAGE();
END CATCH;

-- 5. Error log, grouped (timestamps in SERVER local time).
DECLARE @off int = DATEDIFF(MINUTE, GETUTCDATE(), GETDATE()),
        @lf datetime = DATEADD(MINUTE, DATEDIFF(MINUTE, GETUTCDATE(), GETDATE()), CAST(SWITCHOFFSET(@day_from, '+00:00') AS datetime)),
        @lt datetime = DATEADD(MINUTE, DATEDIFF(MINUTE, GETUTCDATE(), GETDATE()), CAST(SWITCHOFFSET(@day_to,   '+00:00') AS datetime));
CREATE TABLE #log (d datetime, p nvarchar(100), t nvarchar(max));
BEGIN TRY INSERT #log EXEC master.dbo.xp_readerrorlog 0, 1, N'', N'', @lf, @lt, N'asc'; END TRY BEGIN CATCH PRINT 'errorlog 0 failed: ' + ERROR_MESSAGE(); END CATCH;
BEGIN TRY INSERT #log EXEC master.dbo.xp_readerrorlog 1, 1, N'', N'', @lf, @lt, N'asc'; END TRY BEGIN CATCH PRINT 'errorlog 1 failed: ' + ERROR_MESSAGE(); END CATCH;
SELECT TOP 25 LEFT(t, 100) msg, COUNT(*) n, MIN(d) first_local, MAX(d) last_local
FROM #log
WHERE t LIKE '%error%' OR t LIKE '%deadlock%' OR t LIKE '%longer than%' OR t LIKE '%timeout%' OR t LIKE '%failed%'
   OR t LIKE '%memory%' OR t LIKE '%stack dump%' OR t LIKE '%non-yielding%' OR t LIKE '%latch%'
GROUP BY LEFT(t, 100)
ORDER BY n DESC;

-- 6. SQL Agent jobs on the day that ran >= 60s or failed (backups, index/stats maintenance, ETL), Dubai start time.
BEGIN TRY
  SELECT TOP 40 j.name job,
         DATEADD(MINUTE, 240 - @off, msdb.dbo.agent_datetime(h.run_date, h.run_time)) dubai_start,
         (h.run_duration / 10000 * 3600 + h.run_duration / 100 % 100 * 60 + h.run_duration % 100) dur_s,
         h.run_status
  FROM msdb.dbo.sysjobhistory h
  JOIN msdb.dbo.sysjobs j ON j.job_id = h.job_id
  WHERE h.step_id = 0 AND h.run_date >= 20261021
    AND msdb.dbo.agent_datetime(h.run_date, h.run_time) >= @lf
    AND msdb.dbo.agent_datetime(h.run_date, h.run_time) <  @lt
    AND ((h.run_duration / 10000 * 3600 + h.run_duration / 100 % 100 * 60 + h.run_duration % 100) >= 60 OR h.run_status <> 1)
  ORDER BY dubai_start;
END TRY
BEGIN CATCH
  PRINT 'agent history failed: ' + ERROR_MESSAGE();
END CATCH;

DROP TABLE #w, #log;
