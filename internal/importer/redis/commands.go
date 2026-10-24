// Package redis imports owner-run redis-cli exports: SLOWLOG GET, LATENCY HISTORY and
// INFO loops (wave 2 card W2-h). The owner runs read-only commands and sends the output
// file; the tool never connects to Redis and holds no credentials.
//
// Three importers share this package: redis-slowlog, redis-latency and redis-info. The
// first two read the same reply formats (see resp.go): the redis-cli --no-raw tree text,
// --json (redis-cli 7 and later) and a RESP2 dump. --raw output is refused, because
// argument boundaries cannot be recovered from it.
//
// Privacy (wave plan section 2.2): keys, values, command arguments and client addresses
// are dropped while parsing and never stored, logged or put in an error. A SLOWLOG entry
// is reduced to a command name from the fixed table in this file (anything else is
// "other"), the argument count and a pseudonym of the client name. Errors carry a
// reason and a line number, never file content. The package makes no network calls.
package redis

import "strings"

// Names of the three importers, and the event kinds they emit. The kinds, attribute names
// and enum values in this file are a subset of the export policy in internal/evidence
// (caseexport.go and caseexport_enums.go); a test keeps the two in step.
const (
	NameSlowlog = "redis-slowlog"
	NameLatency = "redis-latency"
	NameInfo    = "redis-info"

	KindSlowCommand  = "slow-command"
	KindLatencySpike = "latency-spike"
	KindMetric       = "metric"
	KindRedisStart   = "redis-start"
)

// Attribute names.
const (
	attrCommand = "command"
	attrArgc    = "argc"
	attrClient  = "client"
	attrID      = "id"
	attrEvent   = "event"
	attrMetric  = "metric"
	attrRole    = "role"
)

// Other is the value for a command or latency event outside the fixed tables.
const Other = "other"

// CommandNames is the fixed Redis command table, in the spelling the export policy uses
// (redisCommands in internal/evidence/caseexport_enums.go). This file owns it: W2-i
// (redis.log) reads CommandNames and CanonicalCommand and does not extend them. A name
// added here must be added to the export enum too, or the export shows it as "[removed]".
var CommandNames = []string{
	"GET", "SET", "MGET", "MSET", "MSETNX", "SETEX", "SETNX", "PSETEX", "GETSET", "GETDEL", "GETEX", "APPEND",
	"STRLEN", "SETRANGE", "GETRANGE", "INCR", "INCRBY", "INCRBYFLOAT", "DECR", "DECRBY", "SETBIT", "GETBIT",
	"BITCOUNT", "BITOP", "BITPOS", "BITFIELD", "DEL", "UNLINK", "EXISTS", "EXPIRE", "PEXPIRE", "EXPIREAT",
	"PEXPIREAT", "EXPIRETIME", "TTL", "PTTL", "PERSIST", "TYPE", "KEYS", "SCAN", "RENAME", "RENAMENX", "DUMP",
	"RESTORE", "OBJECT", "TOUCH", "RANDOMKEY", "COPY", "MOVE", "MIGRATE", "SORT", "SORT_RO",
	"HGET", "HSET", "HMGET", "HMSET", "HGETALL", "HDEL", "HEXISTS", "HINCRBY", "HINCRBYFLOAT", "HKEYS", "HVALS",
	"HLEN", "HSCAN", "HSETNX", "HSTRLEN", "HRANDFIELD",
	"LPUSH", "RPUSH", "LPUSHX", "RPUSHX", "LPOP", "RPOP", "LRANGE", "LLEN", "LINDEX", "LSET", "LREM", "LTRIM",
	"LINSERT", "LPOS", "LMOVE", "BLMOVE", "BLPOP", "BRPOP", "BRPOPLPUSH", "RPOPLPUSH", "LMPOP", "BLMPOP",
	"SADD", "SREM", "SMEMBERS", "SISMEMBER", "SMISMEMBER", "SCARD", "SPOP", "SRANDMEMBER", "SINTER", "SINTERCARD",
	"SUNION", "SDIFF", "SINTERSTORE", "SUNIONSTORE", "SDIFFSTORE", "SSCAN", "SMOVE",
	"ZADD", "ZREM", "ZRANGE", "ZREVRANGE", "ZRANGEBYSCORE", "ZREVRANGEBYSCORE", "ZRANGEBYLEX", "ZREVRANGEBYLEX",
	"ZRANGESTORE", "ZRANK", "ZREVRANK", "ZSCORE", "ZMSCORE", "ZCARD", "ZCOUNT", "ZLEXCOUNT", "ZINCRBY",
	"ZREMRANGEBYSCORE", "ZREMRANGEBYRANK", "ZREMRANGEBYLEX", "ZUNIONSTORE", "ZINTERSTORE", "ZDIFFSTORE", "ZUNION",
	"ZINTER", "ZDIFF", "ZSCAN", "ZPOPMIN", "ZPOPMAX", "BZPOPMIN", "BZPOPMAX", "ZMPOP", "BZMPOP", "ZRANDMEMBER",
	"XADD", "XREAD", "XREADGROUP", "XRANGE", "XREVRANGE", "XLEN", "XACK", "XDEL", "XTRIM", "XGROUP", "XINFO",
	"XPENDING", "XCLAIM", "XAUTOCLAIM", "XSETID",
	"GEOADD", "GEODIST", "GEOHASH", "GEOPOS", "GEORADIUS", "GEORADIUSBYMEMBER", "GEOSEARCH", "GEOSEARCHSTORE",
	"PFADD", "PFCOUNT", "PFMERGE",
	"PUBLISH", "SUBSCRIBE", "UNSUBSCRIBE", "PSUBSCRIBE", "PUNSUBSCRIBE", "PUBSUB", "SPUBLISH", "SSUBSCRIBE",
	"SUNSUBSCRIBE", "MULTI", "EXEC", "DISCARD", "WATCH", "UNWATCH",
	"EVAL", "EVALSHA", "EVAL_RO", "EVALSHA_RO", "SCRIPT", "FCALL", "FCALL_RO", "FUNCTION",
	"PING", "ECHO", "AUTH", "HELLO", "SELECT", "QUIT", "RESET", "CLIENT", "INFO", "CONFIG", "DBSIZE", "FLUSHDB",
	"FLUSHALL", "SAVE", "BGSAVE", "BGREWRITEAOF", "LASTSAVE", "SHUTDOWN", "SLOWLOG", "LATENCY", "MEMORY",
	"MONITOR", "DEBUG", "COMMAND", "CLUSTER", "READONLY", "READWRITE", "REPLICAOF", "SLAVEOF", "ROLE", "SYNC",
	"PSYNC", "REPLCONF", "WAIT", "WAITAOF", "TIME", "LOLWUT", "MODULE", "ACL", "SWAPDB", "FAILOVER",
}

var commandSet = func() map[string]bool {
	m := make(map[string]bool, len(CommandNames))
	for _, c := range CommandNames {
		m[c] = true
	}
	return m
}()

// CanonicalCommand returns the table spelling of a command name read from a file (the
// match ignores ASCII case), or Other when the name is not in CommandNames. The result is
// always one of those values, never text from the file.
func CanonicalCommand(name string) string {
	if len(name) == 0 || len(name) > 32 {
		return Other
	}
	up := strings.ToUpper(name)
	if commandSet[up] {
		return up
	}
	return Other
}

// LatencyEvents is the documented LATENCY event list (redisLatencyEvents in the export
// policy) without Other.
var LatencyEvents = []string{
	"command", "fast-command", "fork", "aof-fsync-always", "aof-write", "aof-write-pending-fsync",
	"aof-write-active-child", "aof-write-alone", "aof-fstat", "aof-rename", "aof-rewrite-diff-write",
	"expire-cycle", "eviction-cycle", "eviction-del", "eviction-lazyfree", "active-defrag-cycle",
	"rdb-unlink-temp-file",
}

var latencyEventSet = func() map[string]bool {
	m := make(map[string]bool, len(LatencyEvents))
	for _, e := range LatencyEvents {
		m[e] = true
	}
	return m
}()

// canonicalLatencyEvent returns a name from LatencyEvents, or Other.
func canonicalLatencyEvent(name string) string {
	if latencyEventSet[name] {
		return name
	}
	return Other
}

// metricSpec describes one INFO field the importer keeps.
type metricSpec struct {
	key  string  // the INFO field name, also the exported metric name
	unit string  // Event.Unit
	div  float64 // the parsed number is divided by this (latest_fork_usec is read as ms)
	cpu  bool    // cumulative CPU seconds: emitted as percent-one-core between samples
	// max is the largest value accepted; a larger one is a bad number.
	max float64
}

const (
	maxCounter = 1e18
	maxUptime  = 4e9 // seconds; keeps time arithmetic inside int64 nanoseconds
)

// metricSpecs is the INFO allowlist in emission order (the order is part of the output).
// Every other field, including the Keyspace section, executable, config_file, run_id,
// master_host and os, is dropped unread.
var metricSpecs = []metricSpec{
	{key: "uptime_in_seconds", unit: "count", div: 1, max: maxUptime},
	{key: "used_memory", unit: "bytes", div: 1, max: maxCounter},
	{key: "used_memory_rss", unit: "bytes", div: 1, max: maxCounter},
	{key: "used_memory_peak", unit: "bytes", div: 1, max: maxCounter},
	{key: "maxmemory", unit: "bytes", div: 1, max: maxCounter},
	{key: "mem_fragmentation_ratio", unit: "ratio", div: 1, max: maxCounter},
	{key: "connected_clients", unit: "count", div: 1, max: maxCounter},
	{key: "blocked_clients", unit: "count", div: 1, max: maxCounter},
	{key: "instantaneous_ops_per_sec", unit: "per-sec", div: 1, max: maxCounter},
	{key: "rejected_connections", unit: "count", div: 1, max: maxCounter},
	{key: "evicted_keys", unit: "count", div: 1, max: maxCounter},
	{key: "expired_keys", unit: "count", div: 1, max: maxCounter},
	{key: "latest_fork_usec", unit: "ms", div: 1000, max: maxCounter},
	{key: "total_connections_received", unit: "count", div: 1, max: maxCounter},
	{key: "keyspace_hits", unit: "count", div: 1, max: maxCounter},
	{key: "keyspace_misses", unit: "count", div: 1, max: maxCounter},
	{key: "used_cpu_sys", unit: "percent-one-core", div: 1, cpu: true, max: maxCounter},
	{key: "used_cpu_user", unit: "percent-one-core", div: 1, cpu: true, max: maxCounter},
	{key: "rdb_bgsave_in_progress", unit: "count", div: 1, max: maxCounter},
	{key: "aof_rewrite_in_progress", unit: "count", div: 1, max: maxCounter},
}

// Indexes into metricSpecs that info.go refers to.
const (
	idxUptime = 0
	idxCPUSys = 16
	idxCPUUsr = 17
)

var metricIndex = func() map[string]int {
	m := make(map[string]int, len(metricSpecs))
	for i, s := range metricSpecs {
		m[s.key] = i
	}
	return m
}()

// roleOf maps an INFO role value to the export role. Redis still writes "slave"; the
// export spelling is "replica". Any other value has no role.
func roleOf(v string) string {
	switch v {
	case "master":
		return "master"
	case "slave", "replica":
		return "replica"
	case "sentinel":
		return "sentinel"
	}
	return ""
}
