package blackbox

import "time"

type HTTPGroup struct {
	Queues []HTTPQueue `json:"queues,omitempty"`
}

type HTTPQueue struct {
	Pool             string   `json:"pool"`
	CurrentQueueSize *float64 `json:"currentQueueSize,omitempty"`
	MaxQueueItemAge  *float64 `json:"maxQueueItemAge,omitempty"`
	RejectedRequests *float64 `json:"rejectedRequests,omitempty"`
	ArrivalRate      *float64 `json:"arrivalRate,omitempty"`
}

type IISGroup struct {
	Workers []IISWorker `json:"workers,omitempty"`
}

type IISWorker struct {
	Pool string `json:"pool"`
	PID  uint32 `json:"pid"`
	// StartTicks is the worker's verified creation identity: the raw FILETIME
	// creation ticks (100-ns since 1601) read from the Process object's Creation
	// Time in the same atomic query that verified the worker. Additive and
	// optional (omitted when 0/unreadable, never renamed): a recycled worker PID
	// stays distinguishable downstream even when two lifetimes share a unix
	// second. Old recordings without the field decode to 0; old consumers that
	// ignore it keep working.
	StartTicks     int64    `json:"startTicks,omitempty"`
	ActiveRequests *float64 `json:"activeRequests,omitempty"`
	ActiveThreads  *float64 `json:"activeThreads,omitempty"`
	PingLatencyMs  *float64 `json:"pingLatencyMs,omitempty"`
}

type DiskCPUGroup struct {
	Disks []DiskSplit `json:"disks,omitempty"`
	CPU   *CPUSplit   `json:"cpu,omitempty"`
}

type DiskSplit struct {
	Name          string   `json:"name"`
	ReadMs        *float64 `json:"readMs,omitempty"`
	WriteMs       *float64 `json:"writeMs,omitempty"`
	ReadsPerSec   *float64 `json:"readsPerSec,omitempty"`
	WritesPerSec  *float64 `json:"writesPerSec,omitempty"`
	SplitIOPerSec *float64 `json:"splitIOPerSec,omitempty"`
}

type CPUSplit struct {
	QueueLength   *float64 `json:"queueLength,omitempty"`
	PrivilegedPct *float64 `json:"privilegedPct,omitempty"`
	DPCPct        *float64 `json:"dpcPct,omitempty"`
	InterruptPct  *float64 `json:"interruptPct,omitempty"`
	// UtilityPct is the modern CPU load (Windows Task Manager's number, frequency
	// aware, can pass 100 on boost). ContextSwitchesPerSec is the machine-wide
	// switch rate: a jump with flat CPU points at thread thrash.
	UtilityPct            *float64 `json:"utilityPct,omitempty"`
	ContextSwitchesPerSec *float64 `json:"contextSwitchesPerSec,omitempty"`
}

type SQLHostGroup struct {
	Instances []SQLHostInstance `json:"instances,omitempty"`
}

type SQLHostInstance struct {
	Instance               string   `json:"instance"`
	BatchRequestsPerSec    *float64 `json:"batchRequestsPerSec,omitempty"`
	UserConnections        *float64 `json:"userConnections,omitempty"`
	LockWaitsPerSec        *float64 `json:"lockWaitsPerSec,omitempty"`
	ProcessesBlocked       *float64 `json:"processesBlocked,omitempty"`
	MemoryGrantsPending    *float64 `json:"memoryGrantsPending,omitempty"`
	PageLifeExpectancy     *float64 `json:"pageLifeExpectancy,omitempty"`
	FreeListStallsPerSec   *float64 `json:"freeListStallsPerSec,omitempty"`
	CompilationsPerSec     *float64 `json:"compilationsPerSec,omitempty"`
	ReCompilationsPerSec   *float64 `json:"reCompilationsPerSec,omitempty"`
	DeadlocksPerSec        *float64 `json:"deadlocksPerSec,omitempty"`
	LockWaitTimeMs         *float64 `json:"lockWaitTimeMs,omitempty"`
	LogFlushWaitTimeMs     *float64 `json:"logFlushWaitTimeMs,omitempty"`
	ForwardedRecordsPerSec *float64 `json:"forwardedRecordsPerSec,omitempty"`
}

type CLRGroup struct {
	Targets []CLRTarget `json:"targets,omitempty"`
}

type CLRTarget struct {
	Target           string   `json:"target"`
	Gen0Bytes        *uint64  `json:"gen0Bytes,omitempty"`
	Gen1Bytes        *uint64  `json:"gen1Bytes,omitempty"`
	Gen2Bytes        *uint64  `json:"gen2Bytes,omitempty"`
	LOHBytes         *uint64  `json:"lohBytes,omitempty"`
	PctTimeInGC      *float64 `json:"pctTimeInGC,omitempty"`
	AllocBytesPerSec *float64 `json:"allocBytesPerSec,omitempty"`
	ContentionPerSec *float64 `json:"contentionPerSec,omitempty"`
	Threads          *float64 `json:"threads,omitempty"`
	ExceptionsPerSec *float64 `json:"exceptionsPerSec,omitempty"`
}

type Proc10Group struct {
	Targets []Proc10Target `json:"targets,omitempty"`
}

type Proc10Target struct {
	Target       string   `json:"target"`
	CPUPct       *float64 `json:"cpuPct,omitempty"`
	WorkingSet   *uint64  `json:"workingSet,omitempty"`
	PrivateBytes *uint64  `json:"privateBytes,omitempty"`
}

type ProcIOGroup struct {
	Targets []ProcIOTarget `json:"targets,omitempty"`
}

type ProcIOTarget struct {
	Target             string   `json:"target"`
	IOReadBytesPerSec  *float64 `json:"ioReadBytesPerSec,omitempty"`
	IOWriteBytesPerSec *float64 `json:"ioWriteBytesPerSec,omitempty"`
	PageFileBytes      *uint64  `json:"pageFileBytes,omitempty"`
	VirtualBytes       *uint64  `json:"virtualBytes,omitempty"`
	ElapsedSec         *float64 `json:"elapsedSec,omitempty"`
}

type EventsGroup struct {
	Events []EventEntry `json:"events,omitempty"`
	// Truncated says the per-channel read budget was exhausted with backlog
	// still unread: the cursor continues forward next tick, so this is a
	// partial read, never a silent gap (W6-01/W6-02).
	Truncated bool `json:"truncated,omitempty"`
	// Unreadable counts records that were fetched but could not be rendered
	// (bad variant, unreadable Level/ID/time). They are missing, never 0, and
	// they still charged the read budget (W6-02).
	Unreadable uint32 `json:"unreadable,omitempty"`
	// Lost counts records intentionally dropped from a channel generation
	// (log clear/reset). An explicit numeric loss marker, never a silent gap
	// (W6-01).
	Lost uint64 `json:"lost,omitempty"`

	// commit/pending carry the W6-06 acknowledgement: the read cursor is
	// published only by Ack(), which the recorder calls after Store.Append has
	// durably accepted the sample - never inside Collect and never as a side
	// effect of serialization. Unexported so they are never serialized; nil on
	// a batch read back from disk.
	commit  *eventCommittedStore
	pending eventCursors
}

type EventEntry struct {
	Channel uint8     `json:"channel"` // fixed enum: 1=System, 2=Application
	EventID uint32    `json:"eventId"`
	Level   uint8     `json:"level"`
	Count   uint32    `json:"count"`
	T       time.Time `json:"t"`
}

type TCPGroup struct {
	RetransmitsPerSec *float64 `json:"retransmitsPerSec,omitempty"`
	ResetsPerSec      *float64 `json:"resetsPerSec,omitempty"`
	OutboundQueue     *float64 `json:"outboundQueue,omitempty"`
	// OutboundQueuePartial marks the outbound-queue sum as covering only some
	// interfaces: at least one expected interface was unreadable, so the total
	// is not a complete machine-wide reading (W6-04).
	OutboundQueuePartial bool `json:"outboundQueuePartial,omitempty"`
	// OutboundQueueInterfaces is the number of interfaces that contributed to
	// OutboundQueue (the covered-interface count, W6-04).
	OutboundQueueInterfaces int `json:"outboundQueueInterfaces,omitempty"`
	// ConnFailuresPerSec: connection attempts that failed (TCPv4 plus TCPv6).
	// DiscardedPerSec: inbound packets dropped with no error, all adapters
	// summed. OutErrorsPerSec: outbound packets that failed, all adapters summed.
	ConnFailuresPerSec *float64        `json:"connFailuresPerSec,omitempty"`
	DiscardedPerSec    *float64        `json:"discardedPerSec,omitempty"`
	OutErrorsPerSec    *float64        `json:"outErrorsPerSec,omitempty"`
	States             []TCPStateCount `json:"states,omitempty"`
	// StatesTruncated says the TCP table exceeded the checked byte/row ceiling
	// and could not be read completely: States is partial or absent, never an
	// unlabeled complete count (W6-03).
	StatesTruncated bool `json:"statesTruncated,omitempty"`
}

type TCPStateCount struct {
	State uint8  `json:"state"` // numeric MIB_TCP_STATE, never an address
	Count uint32 `json:"count"`
}

type ASPNETGroup struct {
	RequestsQueued      *float64 `json:"requestsQueued,omitempty"`
	RequestsCurrent     *float64 `json:"requestsCurrent,omitempty"`
	RequestsRejected    *float64 `json:"requestsRejected,omitempty"`
	RequestWaitTimeMs   *float64 `json:"requestWaitTimeMs,omitempty"`
	ApplicationRestarts *float64 `json:"applicationRestarts,omitempty"`
}

type MemPressureGroup struct {
	HardFaultsPerSec  *float64 `json:"hardFaultsPerSec,omitempty"`
	PoolPagedBytes    *uint64  `json:"poolPagedBytes,omitempty"`
	PoolNonPagedBytes *uint64  `json:"poolNonPagedBytes,omitempty"`
	PageFileUsagePct  *float64 `json:"pageFileUsagePct,omitempty"`
	// PagesPerSec is all paging traffic (in plus out); PageFaultsPerSec counts
	// every fault, hard or soft. Standby, Modified and Free are the memory
	// lists: standby is the file cache Windows can hand back, modified waits to
	// be written, free is truly empty. Together they say whether low "available"
	// memory is harmless cache or real pressure.
	PagesPerSec      *float64 `json:"pagesPerSec,omitempty"`
	PageFaultsPerSec *float64 `json:"pageFaultsPerSec,omitempty"`
	StandbyBytes     *uint64  `json:"standbyBytes,omitempty"`
	ModifiedBytes    *uint64  `json:"modifiedBytes,omitempty"`
	FreeBytes        *uint64  `json:"freeBytes,omitempty"`
}

type VMGroup struct {
	StealPct     *float64 `json:"stealPct,omitempty"`
	BalloonBytes *uint64  `json:"balloonBytes,omitempty"`
}

type DumpsGroup struct {
	Dumps []DumpEntry `json:"dumps,omitempty"`
	// Truncated/Unreadable carry the scan's own coverage (W12-06): a capped or
	// partly unreadable scan is not a complete newest-first catalogue. Additive
	// and omitted when the scan was complete, so old rows decode unchanged.
	Truncated  bool `json:"truncated,omitempty"`
	Unreadable int  `json:"unreadable,omitempty"`
}

type DumpEntry struct {
	Name   string    `json:"name"` // file name only, never content
	Size   int64     `json:"size"`
	MTime  time.Time `json:"mtime"`
	Target string    `json:"target,omitempty"`
}
