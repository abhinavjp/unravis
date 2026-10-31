var EVDEP_DEFS = {
  querystore: {
    group: 'sql',
    soft: false,
    title: 'Query Store is off for this database',
    why: 'Query Store keeps the history of slow queries and plan changes. While it is off there is nothing to compare the incident with.',
    effect: 'Cards that need query history show Not collected instead of Looked normal.',
    fix: ['ALTER DATABASE [YourDb] SET QUERY_STORE = ON;'],
    note: 'Ask a database administrator to run this. Unravis only reads and never runs it.'
  },
  qswaits: {
    group: 'sql',
    soft: true,
    title: 'Per-query wait types are not available',
    why: 'Per-query wait types require SQL Server 2017+ with Query Store wait stats enabled.',
    effect: 'The wait column is left out of the query table. The rest of the card still works.',
    fix: ['ALTER DATABASE [YourDb] SET QUERY_STORE (WAIT_STATS_CAPTURE_MODE = ON);'],
    note: 'Ask a database administrator to run this. Unravis only reads and never runs it.'
  },
  systemhealth: {
    group: 'sql',
    soft: false,
    title: 'Live deadlock capture is stopped',
    why: 'Live deadlock capture is stopped. Past deadlocks may be in the system_health .xel files on the server.',
    effect: 'The deadlock card cannot say there were none.',
    fix: ['ALTER EVENT SESSION [system_health] ON SERVER STATE = START;'],
    note: 'Ask a database administrator to run this. Unravis only reads and never runs it.'
  },
  viewstate: {
    group: 'sql',
    soft: false,
    title: 'No permission to read server state',
    why: 'The Unravis login lacks VIEW SERVER STATE, so wait statistics and live blocking cannot be read.',
    effect: 'Wait, blocking, memory and tempdb cards show Not collected.',
    fix: ['GRANT VIEW SERVER STATE TO [UnravisReader];', '-- SQL Server 2022 and later: GRANT VIEW SERVER PERFORMANCE STATE TO [UnravisReader];'],
    note: 'Ask a database administrator to run this. Unravis only reads and never runs it.'
  },
  profiler: {
    group: 'mongo',
    soft: false,
    title: 'The database profiler is off',
    why: 'MongoDB only records slow operations while the profiler is on. With it off, a quiet result may only mean nothing was recorded.',
    effect: 'Slow operation and scan cards show Not collected.',
    fix: ["db.setProfilingLevel(1, { slowms: 100 })"],
    note: 'Run in the mongo shell on the database to watch, by a database administrator. Unravis never runs it.'
  },
  monitorrole: {
    group: 'mongo',
    soft: false,
    title: 'No permission to read server health',
    why: 'The Unravis user lacks the clusterMonitor role, so tickets, cache, connections and replication status cannot be read.',
    effect: 'Ticket, cache, connection, lock and replication cards show Not collected.',
    fix: ['use admin', "db.grantRolesToUser('unravis', [{ role: 'clusterMonitor', db: 'admin' }])"],
    note: 'Run in the mongo shell by a database administrator. Unravis never runs it.'
  },
  oplogread: {
    group: 'mongo',
    soft: false,
    title: 'No permission to read the oplog',
    why: 'The Unravis user cannot read the local database, so the oplog window cannot be measured.',
    effect: 'The oplog card shows Not collected.',
    fix: ['use admin', "db.grantRolesToUser('unravis', [{ role: 'read', db: 'local' }])"],
    note: 'Run in the mongo shell by a database administrator. Unravis never runs it.'
  },
  blackbox: {
    group: 'host',
    soft: false,
    title: 'No Blackbox on this host',
    why: 'Processor, memory, disk and network figures come from a small Unravis helper called a Blackbox. None is placed on this host.',
    effect: 'All host cards show Not collected.',
    fix: [],
    note: 'Place a Blackbox from the Setup page. Nothing is installed from here.'
  },
  perfaccess: {
    group: 'host',
    soft: false,
    title: 'No access to performance counters',
    why: 'The account running the Blackbox is not allowed to read Windows performance counters.',
    effect: 'Run queue, TCP and swap cards show Not collected.',
    fix: ['net localgroup "Performance Monitor Users" DOMAIN\\unravis /add'],
    note: 'Run as an administrator on the host. Unravis never runs it.'
  }
};

var EVDEP_MAP = {
  'sql-waits-benign': ['viewstate'], 'sql-topqueries-orders': ['querystore', 'qswaits'], 'sql-log-growth': [], 'sql-lock-waits-rising': ['viewstate'],
  'sql-memory-grants': ['viewstate'], 'sql-index-maintenance': [], 'sql-waits-bottleneck': ['viewstate'], 'sql-blocking-chain': ['viewstate'],
  'sql-tempdb-contention': ['viewstate'], 'sql-plan-regression': ['querystore'], 'sql-deadlocks': ['systemhealth'],
  'sql-cpu-high': ['querystore', 'qswaits'],
  'MG-BAD-SLOWOPS': ['profiler'], 'MG-BAD-TICKETS': ['monitorrole'], 'MG-BAD-REPLAG': ['monitorrole'], 'MG-WATCH-CACHE': ['monitorrole'],
  'MG-WATCH-CONNS': ['monitorrole'], 'MG-WATCH-GLOCK': ['monitorrole'], 'MG-OK-SLOWOPS': ['profiler'], 'MG-OK-TICKETS': ['monitorrole'],
  'MG-OK-REPLAG': ['monitorrole'], 'MG-NONE-COLLSCAN': ['profiler'], 'MG-NONE-INDEX': ['profiler'], 'MG-NONE-OPLOG': ['oplogread'],
  'GS-01-host-cpu-ok': ['blackbox'], 'GS-02-host-disk-ok': ['blackbox'], 'GS-03-host-mem-ok': ['blackbox'], 'GS-04-host-cpu-watch': ['blackbox'],
  'GS-05-host-disk-watch': ['blackbox'], 'GS-06-host-net-watch': ['blackbox'], 'GS-07-host-cpu-red': ['blackbox'], 'GS-08-host-mem-red': ['blackbox'],
  'GS-09-host-disk-red': ['blackbox'], 'GS-10-host-runqueue-gray': ['blackbox', 'perfaccess'], 'GS-11-host-tcp-gray': ['blackbox', 'perfaccess'],
  'GS-12-host-swap-gray': ['blackbox', 'perfaccess']
};
