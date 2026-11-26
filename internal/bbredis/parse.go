package bbredis

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Everything here turns a server answer into numbers and fixed names. Nothing that can hold a key, a value, an
// address or a password is kept: INFO is read through a fixed list of field names, SLOWLOG keeps the command name,
// CLIENT LIST is only counted.

const maxInfoLines = 4000

// ParseInfo reads an INFO answer into field -> text. Section headers and blank lines are skipped.
func ParseInfo(text string) map[string]string {
	out := map[string]string{}
	n := 0
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" || line[0] == '#' {
			continue
		}
		if n++; n > maxInfoLines {
			break
		}
		if i := strings.IndexByte(line, ':'); i > 0 {
			out[line[:i]] = line[i+1:]
		}
	}
	return out
}

// InfoNumbers are the INFO fields the recorder keeps as numbers. The list is fixed here; a new field is a code change.
var InfoNumbers = []string{
	"uptime_in_seconds", "connected_clients", "blocked_clients", "maxclients",
	"used_memory", "used_memory_rss", "used_memory_peak", "maxmemory", "mem_fragmentation_ratio",
	"total_connections_received", "total_commands_processed", "instantaneous_ops_per_sec",
	"total_net_input_bytes", "total_net_output_bytes", "rejected_connections",
	"expired_keys", "evicted_keys", "keyspace_hits", "keyspace_misses", "latest_fork_usec",
	"rdb_changes_since_last_save", "rdb_bgsave_in_progress", "aof_enabled", "aof_rewrite_in_progress", "loading",
	"connected_slaves", "master_link_down_since_seconds", "master_repl_offset", "used_cpu_sys", "used_cpu_user",
}

// InfoWords are the INFO fields kept as short fixed words.
var InfoWords = []string{
	"redis_version", "role", "rdb_last_bgsave_status", "aof_last_write_status", "aof_last_bgrewrite_status", "master_link_status",
}

var wordRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,32}$`)

// Numbers picks the fixed numeric fields that are present and parse.
func Numbers(info map[string]string) map[string]float64 {
	out := map[string]float64{}
	for _, k := range InfoNumbers {
		if v, ok := info[k]; ok {
			if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
				out[k] = f
			}
		}
	}
	return out
}

// Words picks the fixed word fields that are present and look like a word.
func Words(info map[string]string) map[string]string {
	out := map[string]string{}
	for _, k := range InfoWords {
		if v, ok := info[k]; ok {
			v = strings.TrimSpace(v)
			if wordRe.MatchString(v) {
				out[k] = v
			}
		}
	}
	return out
}

// Keyspace sums the keys and the keys with an expiry over all databases ("db0:keys=3,expires=1,avg_ttl=0").
func Keyspace(info map[string]string) (keys, expires int64) {
	for k, v := range info {
		if !strings.HasPrefix(k, "db") {
			continue
		}
		if _, err := strconv.Atoi(k[2:]); err != nil {
			continue
		}
		for _, part := range strings.Split(v, ",") {
			kv := strings.SplitN(part, "=", 2)
			if len(kv) != 2 {
				continue
			}
			n, err := strconv.ParseInt(kv[1], 10, 64)
			if err != nil {
				continue
			}
			switch kv[0] {
			case "keys":
				keys += n
			case "expires":
				expires += n
			}
		}
	}
	return keys, expires
}

// CommandStat is the count and the time of one command name.
type CommandStat struct {
	Name  string `json:"name"`
	Calls int64  `json:"calls"`
	Usec  int64  `json:"usec"`
}

// MaxCommandStats keeps the commands that took the most time; the rest are dropped.
const MaxCommandStats = 32

// CommandStats reads "cmdstat_get:calls=10,usec=50,usec_per_call=5.0" lines, keeping the top MaxCommandStats by time.
func CommandStats(info map[string]string) []CommandStat {
	var out []CommandStat
	for k, v := range info {
		if !strings.HasPrefix(k, "cmdstat_") {
			continue
		}
		name := k[len("cmdstat_"):]
		if !commandNameRe.MatchString(name) {
			continue
		}
		cs := CommandStat{Name: strings.ToLower(name)}
		for _, part := range strings.Split(v, ",") {
			kv := strings.SplitN(part, "=", 2)
			if len(kv) != 2 {
				continue
			}
			n, err := strconv.ParseInt(kv[1], 10, 64)
			if err != nil {
				continue
			}
			switch kv[0] {
			case "calls":
				cs.Calls = n
			case "usec":
				cs.Usec = n
			}
		}
		out = append(out, cs)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Usec != out[j].Usec {
			return out[i].Usec > out[j].Usec
		}
		return out[i].Name < out[j].Name
	})
	if len(out) > MaxCommandStats {
		out = out[:MaxCommandStats]
	}
	return out
}

var commandNameRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._|-]{0,31}$`)

// SlowEntry is one slow command: when, how long, and the command name. Arg is the first argument, only when the owner
// asked for it and only for commands whose first argument is a key.
type SlowEntry struct {
	ID   int64  `json:"id"`
	Unix int64  `json:"t"`
	Usec int64  `json:"usec"`
	Cmd  string `json:"cmd"`
	Arg  string `json:"arg,omitempty"`
}

// maxArgChars bounds the first argument that may be kept.
const maxArgChars = 64

// keyFirst are the commands whose first argument is a key name (not a script, a password or a value). Only these may
// keep an argument, and only when the owner turned that on.
var keyFirst = map[string]bool{
	"get": true, "set": true, "setex": true, "del": true, "exists": true, "expire": true, "ttl": true, "incr": true, "decr": true,
	"mget": true, "hget": true, "hset": true, "hgetall": true, "hdel": true, "hmget": true, "lpush": true, "rpush": true,
	"lrange": true, "llen": true, "sadd": true, "smembers": true, "srem": true, "zadd": true, "zrange": true, "zrem": true,
}

// SlowEntries decodes SLOWLOG GET. Entries are [id, unix time, microseconds, [args...], client addr, client name]; the
// address and the name are never read. Only entries with id > afterID are returned, oldest first.
func SlowEntries(r Reply, afterID int64, keepFirstArg bool) []SlowEntry {
	var out []SlowEntry
	for _, e := range r.Arr {
		if e.Kind != '*' || len(e.Arr) < 4 {
			continue
		}
		id, ts, usec := e.Arr[0].Int, e.Arr[1].Int, e.Arr[2].Int
		if id <= afterID || e.Arr[3].Kind != '*' || len(e.Arr[3].Arr) == 0 {
			continue
		}
		args := e.Arr[3].Arr
		name := strings.ToLower(args[0].Str)
		if !commandNameRe.MatchString(name) {
			name = "other"
		}
		se := SlowEntry{ID: id, Unix: ts, Usec: usec, Cmd: name}
		if keepFirstArg && keyFirst[name] && len(args) > 1 {
			a := args[1].Str
			if len(a) > maxArgChars {
				a = a[:maxArgChars]
			}
			se.Arg = strings.Map(func(c rune) rune {
				if c < 0x20 || c == 0x7f {
					return -1
				}
				return c
			}, a)
		}
		out = append(out, se)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// LatencyEntry is one latency event: its name, when the latest spike was, and the latest and the largest in milliseconds.
type LatencyEntry struct {
	Event string `json:"event"`
	Unix  int64  `json:"t"`
	Last  int64  `json:"ms"`
	Max   int64  `json:"maxMs"`
}

// LatencyEntries decodes LATENCY LATEST: [event, unix time, latest ms, max ms] per event.
func LatencyEntries(r Reply) []LatencyEntry {
	var out []LatencyEntry
	for _, e := range r.Arr {
		if e.Kind != '*' || len(e.Arr) < 4 || len(out) >= 32 {
			continue
		}
		ev := e.Arr[0].Str
		if !wordRe.MatchString(ev) {
			continue
		}
		out = append(out, LatencyEntry{Event: ev, Unix: e.Arr[1].Int, Last: e.Arr[2].Int, Max: e.Arr[3].Int})
	}
	return out
}

// ClientCounts is CLIENT LIST reduced to counts. No address, name or command is kept.
type ClientCounts struct {
	Total    int `json:"total"`
	Blocked  int `json:"blocked"`  // flag b
	Replicas int `json:"replicas"` // flag S
	Idle10s  int `json:"idle10s"`  // idle under 10 seconds
	Idle60s  int `json:"idle60s"`  // 10 to 59 seconds
	Idle5m   int `json:"idle5m"`   // 60 to 299 seconds
	IdleMore int `json:"idleMore"` // 300 seconds or more
}

const maxClientLines = 20000

// CountClients reads CLIENT LIST text ("id=3 addr=... flags=N idle=0 ..."), keeping only the flags and the idle time.
func CountClients(text string) ClientCounts {
	var c ClientCounts
	for i, line := range strings.Split(text, "\n") {
		if i >= maxClientLines {
			break
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		c.Total++
		idle := int64(-1)
		for _, f := range strings.Fields(line) {
			switch {
			case strings.HasPrefix(f, "flags="):
				flags := f[len("flags="):]
				if strings.Contains(flags, "b") {
					c.Blocked++
				}
				if strings.Contains(flags, "S") {
					c.Replicas++
				}
			case strings.HasPrefix(f, "idle="):
				if n, err := strconv.ParseInt(f[len("idle="):], 10, 64); err == nil {
					idle = n
				}
			}
		}
		switch {
		case idle < 0:
		case idle < 10:
			c.Idle10s++
		case idle < 60:
			c.Idle60s++
		case idle < 300:
			c.Idle5m++
		default:
			c.IdleMore++
		}
	}
	return c
}
