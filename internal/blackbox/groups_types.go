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
	Pool           string   `json:"pool"`
	PID            uint32   `json:"pid"`
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
}

type EventEntry struct {
	Channel uint8     `json:"channel"` // fixed enum: 1=System, 2=Application
	EventID uint32    `json:"eventId"`
	Level   uint8     `json:"level"`
	Count   uint32    `json:"count"`
	T       time.Time `json:"t"`
}

type TCPGroup struct {
	RetransmitsPerSec *float64        `json:"retransmitsPerSec,omitempty"`
	ResetsPerSec      *float64        `json:"resetsPerSec,omitempty"`
	OutboundQueue     *float64        `json:"outboundQueue,omitempty"`
	States            []TCPStateCount `json:"states,omitempty"`
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
}

type VMGroup struct {
	StealPct     *float64 `json:"stealPct,omitempty"`
	BalloonBytes *uint64  `json:"balloonBytes,omitempty"`
}

type DumpsGroup struct {
	Dumps []DumpEntry `json:"dumps,omitempty"`
}

type DumpEntry struct {
	Name   string    `json:"name"` // file name only, never content
	Size   int64     `json:"size"`
	MTime  time.Time `json:"mtime"`
	Target string    `json:"target,omitempty"`
}
