export interface FigureDetails {
  what: string;
  why: string;
  watch: string;
  stored: string;
}

export interface Figure {
  id: string;
  label: string;
  hint: string;
  details: FigureDetails;
}

export interface RecordGroup {
  id: string;
  title: string;
  always: boolean;
  toggle?: 'processes' | 'logsCatalogue' | 'copyLogsOnSnapshot';
  sqlHost?: true;
  /** The collector row that switches the group on and off (Record step, wizard.collectorRows). Not a record boolean, so no `toggle`. */
  row?: 'facts' | 'counters' | 'paths';
  figures: Figure[];
}

const SAMPLE_NUMBERS = 'One number for each available figure per sample. A figure Windows cannot read is left out, never saved as 0.';
const TARGET_NUMBERS = 'One row per watched target with its available figures. A missing figure is left out, never saved as 0.';
const SQL_NUMBERS = 'One number per available counter, per SQL Server instance and sample. A missing counter is left out, never saved as 0.';

export const RECORD_GROUPS: RecordGroup[] = [
  // source: groups_types.go DiskCPUGroup, MemPressureGroup and TCPGroup; sample.go Sample for the base figures.
  {
    id: 'machine',
    title: 'The whole machine',
    always: true,
    figures: [
      // source: sample.go Sample fields CPU, MemAvail, MemCommit, DiskQueue, DiskMs, NetIn, NetOut and DiskFreePct.
      {
        id: 'machine.base',
        label: 'Core figures',
        hint: 'Processor use, free memory, disk activity, network bytes and free space on the save drive.',
        details: {
          what: 'The main processor, memory, disk and network figures. It also includes free space on the save drive.',
          why: 'These figures show whether the whole server was busy or short of resources.',
          watch: 'High processor use, little free memory, slow disks, or a save drive that is nearly full.',
          stored: 'One number per available figure per sample. Network bytes are running totals. Missing figures are left out, never saved as 0.'
        }
      },
      // source: groups_types.go CPUSplit.
      {
        id: 'machine.cpu',
        label: 'Processor detail',
        hint: 'Jobs lined up for the processor, and time spent on Windows and system work.',
        details: {
          what: 'How busy the processor is. It counts jobs waiting in line and time spent on system work.',
          why: 'A long queue means programs are ready but cannot run yet. The other figures show where processor time went.',
          watch: 'A queue that stays long, or an unusual rise in Windows or interrupt time.',
          stored: SAMPLE_NUMBERS
        }
      },
      // source: groups_types.go MemPressureGroup.
      {
        id: 'machine.memory',
        label: 'Memory detail',
        hint: 'Hard page faults, Windows memory pools and page file use.',
        details: {
          what: 'Hard page faults, the two Windows memory pools, and page file use.',
          why: 'Hard faults and a busy page file can appear when Windows has little memory left.',
          watch: 'Hard faults that stay high, memory pools that keep growing, or a page file that is nearly full.',
          stored: 'Faults per second, pool sizes in bytes, and page file use as a percent. Missing figures are left out, never saved as 0.'
        }
      },
      // source: groups_types.go DiskSplit.
      {
        id: 'machine.disk',
        label: 'Disk detail',
        hint: 'Read and write times, disk activity and split operations for each disk.',
        details: {
          what: 'Read and write times, reads and writes per second, and split disk operations for each disk.',
          why: 'Slow disk reads and writes can add delay to work across the server.',
          watch: 'Read or write times that stay in the tens of milliseconds, or an unusual change in disk activity.',
          stored: 'A row per disk with its name and available figures. Missing figures are left out, never saved as 0.'
        }
      },
      // source: groups_types.go TCPGroup and TCPStateCount.
      {
        id: 'machine.network',
        label: 'Network detail',
        hint: 'Data sent again, resets, queued data and connection counts. Never addresses.',
        details: {
          what: 'How healthy the network is. It counts data sent again, dropped connections and data waiting to go out.',
          why: 'Resends and resets can show trouble on the network path. Queued data can show a slow path.',
          watch: 'Resends, resets or outgoing queues that rise over time.',
          stored: 'Counts and rates only, plus markers for partial reads. Never addresses, ports or network content.'
        }
      }
    ]
  },
  // source: groups_types.go HTTPGroup and HTTPQueue.
  {
    id: 'http',
    title: 'Web request queues',
    always: false,
    figures: [
      // source: groups_types.go HTTPQueue.
      {
        id: 'http.queues',
        label: 'Requests waiting to reach a website',
        hint: 'Queue size, oldest waiting request, rejected requests and new request rate.',
        details: {
          what: 'The queue size, oldest queue item age, rejected requests and new request rate for each web app pool.',
          why: 'Requests gather here before website code runs. Rejected requests did not get through.',
          watch: 'A growing queue or oldest item age, and any rejected requests.',
          stored: 'A row per app pool with its available queue figures. Missing figures are left out, never saved as 0.'
        }
      }
    ]
  },
  // source: groups_types.go IISGroup and IISWorker.
  {
    id: 'iis',
    title: 'Web workers',
    always: false,
    figures: [
      // source: groups_types.go IISWorker.
      {
        id: 'iis.workers',
        label: 'Activity in watched web app pools',
        hint: 'Active requests, active threads and ping time for watched web app pools.',
        details: {
          what: 'Active requests, active threads and ping time for workers of the web app pools you watch.',
          why: 'These figures show whether a watched website was busy and whether its worker answered a ping.',
          watch: 'Requests or threads that keep growing, and ping times that rise.',
          stored: 'The app pool, the process number and when it started, plus the activity figures. Only app pools you watch are included.'
        }
      }
    ]
  },
  // source: groups_types.go ASPNETGroup.
  {
    id: 'aspnet',
    title: 'ASP.NET',
    always: false,
    figures: [
      // source: groups_types.go ASPNETGroup.
      {
        id: 'aspnet.activity',
        label: 'ASP.NET requests and restarts',
        hint: 'Waiting, current and rejected requests, wait time and app restarts.',
        details: {
          what: 'How many ASP.NET requests are waiting, running or turned away. Also how long they wait and how often the app restarts.',
          why: 'Queues and restarts can help explain pauses that people using a website noticed.',
          watch: 'A queue that stays above 0, longer waits, rejected requests, or a restart you did not expect.',
          stored: SAMPLE_NUMBERS
        }
      }
    ]
  },
  // source: groups_types.go CLRGroup and CLRTarget.
  {
    id: 'clr',
    title: '.NET Framework programs',
    always: false,
    figures: [
      // source: groups_types.go CLRTarget.
      {
        id: 'clr.memory',
        label: '.NET Framework memory and threads',
        hint: 'Memory use, cleanup time, locks, threads and errors for watched .NET Framework programs.',
        details: {
          what: 'How the .NET runtime uses memory. It counts memory size, cleanup time, locks, threads and errors.',
          why: 'These figures show how a watched .NET Framework program used memory and processor time.',
          watch: 'Memory cleanup time that stays high, growing heaps, or a rise in contention or exceptions.',
          stored: TARGET_NUMBERS + ' Only watched .NET Framework targets are included.'
        }
      }
    ]
  },
  // source: sample.go ProcRow; this is the host process list controlled by record.processes.
  {
    id: 'processes',
    title: 'Process list',
    always: false,
    toggle: 'processes',
    figures: [
      // source: sample.go ProcRow.
      {
        id: 'processes.list',
        label: 'Busiest programs and programs you watch',
        hint: 'Program names, processor use, memory, handles and threads.',
        details: {
          what: 'The top 10 programs by processor use and top 5 by memory, plus every program you watch.',
          why: 'This shows which programs used the machine, not just that the machine was busy.',
          watch: 'One program using most of the processor or memory, or a handle count that keeps growing.',
          stored: 'Process number, program name and start time, plus processor, memory, handle and thread figures. Never the path or command line.'
        }
      }
    ]
  },
  // source: groups_types.go Proc10Group and Proc10Target.
  {
    id: 'proc10',
    title: '.NET 10 programs',
    always: false,
    figures: [
      // source: groups_types.go Proc10Target.
      {
        id: 'proc10.detail',
        label: '.NET 10 processor and memory',
        hint: 'Processor and memory use for each watched .NET 10 program.',
        details: {
          what: 'How much processor and memory each watched .NET 10 program uses.',
          why: 'These figures separate one watched program from the load on the whole machine.',
          watch: 'A watched program with high processor use or memory that keeps growing.',
          stored: TARGET_NUMBERS + ' Only watched .NET 10 targets are included.'
        }
      }
    ]
  },
  // source: groups_types.go ProcIOGroup and ProcIOTarget.
  {
    id: 'procIO',
    title: 'Watched program activity',
    always: false,
    figures: [
      // source: groups_types.go ProcIOTarget.
      {
        id: 'procIO.detail',
        label: 'Disk and memory activity for watched programs',
        hint: 'Disk activity, memory use and running time for each watched program.',
        details: {
          what: 'Bytes read and written per second, page file and virtual memory use, and running time.',
          why: 'This shows disk and memory use for one watched program. A reset running time can mark a restart.',
          watch: 'A lot of disk use, memory that keeps growing, or a program that keeps restarting.',
          stored: TARGET_NUMBERS + ' Only watched targets are included.'
        }
      }
    ]
  },
  // source: groups_types.go EventsGroup and EventEntry.
  {
    id: 'events',
    title: 'Selected Windows events',
    always: false,
    figures: [
      // source: groups_types.go EventEntry.
      {
        id: 'events.system',
        label: 'System log',
        hint: 'Disk errors, low resources, and web app pool failures and restarts.',
        details: {
          what: 'Chosen System events. They cover disk problems, low resources, and web app pools that fail or restart.',
          why: 'These events can add a precise time to a disk, resource or website problem.',
          watch: 'A disk error, resource warning, or app pool recycle near the time of the problem.',
          stored: 'Event ID, level, time and count. Never the event message or account names.'
        }
      },
      // source: groups_types.go EventEntry.
      {
        id: 'events.application',
        label: 'Application log',
        hint: 'Program crashes and .NET runtime errors.',
        details: {
          what: 'Chosen Application events. They cover programs that crash and .NET errors.',
          why: 'A crash or runtime failure can help explain a sudden stop or restart.',
          watch: 'A crash or runtime failure close to the start of the problem.',
          stored: 'Event ID, level, time and count. Never the event message or program paths.'
        }
      }
    ]
  },
  // source: sample.go LogEntry; record.logsCatalogue controls this list.
  {
    id: 'logsCatalogue',
    title: 'IIS log catalogue',
    always: false,
    toggle: 'logsCatalogue',
    figures: [
      // source: sample.go LogEntry.
      {
        id: 'logs.catalogue',
        label: 'Log file names, sizes and times',
        hint: 'Paths, sizes and times for the newest 200 log files in your chosen folders.',
        details: {
          what: 'The newest 200 .log files in the log folders you choose. The default folder holds IIS logs.',
          why: 'This helps you find the right log for the hour of an incident without reading its content.',
          watch: 'A log that stopped growing, grew very fast, or is not in the folders you chose.',
          stored: 'Full file path, size and last write time. Never the content of the log.'
        }
      }
    ]
  },
  // source: sample.go LogEntry and snapshot.go CopyLogsOnSnapshot.
  {
    id: 'copyLogsOnSnapshot',
    title: 'Log copies in a snapshot',
    always: false,
    toggle: 'copyLogsOnSnapshot',
    figures: [
      // source: sample.go LogEntry; snapshot.go copies the files when record.copyLogsOnSnapshot is on.
      {
        id: 'logs.snapshot',
        label: 'Log files from the snapshot window',
        hint: 'Copies log files from the snapshot window only when you take a snapshot.',
        details: {
          what: 'Copies log files from the snapshot window, only when you take a snapshot.',
          why: 'The copies keep the log evidence with that snapshot before old logs are removed.',
          watch: 'These copies use extra disk space. This setting is off in Recommended.',
          stored: 'A copy of each selected log file at snapshot time. Nothing is copied at other times.'
        }
      }
    ]
  },
  // source: groups_types.go DumpsGroup and DumpEntry.
  {
    id: 'dumps',
    title: 'Crash dump list',
    always: false,
    figures: [
      // source: groups_types.go DumpEntry.
      {
        id: 'dumps.list',
        label: 'Crash dump files found',
        hint: 'Names, sizes and times for the newest 200 crash dump files. Never their content.',
        details: {
          what: 'The newest 200 crash dump files in the standard Windows crash folders.',
          why: 'A new dump can show that Windows or a program crashed.',
          watch: 'Any new dump near the time of the problem.',
          stored: 'File name, size, time and target when known. Never the dump content. The Blackbox never creates dumps.'
        }
      }
    ]
  },
  // source: groups_types.go SQLHostGroup and SQLHostInstance; sqlhost.go sqlHostCounters.
  {
    id: 'sqlHost',
    title: 'SQL Server host counters',
    always: false,
    sqlHost: true,
    figures: [
      // source: SQLHostInstance.BatchRequestsPerSec and sqlhost.go Batch Requests/sec.
      {
        id: 'sqlHost.batchRequestsPerSec',
        label: 'Batch requests per second',
        hint: 'How many batches of work SQL Server gets each second.',
        details: {
          what: 'How many batches of work SQL Server receives each second.',
          why: 'This is a simple measure of how busy SQL Server is.',
          watch: 'A sudden change from the usual level, or a low value while people report active work.',
          stored: SQL_NUMBERS
        }
      },
      // source: SQLHostInstance.UserConnections and sqlhost.go User Connections.
      {
        id: 'sqlHost.userConnections',
        label: 'User connections',
        hint: 'How many apps and people are connected right now.',
        details: {
          what: 'How many apps and people have a SQL Server connection right now.',
          why: 'A count that only climbs can show that apps are not closing connections.',
          watch: 'A steady climb over hours, or a sharp change from the usual count.',
          stored: SQL_NUMBERS
        }
      },
      // source: SQLHostInstance.LockWaitsPerSec and sqlhost.go Lock Waits/sec.
      {
        id: 'sqlHost.lockWaitsPerSec',
        label: 'Lock waits per second',
        hint: 'How often requests pause while another request holds a lock.',
        details: {
          what: 'How often a request pauses each second while another request holds a lock on data.',
          why: 'Locks protect data, but lock waits can slow requests down.',
          watch: 'Values that stay above 0 or rise during busy periods.',
          stored: SQL_NUMBERS
        }
      },
      // source: SQLHostInstance.ProcessesBlocked and sqlhost.go Processes Blocked.
      {
        id: 'sqlHost.processesBlocked',
        label: 'Processes blocked',
        hint: 'Requests held while another request uses the data they need.',
        details: {
          what: 'Requests held up right now while another request holds the data they need.',
          why: 'A blocked process cannot finish its current request yet.',
          watch: 'A value above 0 for several samples in a row.',
          stored: SQL_NUMBERS
        }
      },
      // source: SQLHostInstance.MemoryGrantsPending and sqlhost.go Memory Grants Pending.
      {
        id: 'sqlHost.memoryGrantsPending',
        label: 'Memory grants pending',
        hint: 'Queries that need memory before they can start.',
        details: {
          what: 'Queries that are ready to run but still need SQL Server to give them memory.',
          why: 'These queries cannot start until SQL Server gives them memory.',
          watch: 'A value above 0 that lasts for several samples.',
          stored: SQL_NUMBERS
        }
      },
      // source: SQLHostInstance.PageLifeExpectancy and sqlhost.go Page life expectancy.
      {
        id: 'sqlHost.pageLifeExpectancy',
        label: 'Page life expectancy',
        hint: 'How long data stays in memory. A fast fall can be a warning.',
        details: {
          what: 'How many seconds a page of data stays in SQL Server memory.',
          why: 'A short stay can mean SQL Server reads the same data from disk more often.',
          watch: 'A sudden fall or a lasting drop from the usual value for this server.',
          stored: SQL_NUMBERS
        }
      },
      // source: SQLHostInstance.FreeListStallsPerSec and sqlhost.go Free list stalls/sec.
      {
        id: 'sqlHost.freeListStallsPerSec',
        label: 'Free list stalls per second',
        hint: 'How often work pauses while no memory page is free.',
        details: {
          what: 'How often a request pauses each second while no free memory page is ready.',
          why: 'This can appear when SQL Server has little free memory for new work.',
          watch: 'Numbers that are often above 0, most of all when page life drops.',
          stored: SQL_NUMBERS
        }
      },
      // source: SQLHostInstance.CompilationsPerSec and sqlhost.go SQL Compilations/sec.
      {
        id: 'sqlHost.compilationsPerSec',
        label: 'Compilations per second',
        hint: 'How often SQL Server builds query plans each second.',
        details: {
          what: 'How often SQL Server builds a new plan for running a query each second.',
          why: 'Building query plans uses processor time.',
          watch: 'A high value compared with batch requests, or a rise from the usual level.',
          stored: SQL_NUMBERS
        }
      },
      // source: SQLHostInstance.ReCompilationsPerSec and sqlhost.go SQL Re-Compilations/sec.
      {
        id: 'sqlHost.reCompilationsPerSec',
        label: 'Re-compilations per second',
        hint: 'How often SQL Server builds saved query plans again each second.',
        details: {
          what: 'How often SQL Server replaces a saved query plan with a new one each second.',
          why: 'Rebuilding plans uses processor time and can change how long a query takes.',
          watch: 'A rise together with higher processor use or slower queries.',
          stored: SQL_NUMBERS
        }
      },
      // source: SQLHostInstance.DeadlocksPerSec and sqlhost.go Number of Deadlocks/sec.
      {
        id: 'sqlHost.deadlocksPerSec',
        label: 'Deadlocks per second',
        hint: 'How often requests block each other and one is cancelled.',
        details: {
          what: 'How often requests lock each other and SQL Server cancels one of them.',
          why: 'A cancelled request can fail for the person or app that sent it.',
          watch: 'Any value that stays above 0.',
          stored: SQL_NUMBERS
        }
      },
      // source: SQLHostInstance.LockWaitTimeMs and sqlhost.go Lock Wait Time (ms).
      {
        id: 'sqlHost.lockWaitTimeMs',
        label: 'Lock wait time in milliseconds',
        hint: 'Time requests spent on lock waits in the last second.',
        details: {
          what: 'The time spent on lock waits in the last second, in milliseconds.',
          why: 'This adds how long requests waited to the count of lock waits.',
          watch: 'A rise together with more lock waits or blocked processes.',
          stored: SQL_NUMBERS
        }
      },
      // source: SQLHostInstance.LogFlushWaitTimeMs and sqlhost.go Log Flush Wait Time.
      {
        id: 'sqlHost.logFlushWaitTimeMs',
        label: 'Log flush wait time in milliseconds',
        hint: 'How long SQL Server waited to write its log in the last second.',
        details: {
          what: 'Time in the last second spent on writes to the SQL Server transaction log.',
          why: 'Slow log writes can add delay to database changes.',
          watch: 'A lasting rise from the usual value for this server.',
          stored: SQL_NUMBERS
        }
      },
      // source: SQLHostInstance.ForwardedRecordsPerSec and sqlhost.go Forwarded Records/sec.
      {
        id: 'sqlHost.forwardedRecordsPerSec',
        label: 'Forwarded records per second',
        hint: 'Extra row moves that can slow down table reads.',
        details: {
          what: 'Rows that moved and left a pointer behind, which adds a step when SQL Server reads them.',
          why: 'Many forwarded records can add work to table reads.',
          watch: 'A high value compared with batch requests or the usual level for this server.',
          stored: SQL_NUMBERS
        }
      }
    ]
  },
  // source: internal/blackbox collector host.facts (once an hour).
  {
    id: 'hostFacts',
    title: 'Server facts',
    always: false,
    row: 'facts',
    figures: [
      {
        id: 'facts.versions',
        label: 'Versions and install paths',
        hint: 'Windows, .NET, IIS, SQL Server and Redis versions, and where they are installed.',
        details: {
          what: 'The versions of Windows, .NET, IIS, SQL Server and Redis, and the folder of each.',
          why: 'Servers on other versions can act in other ways.',
          watch: 'A version that changed between two recordings, or that differs from a server that works.',
          stored: 'Product names, version numbers and folder paths, once an hour. Content of files and secrets are never read.'
        }
      },
      {
        id: 'facts.services',
        label: 'Services and their state',
        hint: 'Name, state and start type. Never accounts or passwords.',
        details: {
          what: 'Each Windows service with its name, whether it is running, and how it starts.',
          why: 'A service that stopped, or was set to start by hand, can be the start of a problem.',
          watch: 'A service that is stopped but should start on its own.',
          stored: 'Service name, state and start type, once an hour. Never the account a service runs as, and never passwords.'
        }
      },
      {
        id: 'facts.certificates',
        label: 'Certificate expiry dates',
        hint: 'Subject, thumbprint and end date for the computer store. Never private keys.',
        details: {
          what: 'Each certificate in the computer store, with the date it ends.',
          why: 'An expired certificate can stop sites and connections from working.',
          watch: 'A certificate that ends soon, or that has already ended.',
          stored: 'Subject, thumbprint and end date, once an hour. Never private keys, and never what the certificate protects.'
        }
      },
      {
        id: 'facts.timeReboot',
        label: 'Time sync and pending reboot',
        hint: 'How far the clock is off, and whether Windows waits for a restart.',
        details: {
          what: 'How far this server clock is from its time source, and a yes or no for a restart that is still due.',
          why: 'A clock that is off can confuse logins and make times in different logs disagree. A waiting restart means an update is not finished.',
          watch: 'A clock that is off by more than a few seconds, or a restart that stays due for days.',
          stored: 'The clock offset and a yes or no for the restart, once an hour. Nothing else is read.'
        }
      }
    ]
  },
  // source: internal/blackbox/extracounters.go counters.extra.
  {
    id: 'extraCounters',
    title: 'Extra Windows counters',
    always: false,
    row: 'counters',
    figures: [
      {
        id: 'counters.extra',
        label: 'Counters you add',
        hint: 'One counter path per line, up to 50. A path that does not exist is skipped and reported.',
        details: {
          what: 'Any Windows counter you name, read the same way Performance Monitor reads it.',
          why: 'Some problems show up in a counter the built-in figures do not include.',
          watch: 'A counter that rises or falls at the time of the problem.',
          stored: 'One number per counter and sample. A counter that does not exist is skipped and reported, never saved as 0.'
        }
      }
    ]
  },
  // source: internal/blackbox collector paths.files.
  {
    id: 'filePaths',
    title: 'Log and config file paths',
    always: false,
    row: 'paths',
    figures: [
      {
        id: 'paths.web',
        label: 'IIS, HTTP.sys and failed request tracing folders',
        hint: 'Which folders hold the web logs.',
        details: {
          what: 'The folders where IIS, HTTP.sys and failed request tracing write their logs.',
          why: 'Knowing where the web logs are saves time when you need to open them.',
          watch: 'A log folder that moved, or that is on a drive that is nearly full.',
          stored: 'Folder paths, sizes and times only. The content of files is never read.'
        }
      },
      {
        id: 'paths.sqlRedis',
        label: 'SQL Server and Redis logs and settings',
        hint: 'ERRORLOG, redis.windows.conf and the Redis log.',
        details: {
          what: 'Where the SQL Server error log, the Redis settings file and the Redis log are.',
          why: 'These files often hold the reason a database or cache misbehaved.',
          watch: 'A log with a recent time when the problem started.',
          stored: 'Path, size and time only. The content of files and any secret in them are never read.'
        }
      },
      {
        id: 'paths.salt',
        label: 'Salt minion log',
        hint: 'Path and time of the last write.',
        details: {
          what: 'Where the Salt minion log is, and when it was last written to.',
          why: 'A recent write shows that Salt was active near the time of a change.',
          watch: 'Salt activity just before the problem started.',
          stored: 'Path and time of the last write only. The content of the log is never read.'
        }
      },
      {
        id: 'paths.own',
        label: 'Unravis and Blackbox own logs',
        hint: 'Path, size and time.',
        details: {
          what: 'Where the Unravis and Blackbox logs are, with their size and last write time.',
          why: 'These logs say what the recorder itself did, such as a figure it could not read.',
          watch: 'A log that grows fast, or that stopped being written.',
          stored: 'Path, size and time only. The content of files is never read.'
        }
      }
    ]
  }
];
