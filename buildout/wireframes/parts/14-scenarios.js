// Evidence scenarios for the wireframe (generated from specs/2026-10-29-evidence-fixture-*.json).
var SCEN = {
 "sql": [
  {
   "id": "sql-waits-benign",
   "label": "Top wait types (delta)",
   "verdict": "ok",
   "rule": "Green: benign background waits hold most of the delta and no bottleneck wait is both 30 % larger and 100 ms per task worse than the reference window.",
   "chart": "stackedArea",
   "series": [
    {
     "name": "SLEEP_TASK",
     "win": "normal",
     "pts": [
      {
       "t": 0,
       "v": 62
      },
      {
       "t": 30,
       "v": 64
      }
     ]
    },
    {
     "name": "SLEEP_TASK",
     "win": "incident",
     "pts": [
      {
       "t": 60,
       "v": 61
      },
      {
       "t": 90,
       "v": 63
      },
      {
       "t": 119,
       "v": 62
      }
     ]
    },
    {
     "name": "LAZYWRITER",
     "win": "normal",
     "pts": [
      {
       "t": 0,
       "v": 22
      },
      {
       "t": 30,
       "v": 21
      }
     ]
    },
    {
     "name": "LAZYWRITER",
     "win": "incident",
     "pts": [
      {
       "t": 60,
       "v": 23
      },
      {
       "t": 90,
       "v": 22
      },
      {
       "t": 119,
       "v": 22
      }
     ]
    },
    {
     "name": "PAGEIOLATCH_SH",
     "win": "normal",
     "pts": [
      {
       "t": 0,
       "v": 3
      },
      {
       "t": 30,
       "v": 3
      }
     ]
    },
    {
     "name": "PAGEIOLATCH_SH",
     "win": "incident",
     "pts": [
      {
       "t": 60,
       "v": 4
      },
      {
       "t": 90,
       "v": 3
      },
      {
       "t": 119,
       "v": 3
      }
     ]
    }
   ],
   "table": {
    "cols": [
     "Wait type",
     "Delta share",
     "Delta ms per task"
    ],
    "rows": [
     [
      "SLEEP_TASK",
      "62%",
      "1.1"
     ],
     [
      "LAZYWRITER",
      "22%",
      "0.4"
     ],
     [
      "PAGEIOLATCH_SH",
      "3%",
      "0.9"
     ],
     [
      "CHKPT",
      "2%",
      "0.3"
     ]
    ]
   },
   "why": {
    "summary": "Benign background waits dominate; no bottleneck wait grew in the incident window.",
    "steps": [
     "Delta wait_time_ms between the last reference snapshot and each incident snapshot.",
     "Compare each wait's delta share and per-task ms with the reference window.",
     "No wait is both 30 % larger and 100 ms per task worse, so the row is green."
    ],
    "dataNote": "Wait stats are cumulative and delta'd across snapshots taken every 60 s; spikes between samples can be missed."
   },
   "sampled": true
  },
  {
   "id": "sql-topqueries-orders",
   "label": "Top queries by duration",
   "verdict": "ok",
   "rule": "Green: every query is under the 400 ms hard SLA and within 30 % and 100 ms of its reference duration.",
   "chart": "bars",
   "series": [
    {
     "name": "Orders.Create p95 ms",
     "win": "normal",
     "pts": [
      {
       "t": 0,
       "v": 180
      },
      {
       "t": 15,
       "v": 176
      },
      {
       "t": 30,
       "v": 182
      }
     ]
    },
    {
     "name": "Orders.Create p95 ms",
     "win": "incident",
     "pts": [
      {
       "t": 60,
       "v": 190
      },
      {
       "t": 90,
       "v": 188
      },
      {
       "t": 119,
       "v": 185
      }
     ]
    }
   ],
   "table": {
    "cols": [
     "Query",
     "Calls",
     "Avg ms",
     "p95 ms"
    ],
    "rows": [
     [
      "Orders.Create insert",
      "1,204",
      "142",
      "190"
     ],
     [
      "OrderLines select",
      "3,318",
      "31",
      "58"
     ],
     [
      "Stock lookup",
      "2,907",
      "12",
      "26"
     ],
     [
      "Audit write",
      "1,204",
      "9",
      "21"
     ]
    ]
   },
   "why": {
    "summary": "The slowest query in the window, Orders.Create, stays under SLA and barely moves between windows.",
    "steps": [
     "Read total_elapsed_time and wait_time for in-flight requests each snapshot.",
     "Grouped by query text and took avg and p95 over the window.",
     "All values are below the hard SLA, so the row is green."
    ],
    "dataNote": "sys.dm_exec_requests is a point-in-time gauge; only requests alive at a sample appear."
   },
   "sampled": true
  },
  {
   "id": "sql-log-growth",
   "label": "Log growth",
   "verdict": "ok",
   "rule": "Green: log used stays under 70 % and no autogrowth event falls inside the window.",
   "chart": "line",
   "series": [
    {
     "name": "Log used %",
     "win": "normal",
     "pts": [
      {
       "t": 0,
       "v": 41
      },
      {
       "t": 20,
       "v": 41
      },
      {
       "t": 40,
       "v": 42
      }
     ]
    },
    {
     "name": "Log used %",
     "win": "incident",
     "pts": [
      {
       "t": 60,
       "v": 42
      },
      {
       "t": 80,
       "v": 43
      },
      {
       "t": 100,
       "v": 44
      }
     ]
    }
   ],
   "table": {
    "cols": [
     "Metric",
     "Reference",
     "Incident"
    ],
    "rows": [
     [
      "Log used %",
      "41-42",
      "42-44"
     ],
     [
      "VLF count",
      "312",
      "312"
     ],
     [
      "Autogrowth events",
      "0",
      "0"
     ]
    ]
   },
   "why": {
    "summary": "The log grew a little but stayed far under 70 % and never had to autogrow.",
    "steps": [
     "Read total_log_size and used_log_space each snapshot.",
     "Counted autogrow events in the default trace for both windows.",
     "Used % is below 70 % and no growth event fired, so the row is green."
    ],
    "dataNote": "sys.dm_db_log_space_usage is a gauge; autogrow events come from the default trace, which rolls over."
   },
   "sampled": false
  },
  {
   "id": "sql-lock-waits-rising",
   "label": "Lock waits by resource",
   "verdict": "watch",
   "rule": "Watch: one secondary wait is rising (LCK_M_X is at least 15 % and 50 ms per task worse than reference) but no sessions are queued behind it.",
   "chart": "stackedArea",
   "series": [
    {
     "name": "LCK_M_S",
     "win": "normal",
     "pts": [
      {
       "t": 0,
       "v": 41
      },
      {
       "t": 30,
       "v": 40
      }
     ]
    },
    {
     "name": "LCK_M_S",
     "win": "incident",
     "pts": [
      {
       "t": 60,
       "v": 52
      },
      {
       "t": 90,
       "v": 55
      },
      {
       "t": 119,
       "v": 54
      }
     ]
    },
    {
     "name": "LCK_M_X",
     "win": "normal",
     "pts": [
      {
       "t": 0,
       "v": 8
      },
      {
       "t": 30,
       "v": 9
      }
     ]
    },
    {
     "name": "LCK_M_X",
     "win": "incident",
     "pts": [
      {
       "t": 60,
       "v": 27
      },
      {
       "t": 90,
       "v": 31
      },
      {
       "t": 119,
       "v": 29
      }
     ]
    },
    {
     "name": "LCK_M_IX",
     "win": "normal",
     "pts": [
      {
       "t": 0,
       "v": 5
      },
      {
       "t": 30,
       "v": 5
      }
     ]
    },
    {
     "name": "LCK_M_IX",
     "win": "incident",
     "pts": [
      {
       "t": 60,
       "v": 9
      },
      {
       "t": 90,
       "v": 10
      },
      {
       "t": 119,
       "v": 9
      }
     ]
    }
   ],
   "table": {
    "cols": [
     "Lock wait",
     "Delta ms",
     "Resource"
    ],
    "rows": [
     [
      "LCK_M_X",
      "1,840",
      "Orders (key)"
     ],
     [
      "LCK_M_S",
      "1,510",
      "Orders (page)"
     ],
     [
      "LCK_M_IX",
      "320",
      "Orders (key)"
     ],
     [
      "LCK_M_U",
      "70",
      "OrderLines (key)"
     ]
    ]
   },
   "why": {
    "summary": "X and S lock waits on the Orders object roughly doubled in the incident window, but nothing is queueing yet.",
    "steps": [
     "Delta LCK_M_* from sys.dm_os_wait_stats between snapshots.",
     "Grouped the delta by resource_description.",
     "The rise passes the watch test (half of red) but not red, and no session is blocked, so the row is amber."
    ],
    "dataNote": "dm_os_wait_stats is a cumulative delta; the per-resource split needs a waiting_tasks sample."
   },
   "sampled": true
  },
  {
   "id": "sql-memory-grants",
   "label": "Memory grants",
   "verdict": "watch",
   "rule": "Watch: RESOURCE_SEMAPHORE waits appear occasionally (at least 15 % and 50 ms per task worse than reference) but no request sits in the grant queue at the end of the window.",
   "chart": "line",
   "series": [
    {
     "name": "RESOURCE_SEMAPHORE delta ms",
     "win": "normal",
     "pts": [
      {
       "t": 0,
       "v": 0
      },
      {
       "t": 20,
       "v": 0
      },
      {
       "t": 40,
       "v": 20
      }
     ]
    },
    {
     "name": "RESOURCE_SEMAPHORE delta ms",
     "win": "incident",
     "pts": [
      {
       "t": 60,
       "v": 210
      },
      {
       "t": 80,
       "v": 180
      },
      {
       "t": 100,
       "v": 90
      }
     ]
    },
    {
     "name": "Grant queue depth",
     "win": "normal",
     "pts": [
      {
       "t": 0,
       "v": 0
      },
      {
       "t": 20,
       "v": 0
      },
      {
       "t": 40,
       "v": 0
      }
     ]
    },
    {
     "name": "Grant queue depth",
     "win": "incident",
     "pts": [
      {
       "t": 60,
       "v": 2
      },
      {
       "t": 80,
       "v": 1
      },
      {
       "t": 100,
       "v": 0
      }
     ]
    }
   ],
   "table": {
    "cols": [
     "Metric",
     "Reference",
     "Incident"
    ],
    "rows": [
     [
      "RESOURCE_SEMAPHORE ms",
      "0",
      "480"
     ],
     [
      "Peak grant queue",
      "0",
      "2"
     ],
     [
      "Granted vs requested",
      "1.0x",
      "0.8x"
     ]
    ]
   },
   "why": {
    "summary": "A few sorts waited briefly for memory grants in the incident window, but the queue drained on its own.",
    "steps": [
     "Delta the RESOURCE_SEMAPHORE wait from dm_os_wait_stats.",
     "Sampled dm_exec_query_memory_grants for outstanding grants.",
     "Waits are occasional and the queue clears, so the row is amber, not red."
    ],
    "dataNote": "The wait delta is durable; the queue depth is a snapshot and can miss a short queue."
   },
   "sampled": true
  },
  {
   "id": "sql-index-maintenance",
   "label": "Index maintenance overlap",
   "verdict": "watch",
   "rule": "Watch: a rebuild or reorg job overlaps the window (at least 15 % and 50 ms per task worse on the affected file) but no bottleneck wait shares its timestamp.",
   "chart": "bars",
   "series": [
    {
     "name": "Maintenance job in window",
     "win": "normal",
     "pts": [
      {
       "t": 0,
       "v": 0
      },
      {
       "t": 30,
       "v": 0
      }
     ]
    },
    {
     "name": "Maintenance job in window",
     "win": "incident",
     "pts": [
      {
       "t": 60,
       "v": 42
      },
      {
       "t": 90,
       "v": 35
      },
      {
       "t": 119,
       "v": 0
      }
     ]
    }
   ],
   "table": {
    "cols": [
     "Job",
     "Window",
     "Overlap min",
     "Fragmentation after"
    ],
    "rows": [
     [
      "IX_Orders_Cust_rebuild",
      "09:00-10:00",
      "42",
      "2 %"
     ],
     [
      "IX_Lines_Order_reorg",
      "09:20-09:55",
      "35",
      "8 %"
     ]
    ]
   },
   "why": {
    "summary": "An index rebuild ran inside the incident window and added mild I/O, but no wait spike lines up with it.",
    "steps": [
     "Read msdb job history for maintenance jobs overlapping the window.",
     "Read fragmentation from dm_db_index_physical_stats.",
     "The overlap is real but not tied to a bottleneck wait, so the row is amber."
    ],
    "dataNote": "Job history is durable; fragmentation is a gauge from the last scan."
   },
   "sampled": false
  },
  {
   "id": "sql-waits-bottleneck",
   "label": "Top wait types (delta)",
   "verdict": "bad",
   "rule": "Bad: PAGEIOLATCH_SH and WRITELOG hold at least 30 % of the wait delta and are at least 100 ms per task worse than reference, and both scale with load.",
   "chart": "stackedArea",
   "series": [
    {
     "name": "PAGEIOLATCH_SH",
     "win": "normal",
     "pts": [
      {
       "t": 0,
       "v": 14
      },
      {
       "t": 30,
       "v": 15
      }
     ]
    },
    {
     "name": "PAGEIOLATCH_SH",
     "win": "incident",
     "pts": [
      {
       "t": 60,
       "v": 44
      },
      {
       "t": 90,
       "v": 51
      },
      {
       "t": 119,
       "v": 48
      }
     ]
    },
    {
     "name": "WRITELOG",
     "win": "normal",
     "pts": [
      {
       "t": 0,
       "v": 9
      },
      {
       "t": 30,
       "v": 10
      }
     ]
    },
    {
     "name": "WRITELOG",
     "win": "incident",
     "pts": [
      {
       "t": 60,
       "v": 27
      },
      {
       "t": 90,
       "v": 30
      },
      {
       "t": 119,
       "v": 28
      }
     ]
    },
    {
     "name": "SLEEP_TASK",
     "win": "normal",
     "pts": [
      {
       "t": 0,
       "v": 58
      },
      {
       "t": 30,
       "v": 57
      }
     ]
    },
    {
     "name": "SLEEP_TASK",
     "win": "incident",
     "pts": [
      {
       "t": 60,
       "v": 22
      },
      {
       "t": 90,
       "v": 14
      },
      {
       "t": 119,
       "v": 17
      }
     ]
    }
   ],
   "table": {
    "cols": [
     "Wait type",
     "Reference share",
     "Incident share",
     "Per-task delta"
    ],
    "rows": [
     [
      "PAGEIOLATCH_SH",
      "14%",
      "44%",
      "+124 ms"
     ],
     [
      "WRITELOG",
      "9%",
      "31%",
      "+104 ms"
     ],
     [
      "SLEEP_TASK",
      "58%",
      "16%",
      "-3 ms"
     ],
     [
      "CXPACKET",
      "4%",
      "9%",
      "+21 ms"
     ]
    ]
   },
   "why": {
    "summary": "Storage and log waits take over the incident window and replace the benign background waits.",
    "steps": [
     "Delta dm_os_wait_stats between the reference and incident snapshots.",
     "Compare the share of the delta and the ms per waiting task.",
     "Both bottleneck waits pass the red test (30 % and 100 ms) and grow with load, so the row is red."
    ],
    "dataNote": "Cumulative deltas are durable; the per-resource split needs a matching waiting_tasks sample."
   },
   "sampled": true
  },
  {
   "id": "sql-blocking-chain",
   "label": "Head blocker / blocking chains",
   "verdict": "bad",
   "rule": "Bad: a head blocker is itself waiting on LCK_M_* or PAGEIOLATCH_* and the chain is sustained at least 30 % of the window and 100 ms longer than reference.",
   "chart": "bars",
   "series": [
    {
     "name": "Blocked seconds by session",
     "win": "incident",
     "pts": [
      {
       "t": 60,
       "v": 38
      },
      {
       "t": 75,
       "v": 52
      },
      {
       "t": 90,
       "v": 61
      },
      {
       "t": 105,
       "v": 58
      },
      {
       "t": 119,
       "v": 55
      }
     ]
    }
   ],
   "table": {
    "cols": [
     "Session",
     "Blocked by",
     "Wait",
     "Blocked s"
    ],
    "rows": [
     [
      "spid 78",
      "spid 61",
      "LCK_M_X",
      "61"
     ],
     [
      "spid 84",
      "spid 61",
      "LCK_M_X",
      "54"
     ],
     [
      "spid 61 (head)",
      "-",
      "PAGEIOLATCH_SH",
      "48"
     ],
     [
      "spid 90",
      "spid 78",
      "LCK_M_S",
      "22"
     ]
    ]
   },
   "why": {
    "summary": "A chain forms with a head blocker that is itself waiting on I/O, and it lasts most of the window.",
    "steps": [
     "Sampled dm_exec_requests.blocking_session_id and dm_os_waiting_tasks.",
     "Walked the chain to its head and timed each hop.",
     "The head blocker is waiting, not running, and the chain is sustained, so the row is red."
    ],
    "dataNote": "Only visible if a snapshot landed during the window; blocking that started and ended between samples is missed."
   },
   "sampled": true
  },
  {
   "id": "sql-tempdb-contention",
   "label": "tempdb allocation contention",
   "verdict": "bad",
   "rule": "Bad: PAGELATCH_UP or PAGELATCH_EX on tempdb allocation pages (2:x:1/2/3) is sustained at least 30 % and 100 ms per task worse than reference.",
   "chart": "line",
   "series": [
    {
     "name": "PAGELATCH_UP delta ms",
     "win": "normal",
     "pts": [
      {
       "t": 0,
       "v": 0
      },
      {
       "t": 20,
       "v": 5
      },
      {
       "t": 40,
       "v": 12
      }
     ]
    },
    {
     "name": "PAGELATCH_UP delta ms",
     "win": "incident",
     "pts": [
      {
       "t": 60,
       "v": 420
      },
      {
       "t": 80,
       "v": 510
      },
      {
       "t": 100,
       "v": 470
      }
     ]
    }
   ],
   "table": {
    "cols": [
     "Wait resource",
     "Delta ms",
     "Pages"
    ],
    "rows": [
     [
      "2:1:1",
      "610",
      "PFS"
     ],
     [
      "2:3:2",
      "280",
      "GAM"
     ],
     [
      "2:4:3",
      "120",
      "SGAM"
     ]
    ]
   },
   "why": {
    "summary": "Waits pile up on tempdb allocation pages, the classic few-files signature.",
    "steps": [
     "Delta PAGELATCH_* from dm_os_wait_stats.",
     "Grouped by resource_description to find the tempdb file:page.",
     "Waits concentrate on 2:x:1/2/3 and stay high, so the row is red."
    ],
    "dataNote": "Durable as a delta; the file:page split needs a waiting_tasks sample in the window."
   },
   "sampled": true
  },
  {
   "id": "sql-plan-regression",
   "label": "Plan regressions (Query Store)",
   "verdict": "none",
   "rule": "Gray, not collected yet: Query Store is off, so no plan history was captured for this case. Absent evidence, not a pass.",
   "chart": "none",
   "series": [],
   "table": {
    "cols": [
     "Query",
     "plan_id",
     "Avg ms"
    ],
    "rows": []
   },
   "why": {
    "summary": "Cannot tell yet whether Orders.Create changed plan, because Query Store is not enabled on this database.",
    "steps": [
     "Check sys.database_query_store_options for the case database.",
     "Result: enabled = 0, so there is no runtime stats or plan history to read.",
     "The row is shown gray and labelled not collected yet; turning Query Store on and re-running would fill it."
    ],
    "dataNote": "Query Store persists across restarts; without it, plan changes leave no trace in the snapshots."
   },
   "sampled": false
  },
  {
   "id": "sql-deadlocks",
   "label": "Deadlocks (system_health XE)",
   "verdict": "none",
   "rule": "Gray, not collected yet: the system_health session is not writing to a persisted event file, so the deadlock report cannot be read. Absent evidence, not a pass.",
   "chart": "none",
   "series": [],
   "table": {
    "cols": [
     "Time",
     "Victim",
     "Resources"
    ],
    "rows": []
   },
   "why": {
    "summary": "Cannot tell yet whether deadlocks fired in the window, because system_health has no persisted event file to read.",
    "steps": [
     "Look for xml_deadlock_report entries in the system_health XE target.",
     "Result: no file target, so only the ring buffer exists and it holds recent events only.",
     "The row is shown gray and labelled not collected yet; enabling a file target would fill it."
    ],
    "dataNote": "system_health survives snapshots; without a file target, deadlock graphs are lost once they leave the ring buffer."
   },
   "sampled": false
  },
  {
   "id": "sql-cpu-high",
   "label": "CPU is high; here are the top waits and queries",
   "verdict": "bad",
   "rule": "Red: CPU is high in the incident window and one bounded wait type holds most of the wait delta while the top query holds the extra time.",
   "chart": "bars",
   "series": [
    {
     "name": "Top wait share (%)",
     "win": "normal",
     "pts": [ { "t": 0, "v": 14 }, { "t": 30, "v": 15 } ]
    },
    {
     "name": "Top wait share (%)",
     "win": "incident",
     "pts": [ { "t": 60, "v": 44 }, { "t": 90, "v": 51 }, { "t": 110, "v": 48 } ]
    }
   ],
   "table": {
    "cols": [ "Wait type", "Incident share", "Per-task delta" ],
    "rows": [
     [ "PAGEIOLATCH_SH", "44%", "+124 ms" ],
     [ "WRITELOG", "31%", "+104 ms" ],
     [ "SLEEP_TASK", "16%", "-3 ms" ]
    ]
   },
   "queries": [
    { "id": "1204", "calls": "1,204", "avg": "142", "p95": "190", "wait": "PAGEIOLATCH_SH" },
    { "id": "3318", "calls": "3,318", "avg": "31", "p95": "58", "wait": "SLEEP_TASK" },
    { "id": "2907", "calls": "2,907", "avg": "12", "p95": "26", "wait": "LCK_M_S" },
    { "id": "1205", "calls": "1,204", "avg": "9", "p95": "21", "wait": "WRITELOG" }
   ],
   "why": {
    "summary": "CPU is high; here are the top waits and queries. PAGEIOLATCH_SH holds 44% of the wait delta and Orders.Create insert is the top query.",
    "steps": [
     "Delta dm_os_wait_stats between the reference and incident windows.",
     "Rank the queries by total elapsed time in the incident window.",
     "CPU is high and one bounded wait type holds most of the delta, so the row is red."
    ],
    "dataNote": "Wait stats are sampled; per-query wait types need SQL Server 2017+ with Query Store wait stats."
   },
   "sampled": true
  }
 ],
 "mongo": [
  {
   "id": "MG-BAD-SLOWOPS",
   "label": "Slow ops by namespace (Orders.Create)",
   "verdict": "bad",
   "rule": "Red: incident p95 is at least 30% and at least 100 ms worse than reference. 191 ms vs 41 ms is +366% and +150 ms.",
   "chart": "line",
   "series": [
    {
     "name": "p95 op latency (ms)",
     "win": "normal",
     "pts": [
      {
       "t": 0,
       "v": 40
      },
      {
       "t": 5,
       "v": 42
      },
      {
       "t": 10,
       "v": 39
      },
      {
       "t": 15,
       "v": 41
      },
      {
       "t": 20,
       "v": 43
      },
      {
       "t": 25,
       "v": 40
      },
      {
       "t": 30,
       "v": 38
      },
      {
       "t": 35,
       "v": 42
      },
      {
       "t": 40,
       "v": 44
      },
      {
       "t": 45,
       "v": 41
      },
      {
       "t": 50,
       "v": 40
      },
      {
       "t": 55,
       "v": 42
      }
     ]
    },
    {
     "name": "p95 op latency (ms)",
     "win": "incident",
     "pts": [
      {
       "t": 60,
       "v": 120
      },
      {
       "t": 65,
       "v": 150
      },
      {
       "t": 70,
       "v": 175
      },
      {
       "t": 75,
       "v": 190
      },
      {
       "t": 80,
       "v": 182
      },
      {
       "t": 85,
       "v": 205
      },
      {
       "t": 90,
       "v": 195
      },
      {
       "t": 95,
       "v": 210
      },
      {
       "t": 100,
       "v": 188
      },
      {
       "t": 105,
       "v": 200
      },
      {
       "t": 110,
       "v": 192
      },
      {
       "t": 115,
       "v": 185
      }
     ]
    }
   ],
   "table": {
    "cols": [
     "Window",
     "p95 ms",
     "slow ops/min"
    ],
    "rows": [
     [
      "Reference 08:00-09:00",
      "41",
      "3"
     ],
     [
      "Incident 09:00-10:00",
      "191",
      "48"
     ]
    ]
   },
   "why": {
    "summary": "Orders.Create operations stayed well under slowms in the reference hour, then ran long and queued during the incident hour.",
    "steps": [
     "Profiler/slow-log groups ops past slowms by namespace; Orders.Create keeps a steady backlog.",
     "p95 rises through the hour and is still climbing at 10:00, so this is a sustained queue, not one spike.",
     "Same namespace and same API as the report, so this slow-op row is the primary confirmation."
    ],
    "dataNote": "Slow ops come from periodic profiler samples, so counts are a sample of activity, not a full capture."
   },
   "sampled": true
  },
  {
   "id": "MG-BAD-TICKETS",
   "label": "WiredTiger read/write tickets",
   "verdict": "bad",
   "rule": "Red: tickets out is at least 30% and at least 25 tickets worse than reference. 126 vs 25 of 128 is +404% and +101 tickets.",
   "chart": "line",
   "series": [
    {
     "name": "tickets out (of 128)",
     "win": "normal",
     "pts": [
      {
       "t": 0,
       "v": 24
      },
      {
       "t": 5,
       "v": 26
      },
      {
       "t": 10,
       "v": 25
      },
      {
       "t": 15,
       "v": 23
      },
      {
       "t": 20,
       "v": 27
      },
      {
       "t": 25,
       "v": 25
      },
      {
       "t": 30,
       "v": 24
      },
      {
       "t": 35,
       "v": 26
      },
      {
       "t": 40,
       "v": 28
      },
      {
       "t": 45,
       "v": 25
      },
      {
       "t": 50,
       "v": 24
      },
      {
       "t": 55,
       "v": 26
      }
     ]
    },
    {
     "name": "tickets out (of 128)",
     "win": "incident",
     "pts": [
      {
       "t": 60,
       "v": 110
      },
      {
       "t": 65,
       "v": 118
      },
      {
       "t": 70,
       "v": 124
      },
      {
       "t": 75,
       "v": 126
      },
      {
       "t": 80,
       "v": 127
      },
      {
       "t": 85,
       "v": 125
      },
      {
       "t": 90,
       "v": 128
      },
      {
       "t": 95,
       "v": 126
      },
      {
       "t": 100,
       "v": 124
      },
      {
       "t": 105,
       "v": 127
      },
      {
       "t": 110,
       "v": 125
      },
      {
       "t": 115,
       "v": 123
      }
     ]
    }
   ],
   "table": {
    "cols": [
     "Window",
     "tickets out",
     "available",
     "total"
    ],
    "rows": [
     [
      "Reference 08:00-09:00",
      "25",
      "103",
      "128"
     ],
     [
      "Incident 09:00-10:00",
      "126",
      "2",
      "128"
     ]
    ]
   },
   "why": {
    "summary": "WiredTiger's concurrent transaction tickets were almost all in use, so operations queued for a free ticket instead of running.",
    "steps": [
     "serverStatus.wiredTiger.concurrentTransactions shows 126 of 128 tickets out during the incident.",
     "With only 2 tickets left, new reads and writes wait, which shows up as rising slow ops.",
     "Ticket pressure is near the ceiling for the whole hour, so this is exhaustion, not a transient peak."
    ],
    "dataNote": "Ticket counts are point-in-time snapshots sampled every minute, so short sub-minute peaks can be missed."
   },
   "sampled": true
  },
  {
   "id": "MG-BAD-REPLAG",
   "label": "Replication lag",
   "verdict": "bad",
   "rule": "Red: lag is at least 30% and at least 5 s worse than reference (5 s replaces the 100 ms unit). 22 s vs 1.3 s is +1592% and +20.7 s.",
   "chart": "line",
   "series": [
    {
     "name": "replica lag (s)",
     "win": "normal",
     "pts": [
      {
       "t": 0,
       "v": 1
      },
      {
       "t": 5,
       "v": 1
      },
      {
       "t": 10,
       "v": 2
      },
      {
       "t": 15,
       "v": 1
      },
      {
       "t": 20,
       "v": 1
      },
      {
       "t": 25,
       "v": 2
      },
      {
       "t": 30,
       "v": 1
      },
      {
       "t": 35,
       "v": 1
      },
      {
       "t": 40,
       "v": 2
      },
      {
       "t": 45,
       "v": 1
      },
      {
       "t": 50,
       "v": 1
      },
      {
       "t": 55,
       "v": 1
      }
     ]
    },
    {
     "name": "replica lag (s)",
     "win": "incident",
     "pts": [
      {
       "t": 60,
       "v": 8
      },
      {
       "t": 65,
       "v": 12
      },
      {
       "t": 70,
       "v": 18
      },
      {
       "t": 75,
       "v": 22
      },
      {
       "t": 80,
       "v": 25
      },
      {
       "t": 85,
       "v": 24
      },
      {
       "t": 90,
       "v": 26
      },
      {
       "t": 95,
       "v": 23
      },
      {
       "t": 100,
       "v": 22
      },
      {
       "t": 105,
       "v": 24
      },
      {
       "t": 110,
       "v": 21
      },
      {
       "t": 115,
       "v": 20
      }
     ]
    }
   ],
   "table": {
    "cols": [
     "Window",
     "member",
     "lag s",
     "oplog window h"
    ],
    "rows": [
     [
      "Reference 08:00-09:00",
      "rs0/secondary",
      "1.3",
      "48"
     ],
     [
      "Incident 09:00-10:00",
      "rs0/secondary",
      "22",
      "6"
     ]
    ]
   },
   "why": {
    "summary": "The secondary fell tens of seconds behind the primary and the oplog window shrank, so reads served from it were stale.",
    "steps": [
     "replSetGetStatus shows per-member lag growing from about 1 s to over 20 s across the hour.",
     "The oplog window dropped from about 48 h to 6 h, so the replica was applying slower than the primary wrote.",
     "Lag was still high at 10:00, which matches a sustained write backlog, not a brief blip."
    ],
    "dataNote": "Lag is sampled from replSetGetStatus every minute; the true peak between samples can be higher."
   },
   "sampled": true
  },
  {
   "id": "MG-WATCH-CACHE",
   "label": "WiredTiger cache pressure",
   "verdict": "watch",
   "rule": "Amber: cache used is at least 15% and at least 10 percentage points worse than reference (10 pp replaces the 100 ms unit). 74% vs 61.5% is +20% and +12.5 pp.",
   "chart": "line",
   "series": [
    {
     "name": "cache used (% of max)",
     "win": "normal",
     "pts": [
      {
       "t": 0,
       "v": 60
      },
      {
       "t": 5,
       "v": 61
      },
      {
       "t": 10,
       "v": 62
      },
      {
       "t": 15,
       "v": 60
      },
      {
       "t": 20,
       "v": 63
      },
      {
       "t": 25,
       "v": 62
      },
      {
       "t": 30,
       "v": 61
      },
      {
       "t": 35,
       "v": 62
      },
      {
       "t": 40,
       "v": 64
      },
      {
       "t": 45,
       "v": 61
      },
      {
       "t": 50,
       "v": 60
      },
      {
       "t": 55,
       "v": 62
      }
     ]
    },
    {
     "name": "cache used (% of max)",
     "win": "incident",
     "pts": [
      {
       "t": 60,
       "v": 70
      },
      {
       "t": 65,
       "v": 72
      },
      {
       "t": 70,
       "v": 74
      },
      {
       "t": 75,
       "v": 73
      },
      {
       "t": 80,
       "v": 76
      },
      {
       "t": 85,
       "v": 75
      },
      {
       "t": 90,
       "v": 74
      },
      {
       "t": 95,
       "v": 73
      },
      {
       "t": 100,
       "v": 75
      },
      {
       "t": 105,
       "v": 74
      },
      {
       "t": 110,
       "v": 76
      },
      {
       "t": 115,
       "v": 75
      }
     ]
    }
   ],
   "table": {
    "cols": [
     "Window",
     "cache used %",
     "evictions/min",
     "dirty %"
    ],
    "rows": [
     [
      "Reference 08:00-09:00",
      "61.5",
      "900",
      "4.1"
     ],
     [
      "Incident 09:00-10:00",
      "74",
      "2600",
      "4.8"
     ]
    ]
   },
   "why": {
    "summary": "The cache moved well into the pressure zone and eviction more than doubled, but it did not reach the max and dirty pages stayed low.",
    "steps": [
     "serverStatus.wiredTiger.cache shows used bytes rising from about 61% to 74% of max.",
     "Eviction rose from about 900 to 2600 pages per minute, so some reads missed cache and went to disk.",
     "Cache stayed under max and did not force application threads to evict, which is why this is amber, not red."
    ],
    "dataNote": "Cache gauges are sampled each minute; the eviction rate is a delta between samples."
   },
   "sampled": true
  },
  {
   "id": "MG-WATCH-CONNS",
   "label": "Connection count",
   "verdict": "watch",
   "rule": "Amber: current connections is at least 15% and at least 20 connections worse than reference (20 replaces the 100 ms unit). 149 vs 120 is +24% and +29 connections.",
   "chart": "line",
   "series": [
    {
     "name": "current connections",
     "win": "normal",
     "pts": [
      {
       "t": 0,
       "v": 118
      },
      {
       "t": 5,
       "v": 120
      },
      {
       "t": 10,
       "v": 122
      },
      {
       "t": 15,
       "v": 119
      },
      {
       "t": 20,
       "v": 121
      },
      {
       "t": 25,
       "v": 120
      },
      {
       "t": 30,
       "v": 123
      },
      {
       "t": 35,
       "v": 120
      },
      {
       "t": 40,
       "v": 119
      },
      {
       "t": 45,
       "v": 122
      },
      {
       "t": 50,
       "v": 121
      },
      {
       "t": 55,
       "v": 120
      }
     ]
    },
    {
     "name": "current connections",
     "win": "incident",
     "pts": [
      {
       "t": 60,
       "v": 145
      },
      {
       "t": 65,
       "v": 150
      },
      {
       "t": 70,
       "v": 148
      },
      {
       "t": 75,
       "v": 152
      },
      {
       "t": 80,
       "v": 149
      },
      {
       "t": 85,
       "v": 146
      },
      {
       "t": 90,
       "v": 151
      },
      {
       "t": 95,
       "v": 150
      },
      {
       "t": 100,
       "v": 148
      },
      {
       "t": 105,
       "v": 147
      },
      {
       "t": 110,
       "v": 150
      },
      {
       "t": 115,
       "v": 149
      }
     ]
    }
   ],
   "table": {
    "cols": [
     "Window",
     "current",
     "available",
     "totalCreated"
    ],
    "rows": [
     [
      "Reference 08:00-09:00",
      "120",
      "580",
      "3120"
     ],
     [
      "Incident 09:00-10:00",
      "149",
      "551",
      "3890"
     ]
    ]
   },
   "why": {
    "summary": "More clients connected during the incident and totalCreated rose, but the pool still had plenty of available connections.",
    "steps": [
     "serverStatus.connections shows current rising from about 120 to 149 of a 700 limit.",
     "available stayed above 550, so the pool never ran out; connecting clients just took slots.",
     "There is real headroom left, so this is a warning, not exhaustion."
    ],
    "dataNote": "Connection counts are sampled each minute; churn between samples is not visible."
   },
   "sampled": true
  },
  {
   "id": "MG-WATCH-GLOCK",
   "label": "Global lock write queue",
   "verdict": "watch",
   "rule": "Amber: queued writers is at least 15% and at least 2 ops worse than reference (2 replaces the 100 ms unit). 3.7 vs 0.4 is +825% and +3.3, above watch, below the 5-op red line.",
   "chart": "stackedArea",
   "series": [
    {
     "name": "readers queued",
     "win": "normal",
     "pts": [
      {
       "t": 0,
       "v": 0
      },
      {
       "t": 5,
       "v": 0
      },
      {
       "t": 10,
       "v": 1
      },
      {
       "t": 15,
       "v": 0
      },
      {
       "t": 20,
       "v": 0
      },
      {
       "t": 25,
       "v": 1
      },
      {
       "t": 30,
       "v": 0
      },
      {
       "t": 35,
       "v": 0
      },
      {
       "t": 40,
       "v": 1
      },
      {
       "t": 45,
       "v": 0
      },
      {
       "t": 50,
       "v": 0
      },
      {
       "t": 55,
       "v": 0
      }
     ]
    },
    {
     "name": "readers queued",
     "win": "incident",
     "pts": [
      {
       "t": 60,
       "v": 1
      },
      {
       "t": 65,
       "v": 2
      },
      {
       "t": 70,
       "v": 1
      },
      {
       "t": 75,
       "v": 2
      },
      {
       "t": 80,
       "v": 1
      },
      {
       "t": 85,
       "v": 2
      },
      {
       "t": 90,
       "v": 1
      },
      {
       "t": 95,
       "v": 2
      },
      {
       "t": 100,
       "v": 1
      },
      {
       "t": 105,
       "v": 2
      },
      {
       "t": 110,
       "v": 1
      },
      {
       "t": 115,
       "v": 2
      }
     ]
    },
    {
     "name": "writers queued",
     "win": "normal",
     "pts": [
      {
       "t": 0,
       "v": 0
      },
      {
       "t": 5,
       "v": 1
      },
      {
       "t": 10,
       "v": 0
      },
      {
       "t": 15,
       "v": 0
      },
      {
       "t": 20,
       "v": 1
      },
      {
       "t": 25,
       "v": 0
      },
      {
       "t": 30,
       "v": 1
      },
      {
       "t": 35,
       "v": 0
      },
      {
       "t": 40,
       "v": 0
      },
      {
       "t": 45,
       "v": 1
      },
      {
       "t": 50,
       "v": 0
      },
      {
       "t": 55,
       "v": 1
      }
     ]
    },
    {
     "name": "writers queued",
     "win": "incident",
     "pts": [
      {
       "t": 60,
       "v": 2
      },
      {
       "t": 65,
       "v": 3
      },
      {
       "t": 70,
       "v": 4
      },
      {
       "t": 75,
       "v": 3
      },
      {
       "t": 80,
       "v": 5
      },
      {
       "t": 85,
       "v": 4
      },
      {
       "t": 90,
       "v": 3
      },
      {
       "t": 95,
       "v": 4
      },
      {
       "t": 100,
       "v": 5
      },
      {
       "t": 105,
       "v": 3
      },
      {
       "t": 110,
       "v": 4
      },
      {
       "t": 115,
       "v": 4
      }
     ]
    }
   ],
   "table": {
    "cols": [
     "Window",
     "writers queued (avg)",
     "readers queued (avg)"
    ],
    "rows": [
     [
      "Reference 08:00-09:00",
      "0.4",
      "0.3"
     ],
     [
      "Incident 09:00-10:00",
      "3.7",
      "1.5"
     ]
    ]
   },
   "why": {
    "summary": "A small but steady queue of writers built up behind the global lock, without reaching the level of a hard stall.",
    "steps": [
     "serverStatus.globalLock.currentQueue shows writers queued rising from near zero to around four.",
     "Readers queued moved a little, so the pressure is mostly on writes.",
     "The queue stays modest and no writer waits long enough to be red, so this is a watch."
    ],
    "dataNote": "Queue depth is sampled each minute; the true peak between samples can be higher."
   },
   "sampled": true
  },
  {
   "id": "MG-OK-SLOWOPS",
   "label": "Slow ops by namespace (Orders.Create)",
   "verdict": "ok",
   "rule": "Green: incident p95 is neither 30%+ nor 100 ms worse than reference. 44 ms vs 41 ms is +7% and +3 ms.",
   "chart": "line",
   "series": [
    {
     "name": "p95 op latency (ms)",
     "win": "normal",
     "pts": [
      {
       "t": 0,
       "v": 40
      },
      {
       "t": 5,
       "v": 42
      },
      {
       "t": 10,
       "v": 39
      },
      {
       "t": 15,
       "v": 41
      },
      {
       "t": 20,
       "v": 43
      },
      {
       "t": 25,
       "v": 40
      },
      {
       "t": 30,
       "v": 38
      },
      {
       "t": 35,
       "v": 42
      },
      {
       "t": 40,
       "v": 44
      },
      {
       "t": 45,
       "v": 41
      },
      {
       "t": 50,
       "v": 40
      },
      {
       "t": 55,
       "v": 42
      }
     ]
    },
    {
     "name": "p95 op latency (ms)",
     "win": "incident",
     "pts": [
      {
       "t": 60,
       "v": 43
      },
      {
       "t": 65,
       "v": 44
      },
      {
       "t": 70,
       "v": 42
      },
      {
       "t": 75,
       "v": 45
      },
      {
       "t": 80,
       "v": 44
      },
      {
       "t": 85,
       "v": 43
      },
      {
       "t": 90,
       "v": 46
      },
      {
       "t": 95,
       "v": 44
      },
      {
       "t": 100,
       "v": 43
      },
      {
       "t": 105,
       "v": 45
      },
      {
       "t": 110,
       "v": 44
      },
      {
       "t": 115,
       "v": 43
      }
     ]
    }
   ],
   "table": {
    "cols": [
     "Window",
     "p95 ms",
     "slow ops/min"
    ],
    "rows": [
     [
      "Reference 08:00-09:00",
      "41",
      "3"
     ],
     [
      "Incident 09:00-10:00",
      "44",
      "4"
     ]
    ]
   },
   "why": {
    "summary": "Orders.Create operation latency was flat across both hours, so slow ops do not explain the reported slowdown.",
    "steps": [
     "p95 moved from 41 ms to 44 ms, within normal minute-to-minute noise.",
     "Slow ops per minute stayed in single digits.",
     "This row rules the namespace out rather than pointing at it."
    ],
    "dataNote": "Slow ops come from periodic profiler samples, so counts are a sample of activity, not a full capture."
   },
   "sampled": true
  },
  {
   "id": "MG-OK-TICKETS",
   "label": "WiredTiger read/write tickets",
   "verdict": "ok",
   "rule": "Green: tickets out is neither 30%+ nor 25 tickets worse than reference. 26.5 vs 25 of 128 is +6% and +1.5 tickets.",
   "chart": "line",
   "series": [
    {
     "name": "tickets out (of 128)",
     "win": "normal",
     "pts": [
      {
       "t": 0,
       "v": 24
      },
      {
       "t": 5,
       "v": 26
      },
      {
       "t": 10,
       "v": 25
      },
      {
       "t": 15,
       "v": 23
      },
      {
       "t": 20,
       "v": 27
      },
      {
       "t": 25,
       "v": 25
      },
      {
       "t": 30,
       "v": 24
      },
      {
       "t": 35,
       "v": 26
      },
      {
       "t": 40,
       "v": 28
      },
      {
       "t": 45,
       "v": 25
      },
      {
       "t": 50,
       "v": 24
      },
      {
       "t": 55,
       "v": 26
      }
     ]
    },
    {
     "name": "tickets out (of 128)",
     "win": "incident",
     "pts": [
      {
       "t": 60,
       "v": 26
      },
      {
       "t": 65,
       "v": 25
      },
      {
       "t": 70,
       "v": 27
      },
      {
       "t": 75,
       "v": 26
      },
      {
       "t": 80,
       "v": 28
      },
      {
       "t": 85,
       "v": 27
      },
      {
       "t": 90,
       "v": 26
      },
      {
       "t": 95,
       "v": 25
      },
      {
       "t": 100,
       "v": 27
      },
      {
       "t": 105,
       "v": 26
      },
      {
       "t": 110,
       "v": 28
      },
      {
       "t": 115,
       "v": 27
      }
     ]
    }
   ],
   "table": {
    "cols": [
     "Window",
     "tickets out",
     "available",
     "total"
    ],
    "rows": [
     [
      "Reference 08:00-09:00",
      "25",
      "103",
      "128"
     ],
     [
      "Incident 09:00-10:00",
      "26.5",
      "101.5",
      "128"
     ]
    ]
   },
   "why": {
    "summary": "Plenty of WiredTiger tickets stayed free, so operations never queued for one.",
    "steps": [
     "Tickets out stayed around 26 of 128 for both hours.",
     "More than 100 tickets were available at all times.",
     "Ticket contention is not part of the slowdown."
    ],
    "dataNote": "Ticket counts are point-in-time snapshots sampled every minute."
   },
   "sampled": true
  },
  {
   "id": "MG-OK-REPLAG",
   "label": "Replication lag",
   "verdict": "ok",
   "rule": "Green: lag is neither 30%+ nor 5 s worse than reference. 1.3 s vs 1.2 s is +8% and +0.1 s.",
   "chart": "line",
   "series": [
    {
     "name": "replica lag (s)",
     "win": "normal",
     "pts": [
      {
       "t": 0,
       "v": 1
      },
      {
       "t": 5,
       "v": 1
      },
      {
       "t": 10,
       "v": 2
      },
      {
       "t": 15,
       "v": 1
      },
      {
       "t": 20,
       "v": 1
      },
      {
       "t": 25,
       "v": 2
      },
      {
       "t": 30,
       "v": 1
      },
      {
       "t": 35,
       "v": 1
      },
      {
       "t": 40,
       "v": 2
      },
      {
       "t": 45,
       "v": 1
      },
      {
       "t": 50,
       "v": 1
      },
      {
       "t": 55,
       "v": 1
      }
     ]
    },
    {
     "name": "replica lag (s)",
     "win": "incident",
     "pts": [
      {
       "t": 60,
       "v": 1
      },
      {
       "t": 65,
       "v": 2
      },
      {
       "t": 70,
       "v": 1
      },
      {
       "t": 75,
       "v": 1
      },
      {
       "t": 80,
       "v": 2
      },
      {
       "t": 85,
       "v": 1
      },
      {
       "t": 90,
       "v": 1
      },
      {
       "t": 95,
       "v": 2
      },
      {
       "t": 100,
       "v": 1
      },
      {
       "t": 105,
       "v": 1
      },
      {
       "t": 110,
       "v": 2
      },
      {
       "t": 115,
       "v": 1
      }
     ]
    }
   ],
   "table": {
    "cols": [
     "Window",
     "member",
     "lag s",
     "oplog window h"
    ],
    "rows": [
     [
      "Reference 08:00-09:00",
      "rs0/secondary",
      "1.2",
      "48"
     ],
     [
      "Incident 09:00-10:00",
      "rs0/secondary",
      "1.3",
      "47"
     ]
    ]
   },
   "why": {
    "summary": "The replica kept up with the primary through the incident hour and the oplog window stayed wide.",
    "steps": [
     "replSetGetStatus lag stayed at about one second, bouncing between 1 and 2.",
     "The oplog window was roughly 48 hours in both windows.",
     "Replication is not the cause of the reported slowdown."
    ],
    "dataNote": "Lag is sampled from replSetGetStatus every minute."
   },
   "sampled": true
  },
  {
   "id": "MG-NONE-COLLSCAN",
   "label": "Collection scans (COLLSCAN)",
   "verdict": "none",
   "rule": "Gray: not collected. Needs the database profiler turned on (profiling level 0 is the default), which this deployment does not run.",
   "chart": "none",
   "series": [],
   "table": {
    "cols": [
     "Window",
     "COLLSCAN/min"
    ],
    "rows": []
   },
   "why": {
    "summary": "No collection-scan numbers are shown because the profiler was off for the whole window.",
    "steps": [
     "COLLSCAN counts come from system.profile, which only fills when profiling is enabled.",
     "Profiling level here is 0 (off), so nothing was written for 08:00 to 10:00.",
     "This is absent data, not evidence that no scans happened."
    ],
    "dataNote": "To collect this, enable the profiler (or sample with a slowms threshold) before the next occurrence."
   },
   "sampled": false
  },
  {
   "id": "MG-NONE-INDEX",
   "label": "Index usage (IXSCAN vs COLLSCAN)",
   "verdict": "none",
   "rule": "Gray: not collected. Needs profiler output or the $indexStats privilege, neither of which is granted to this read-only collector.",
   "chart": "none",
   "series": [],
   "table": {
    "cols": [
     "Window",
     "index hit ratio"
    ],
    "rows": []
   },
   "why": {
    "summary": "Index-usage numbers are missing because the collector cannot read profiler output or $indexStats on this deployment.",
    "steps": [
     "Index activity is derived from system.profile or the $indexStats aggregation.",
     "The read-only role does not hold that privilege and the profiler is off.",
     "So the window was never sampled; the row is blank, not green."
    ],
    "dataNote": "Grant $indexStats (or enable sampling) and this row can be filled next time."
   },
   "sampled": false
  },
  {
   "id": "MG-NONE-OPLOG",
   "label": "Oplog window",
   "verdict": "none",
   "rule": "Gray: not collected. Needs the clusterMonitor role (or local-database read) to run rs.printReplicationInfo, which is not granted here.",
   "chart": "none",
   "series": [],
   "table": {
    "cols": [
     "Window",
     "oplog window h"
    ],
    "rows": []
   },
   "why": {
    "summary": "The oplog window is not shown because the collector has no permission to read it on this deployment.",
    "steps": [
     "rs.printReplicationInfo requires clusterMonitor or access to the local database.",
     "The read-only account here has neither, so the command was never run.",
     "Missing permission means no sample, so the state is gray rather than green."
    ],
    "dataNote": "Replication *lag* may still appear on its own row if replSetGetStatus is permitted; only the oplog-window row is blind here."
   },
   "sampled": false
  }
 ],
 "host": [
  {
   "id": "GS-01-host-cpu-ok",
   "label": "CPU busy time is normal",
   "verdict": "ok",
   "rule": "Green when CPU stays under 70% on both sides of the window; amber at 70-85% or clearly rising; red at saturation (90%+ for 5 minutes) or iowait/steal dominating. Unit: percent of host CPU.",
   "chart": "stackedArea",
   "series": [
    {
     "name": "CPU used (user+sys)",
     "win": "normal",
     "pts": [
      {
       "t": 0,
       "v": 32
      },
      {
       "t": 10,
       "v": 30
      },
      {
       "t": 20,
       "v": 34
      },
      {
       "t": 30,
       "v": 33
      },
      {
       "t": 40,
       "v": 31
      },
      {
       "t": 50,
       "v": 33
      }
     ]
    },
    {
     "name": "CPU used (user+sys)",
     "win": "incident",
     "pts": [
      {
       "t": 60,
       "v": 38
      },
      {
       "t": 70,
       "v": 40
      },
      {
       "t": 80,
       "v": 39
      },
      {
       "t": 90,
       "v": 41
      },
      {
       "t": 100,
       "v": 37
      },
      {
       "t": 110,
       "v": 39
      }
     ]
    }
   ],
   "table": {
    "cols": [
     "Window",
     "CPU used",
     "Vs reference"
    ],
    "rows": [
     [
      "08:00-08:59",
      "32%",
      "reference"
     ],
     [
      "09:00-09:59",
      "39%",
      "+7 points"
     ]
    ]
   },
   "why": {
    "summary": "CPU rose a little but stayed far below the amber line, so it is ruled out.",
    "steps": [
     "Incident CPU averages 39% against 32% in the reference hour.",
     "The amber line is 70% CPU, or half of the red gap.",
     "Neither line is crossed, so this row is green."
    ],
    "dataNote": "Sampled every 30s from Windows performance counters (Processor _Total % Processor Time)."
   },
   "sampled": true
  },
  {
   "id": "GS-02-host-disk-ok",
   "label": "Disk is fast with no queue",
   "verdict": "ok",
   "rule": "Green when disk latency stays under 10 ms on SSDs and the queue is near zero; amber when latency or queue clearly rises; red for a sustained queue with high latency. Unit: milliseconds per transfer, queue depth in requests.",
   "chart": "line",
   "series": [
    {
     "name": "Avg disk sec/transfer",
     "win": "normal",
     "pts": [
      {
       "t": 0,
       "v": 4
      },
      {
       "t": 10,
       "v": 4
      },
      {
       "t": 20,
       "v": 3
      },
      {
       "t": 30,
       "v": 5
      },
      {
       "t": 40,
       "v": 4
      },
      {
       "t": 50,
       "v": 4
      }
     ]
    },
    {
     "name": "Avg disk sec/transfer",
     "win": "incident",
     "pts": [
      {
       "t": 60,
       "v": 5
      },
      {
       "t": 70,
       "v": 5
      },
      {
       "t": 80,
       "v": 6
      },
      {
       "t": 90,
       "v": 5
      },
      {
       "t": 100,
       "v": 6
      },
      {
       "t": 110,
       "v": 7
      }
     ]
    }
   ],
   "table": {
    "cols": [
     "Window",
     "Sec/transfer",
     "Queue"
    ],
    "rows": [
     [
      "08:00-08:59",
      "4 ms",
      "0.2"
     ],
     [
      "09:00-09:59",
      "5.6 ms",
      "0.4"
     ]
    ]
   },
   "why": {
    "summary": "Latency stayed well under 10 ms and no queue built up.",
    "steps": [
     "Incident latency averages 5.6 ms against 4 ms, both under the 10 ms green line.",
     "Queue depth stayed near zero.",
     "Disk I/O is not the bottleneck."
    ],
    "dataNote": "Sampled every 30s from performance counters (PhysicalDisk Avg. Disk sec/Transfer and Current Disk Queue Length)."
   },
   "sampled": true
  },
  {
   "id": "GS-03-host-mem-ok",
   "label": "Memory has headroom",
   "verdict": "ok",
   "rule": "Green when available memory keeps headroom and nothing is swapped; amber when headroom is low; red when the host thrashes or an OOM kill happens. Unit: bytes free, pages swapped per second.",
   "chart": "line",
   "series": [
    {
     "name": "Available memory",
     "win": "normal",
     "pts": [
      {
       "t": 0,
       "v": 12500
      },
      {
       "t": 10,
       "v": 12300
      },
      {
       "t": 20,
       "v": 12600
      },
      {
       "t": 30,
       "v": 12200
      },
      {
       "t": 40,
       "v": 12400
      },
      {
       "t": 50,
       "v": 12300
      }
     ]
    },
    {
     "name": "Available memory",
     "win": "incident",
     "pts": [
      {
       "t": 60,
       "v": 11800
      },
      {
       "t": 70,
       "v": 11600
      },
      {
       "t": 80,
       "v": 11900
      },
      {
       "t": 90,
       "v": 11500
      },
      {
       "t": 100,
       "v": 11700
      },
      {
       "t": 110,
       "v": 11600
      }
     ]
    }
   ],
   "table": {
    "cols": [
     "Window",
     "Available",
     "Swap in/out"
    ],
    "rows": [
     [
      "08:00-08:59",
      "12.3 GB",
      "0"
     ],
     [
      "09:00-09:59",
      "11.7 GB",
      "0"
     ]
    ]
   },
   "why": {
    "summary": "Available memory dropped only slightly and no swap was used.",
    "steps": [
     "Incident memory averages 11.7 GB free against 12.3 GB.",
     "Swap stayed at zero.",
     "There is no pressure, so this row is green."
    ],
    "dataNote": "Sampled every 30s (Memory Available MBytes / Committed Bytes). Values in GB here."
   },
   "sampled": true
  },
  {
   "id": "GS-04-host-cpu-watch",
   "label": "CPU climbed into the amber band",
   "verdict": "watch",
   "rule": "Green under 70%; amber at 70-85% or rising; red at saturation (90%+ for 5 minutes) or iowait/steal dominating. Amber starts at half the red gap: at least 15% and 7.5 points worse than reference. Unit: percent of host CPU.",
   "chart": "stackedArea",
   "series": [
    {
     "name": "CPU used (user+sys)",
     "win": "normal",
     "pts": [
      {
       "t": 0,
       "v": 60
      },
      {
       "t": 10,
       "v": 63
      },
      {
       "t": 20,
       "v": 61
      },
      {
       "t": 30,
       "v": 62
      },
      {
       "t": 40,
       "v": 64
      },
      {
       "t": 50,
       "v": 62
      }
     ]
    },
    {
     "name": "CPU used (user+sys)",
     "win": "incident",
     "pts": [
      {
       "t": 60,
       "v": 70
      },
      {
       "t": 70,
       "v": 74
      },
      {
       "t": 80,
       "v": 78
      },
      {
       "t": 90,
       "v": 76
      },
      {
       "t": 100,
       "v": 80
      },
      {
       "t": 110,
       "v": 77
      }
     ]
    }
   ],
   "table": {
    "cols": [
     "Window",
     "CPU used",
     "Vs reference"
    ],
    "rows": [
     [
      "08:00-08:59",
      "62%",
      "reference"
     ],
     [
      "09:00-09:59",
      "76%",
      "+14 points (23% worse)"
     ]
    ]
   },
   "why": {
    "summary": "CPU rose about 23% above the reference, past the amber half-gap but under the 30% red gap.",
    "steps": [
     "Incident CPU averages 76% against 62%.",
     "23% is at least half of the red 30% gap, so amber.",
     "It does not reach the 30% and 15-point red test, so it is not red."
    ],
    "dataNote": "Sampled every 30s from Processor _Total % Processor Time."
   },
   "sampled": true
  },
  {
   "id": "GS-05-host-disk-watch",
   "label": "Disk latency rising",
   "verdict": "watch",
   "rule": "Green under 10 ms on SSD; amber when latency or queue clearly rises; red for a sustained queue with high latency. Amber starts at half the red gap (at least 15% and 7.5 points worse). Unit: milliseconds per transfer.",
   "chart": "line",
   "series": [
    {
     "name": "Avg disk sec/transfer",
     "win": "normal",
     "pts": [
      {
       "t": 0,
       "v": 7
      },
      {
       "t": 10,
       "v": 8
      },
      {
       "t": 20,
       "v": 9
      },
      {
       "t": 30,
       "v": 8
      },
      {
       "t": 40,
       "v": 8
      },
      {
       "t": 50,
       "v": 9
      }
     ]
    },
    {
     "name": "Avg disk sec/transfer",
     "win": "incident",
     "pts": [
      {
       "t": 60,
       "v": 15
      },
      {
       "t": 70,
       "v": 18
      },
      {
       "t": 80,
       "v": 22
      },
      {
       "t": 90,
       "v": 20
      },
      {
       "t": 100,
       "v": 25
      },
      {
       "t": 110,
       "v": 21
      }
     ]
    }
   ],
   "table": {
    "cols": [
     "Window",
     "Sec/transfer",
     "Queue"
    ],
    "rows": [
     [
      "08:00-08:59",
      "8 ms",
      "0.5"
     ],
     [
      "09:00-09:59",
      "20 ms",
      "1.1"
     ]
    ]
   },
   "why": {
    "summary": "Latency more than doubled, into the amber band, but no deep sustained queue formed.",
    "steps": [
     "Incident latency averages 20 ms against 8 ms, well past the amber half-gap.",
     "Queue depth only reached about 1, so the red queue test is not met.",
     "This is a watch item, not the bottleneck yet."
    ],
    "dataNote": "Sampled every 30s (Avg. Disk sec/Transfer, Current Disk Queue Length)."
   },
   "sampled": true
  },
  {
   "id": "GS-06-host-net-watch",
   "label": "Network throughput rising",
   "verdict": "watch",
   "rule": "Green when errors, drops and retransmits stay near zero; amber when they are occasional; red when they are sustained with impact. This row is partial: only throughput and bytes are collected, not retransmits. Unit: bytes per second; retransmits in counts.",
   "chart": "line",
   "series": [
    {
     "name": "Network bytes/sec",
     "win": "normal",
     "pts": [
      {
       "t": 0,
       "v": 4200000
      },
      {
       "t": 10,
       "v": 4100000
      },
      {
       "t": 20,
       "v": 4300000
      },
      {
       "t": 30,
       "v": 4000000
      },
      {
       "t": 40,
       "v": 4400000
      },
      {
       "t": 50,
       "v": 4200000
      }
     ]
    },
    {
     "name": "Network bytes/sec",
     "win": "incident",
     "pts": [
      {
       "t": 60,
       "v": 8200000
      },
      {
       "t": 70,
       "v": 9100000
      },
      {
       "t": 80,
       "v": 9600000
      },
      {
       "t": 90,
       "v": 8800000
      },
      {
       "t": 100,
       "v": 10200000
      },
      {
       "t": 110,
       "v": 9400000
      }
     ]
    }
   ],
   "table": {
    "cols": [
     "Window",
     "Bytes/sec",
     "Retransmits"
    ],
    "rows": [
     [
      "08:00-08:59",
      "4.2 MB/s",
      "not collected"
     ],
     [
      "09:00-09:59",
      "9.2 MB/s",
      "not collected"
     ]
    ]
   },
   "why": {
    "summary": "Traffic more than doubled, but the error and retransmit counters are not read today, so this can only be an amber watch.",
    "steps": [
     "Incident throughput averages 9.2 MB/s against 4.2 MB/s.",
     "Error and retransmit counters are not collected, so impact cannot be confirmed.",
     "Treat as a watch item and confirm with a manual netstat -s."
    ],
    "dataNote": "Sampled every 30s (Network Interface Bytes Total/sec). Retransmits are not in the collector."
   },
   "sampled": true
  },
  {
   "id": "GS-07-host-cpu-red",
   "label": "CPU saturated",
   "verdict": "bad",
   "rule": "Red at the named saturation level: CPU above 90% for 5 minutes or more. Green under 70%; amber at 70-85%. Unit: percent of host CPU.",
   "chart": "stackedArea",
   "series": [
    {
     "name": "CPU used (user+sys)",
     "win": "normal",
     "pts": [
      {
       "t": 0,
       "v": 44
      },
      {
       "t": 10,
       "v": 46
      },
      {
       "t": 20,
       "v": 43
      },
      {
       "t": 30,
       "v": 45
      },
      {
       "t": 40,
       "v": 47
      },
      {
       "t": 50,
       "v": 45
      }
     ]
    },
    {
     "name": "CPU used (user+sys)",
     "win": "incident",
     "pts": [
      {
       "t": 60,
       "v": 88
      },
      {
       "t": 70,
       "v": 92
      },
      {
       "t": 80,
       "v": 95
      },
      {
       "t": 90,
       "v": 94
      },
      {
       "t": 100,
       "v": 93
      },
      {
       "t": 110,
       "v": 91
      }
     ]
    }
   ],
   "table": {
    "cols": [
     "Window",
     "CPU used",
     "Vs reference"
    ],
    "rows": [
     [
      "08:00-08:59",
      "45%",
      "reference"
     ],
     [
      "09:00-09:59",
      "92%",
      "+47 points (104% worse); 92% for 6 minutes"
     ]
    ]
   },
   "why": {
    "summary": "CPU hit 92% and stayed above 90% for six minutes, the named saturation level.",
    "steps": [
     "Incident CPU averages 92% against 45%, a 104% increase.",
     "It stays above 90% for six minutes, the research's saturation test.",
     "The red rule is met, so CPU is the prime suspect."
    ],
    "dataNote": "Sampled every 30s from Processor _Total % Processor Time."
   },
   "sampled": true
  },
  {
   "id": "GS-08-host-mem-red",
   "label": "Memory exhausted, host thrashing",
   "verdict": "bad",
   "rule": "Red when the host thrashes or an OOM kill happens; amber when headroom is low. Unit: bytes free, pages swapped per second.",
   "chart": "line",
   "series": [
    {
     "name": "Available memory",
     "win": "normal",
     "pts": [
      {
       "t": 0,
       "v": 12000
      },
      {
       "t": 10,
       "v": 11800
      },
      {
       "t": 20,
       "v": 12100
      },
      {
       "t": 30,
       "v": 11700
      },
      {
       "t": 40,
       "v": 11900
      },
      {
       "t": 50,
       "v": 11800
      }
     ]
    },
    {
     "name": "Available memory",
     "win": "incident",
     "pts": [
      {
       "t": 60,
       "v": 600
      },
      {
       "t": 70,
       "v": 300
      },
      {
       "t": 80,
       "v": 180
      },
      {
       "t": 90,
       "v": 120
      },
      {
       "t": 100,
       "v": 95
      },
      {
       "t": 110,
       "v": 80
      }
     ]
    },
    {
     "name": "Swap out/sec",
     "win": "incident",
     "pts": [
      {
       "t": 60,
       "v": 120
      },
      {
       "t": 70,
       "v": 480
      },
      {
       "t": 80,
       "v": 900
      },
      {
       "t": 90,
       "v": 1300
      },
      {
       "t": 100,
       "v": 1600
      },
      {
       "t": 110,
       "v": 1500
      }
     ]
    }
   ],
   "table": {
    "cols": [
     "Window",
     "Available",
     "Swap out/sec"
    ],
    "rows": [
     [
      "08:00-08:59",
      "11.8 GB",
      "0"
     ],
     [
      "09:00-09:59",
      "0.2 GB",
      "1,300"
     ],
     [
      "OOM kills",
      "1 process",
      "-"
     ]
    ]
   },
   "why": {
    "summary": "Available memory fell to about 200 MB and the host swapped heavily, with one OOM kill.",
    "steps": [
     "Available memory dropped from 11.8 GB to under 300 MB.",
     "Swap out climbed to about 1,300 pages per second, a thrash pattern.",
     "One OOM kill was recorded, so the red rule is met."
    ],
    "dataNote": "Sampled every 30s (Memory Available MBytes, Pages/sec). Values in GB here."
   },
   "sampled": true
  },
  {
   "id": "GS-09-host-disk-red",
   "label": "Disk queue saturated",
   "verdict": "bad",
   "rule": "Red for a sustained queue with high latency; green under 10 ms on SSD with a near-zero queue; amber when both rise. Unit: milliseconds per transfer, queue depth in requests.",
   "chart": "line",
   "series": [
    {
     "name": "Avg disk sec/transfer",
     "win": "normal",
     "pts": [
      {
       "t": 0,
       "v": 9
      },
      {
       "t": 10,
       "v": 10
      },
      {
       "t": 20,
       "v": 11
      },
      {
       "t": 30,
       "v": 10
      },
      {
       "t": 40,
       "v": 9
      },
      {
       "t": 50,
       "v": 10
      }
     ]
    },
    {
     "name": "Avg disk sec/transfer",
     "win": "incident",
     "pts": [
      {
       "t": 60,
       "v": 35
      },
      {
       "t": 70,
       "v": 48
      },
      {
       "t": 80,
       "v": 60
      },
      {
       "t": 90,
       "v": 55
      },
      {
       "t": 100,
       "v": 62
      },
      {
       "t": 110,
       "v": 58
      }
     ]
    },
    {
     "name": "Disk queue length",
     "win": "incident",
     "pts": [
      {
       "t": 60,
       "v": 6
      },
      {
       "t": 70,
       "v": 11
      },
      {
       "t": 80,
       "v": 15
      },
      {
       "t": 90,
       "v": 14
      },
      {
       "t": 100,
       "v": 16
      },
      {
       "t": 110,
       "v": 15
      }
     ]
    }
   ],
   "table": {
    "cols": [
     "Window",
     "Sec/transfer",
     "Queue"
    ],
    "rows": [
     [
      "08:00-08:59",
      "10 ms",
      "1"
     ],
     [
      "09:00-09:59",
      "53 ms",
      "13"
     ]
    ]
   },
   "why": {
    "summary": "Latency rose five-fold with a queue of about 13 requests held for the hour.",
    "steps": [
     "Incident latency averages 53 ms against 10 ms.",
     "Queue depth stayed near 13, a sustained backlog.",
     "Both the latency and the sustained-queue tests are met, so red."
    ],
    "dataNote": "Sampled every 30s (PhysicalDisk Avg. Disk sec/Transfer, Current Disk Queue Length)."
   },
   "sampled": true
  },
  {
   "id": "GS-10-host-runqueue-gray",
   "label": "CPU run queue (not collected)",
   "verdict": "none",
   "rule": "Gray means the collector does not read this today, so absence is not a clean bill of health. Would be red for a sustained runnable queue, amber for transient. Unit: threads waiting for CPU.",
   "chart": "none",
   "series": [],
   "table": {
    "cols": [
     "What would be shown",
     "Why absent"
    ],
    "rows": [
     [
      "Runnable threads / run-queue length over the window",
      "No sampler probe reads a host run-queue counter today"
     ]
    ]
   },
   "why": {
    "summary": "A run queue would confirm CPU pressure, but no collector reads it, so no verdict is possible.",
    "steps": [
     "GetSystemTimes gives CPU busy time but no run-queue counter.",
     "Until a probe is added this stays gray.",
     "Read it manually with vmstat (r column) or PerfMon System\\Processor Queue Length."
    ],
    "dataNote": "Not collected. Needs a new sampler probe: PerfMon System\\Processor Queue Length or vmstat r."
   },
   "sampled": false
  },
  {
   "id": "GS-11-host-tcp-gray",
   "label": "TCP connection counts (not collected)",
   "verdict": "none",
   "rule": "Gray means the collector does not read this today. Would be red at connection exhaustion with waits, amber when approaching a limit. Unit: open connection count.",
   "chart": "none",
   "series": [],
   "table": {
    "cols": [
     "What would be shown",
     "Why absent"
    ],
    "rows": [
     [
      "Open TCP connections and time-wait counts over the window",
      "No sampler probe reads netstat connection counts today"
     ]
    ]
   },
   "why": {
    "summary": "Connection exhaustion can cause slowdowns, but nothing samples TCP connection counts here.",
    "steps": [
     "The collector reads host CPU, memory, disk and network bytes only.",
     "There is no connection-count series to judge.",
     "Read it manually with netstat -an or netstat -s."
    ],
    "dataNote": "Not collected. Needs a new probe for netstat/GetTcpStatistics connection counts."
   },
   "sampled": false
  },
  {
   "id": "GS-12-host-swap-gray",
   "label": "Swap activity (not collected)",
   "verdict": "none",
   "rule": "Gray means the collector does not read this today. Would be red for active thrashing, amber for occasional paging. Unit: pages swapped in/out per second.",
   "chart": "none",
   "series": [],
   "table": {
    "cols": [
     "What would be shown",
     "Why absent"
    ],
    "rows": [
     [
      "Swap in and swap out per second over the window",
      "No sampler probe reads swap counters today"
     ]
    ]
   },
   "why": {
    "summary": "Swap thrashing is a classic red sign, but the collector reads available and committed memory only, not swap paging.",
    "steps": [
     "Without a swap series, memory pressure can only be inferred from available memory.",
     "Until a probe is added this stays gray.",
     "Read it manually with vmstat (si/so) or PerfMon Paging File counters."
    ],
    "dataNote": "Not collected. Needs a new probe for swap in/out (vmstat si/so, or Paging File)."
   },
   "sampled": false
  }
 ]
};
