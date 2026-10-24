package redis

import (
	"context"
	"fmt"
	"hash/crc32"
	"strings"
	"testing"

	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
	"api-audit-explorer/internal/importer/importertest"
)

// All files here are synthetic. The sentinel marks text that must never leave the parser.
const sentinel = "SENTINEL-7f3a"

// slowGood is SLOWLOG GET as redis-cli --no-raw prints it: two entries, newest first.
const slowGood = `1) 1) (integer) 14
   2) (integer) 1003971751
   3) (integer) 15230
   4) 1) "KEYS"
      2) "session:alice"
   5) "192.0.2.10:52144"
   6) "web-a-conn"
2) 1) (integer) 12
   2) (integer) 1003971740
   3) (integer) 1012000
   4) 1) "SORT"
      2) "list:bob"
      3) "BY"
      4) "weight_*"
   5) "198.51.100.7:50110"
   6) ""
`

// slowBad has the sentinel in a key, an address-side client name and a command name. The
// first entry has a time outside 2000..2100 and is rejected; the second is valid.
const slowBad = `1) 1) (integer) 14
   2) (integer) 99
   3) (integer) 15230
   4) 1) "KEYS"
      2) "session:SENTINEL-7f3a"
   5) "192.0.2.10:52144"
   6) "SENTINEL-7f3a-client"
2) 1) (integer) 12
   2) (integer) 1003971740
   3) (integer) 1012000
   4) 1) "SENTINEL-7f3a"
      2) "list:SENTINEL-7f3a"
   5) "198.51.100.7:50110"
   6) "SENTINEL-7f3a"
SENTINEL-7f3a not a reply line
`

const latGood = `# latency-event: fork
1) 1) (integer) 1003971745
   2) (integer) 230
2) 1) (integer) 1003971800
   2) (integer) 180
# latency-event: aof-fsync-always
1) 1) (integer) 1003971750
   2) (integer) 1180
`

const latBad = `# latency-event: SENTINEL-7f3a
1) 1) "SENTINEL-7f3a"
   2) (integer) 230
2) 1) (integer) 1003971800
   2) (integer) 180
3) 1) (integer) 1003971801
`

const infoGood = `# unravis-sample 2001-09-09T01:00:00Z
# Server
redis_version:7.2.4
executable:/usr/local/bin/redis-server
uptime_in_seconds:1000
# Clients
connected_clients:12
blocked_clients:0
# Memory
used_memory:1048576
used_memory_rss:2097152
mem_fragmentation_ratio:1.50
# Stats
instantaneous_ops_per_sec:850
rejected_connections:0
evicted_keys:0
latest_fork_usec:2300
# Replication
role:master
# CPU
used_cpu_sys:10.50
used_cpu_user:20.25
# Keyspace
db0:keys=5,expires=1,avg_ttl=0
# unravis-sample 2001-09-09T01:01:00Z
# Server
redis_version:7.2.4
uptime_in_seconds:30
# Clients
connected_clients:14
blocked_clients:1
# Memory
used_memory:1148576
used_memory_rss:2197152
mem_fragmentation_ratio:1.40
# Stats
instantaneous_ops_per_sec:900
rejected_connections:0
evicted_keys:3
latest_fork_usec:0
# Replication
role:slave
master_host:primary.example.test
# CPU
used_cpu_sys:16.50
used_cpu_user:32.25
`

const infoBad = `# unravis-sample 2001-09-09T01:00:00Z
# Server
redis_version:7.2.4
config_file:/etc/SENTINEL-7f3a.conf
uptime_in_seconds:1000
used_memory:SENTINEL-7f3a
role:SENTINEL-7f3a
SENTINEL-7f3a
# unravis-sample SENTINEL-7f3a
used_memory:1
`

// pseudo is a stand-in for the case-keyed pseudonym function.
func pseudo(kind, value string) string {
	return fmt.Sprintf("%s-%08x", kind, crc32.ChecksumIEEE([]byte(value)))
}

func hostOpts() importer.Options {
	return importer.Options{
		CaseID: "harnesscase00000000000000a", ImportNonce: "nonce-1", FileName: "redis.txt",
		Host:         &importer.HostHint{Label: "redis-a", Role: "redis"},
		Pseudonymize: pseudo,
	}
}

// treeEntry renders one SLOWLOG entry the way redis-cli right-aligns a long reply.
func treeEntry(w, idx int, id, ts, us int64, cmd, key, client string) string {
	outer := fmt.Sprintf("%*d) ", w, idx)
	pad := strings.Repeat(" ", len(outer))
	var b strings.Builder
	fmt.Fprintf(&b, "%s1) (integer) %d\n", outer, id)
	fmt.Fprintf(&b, "%s2) (integer) %d\n", pad, ts)
	fmt.Fprintf(&b, "%s3) (integer) %d\n", pad, us)
	fmt.Fprintf(&b, "%s4) 1) %q\n", pad, cmd)
	fmt.Fprintf(&b, "%s   2) %q\n", pad, key)
	fmt.Fprintf(&b, "%s5) \"192.0.2.10:%d\"\n", pad, 40000+idx%1000)
	fmt.Fprintf(&b, "%s6) %q\n", pad, client)
	return b.String()
}

// slowLarge is valid, has unique entries and is over 2*HeadSize.
func slowLarge() []byte {
	var b strings.Builder
	const n = 1500
	for i := 1; i <= n; i++ {
		b.WriteString(treeEntry(4, i, int64(n-i), 1003971000+int64(i), 1000+int64(i), "GET", fmt.Sprintf("user:%d", i), "web-a-conn"))
	}
	return []byte(b.String())
}

func latLarge() []byte {
	var b strings.Builder
	for blk := 0; blk < 20; blk++ {
		b.WriteString("# latency-event: fork\n")
		for i := 1; i <= 160; i++ {
			outer := fmt.Sprintf("%4d) ", i)
			fmt.Fprintf(&b, "%s1) (integer) %d\n%s2) (integer) %d\n", outer, 1003000000+blk*1000+i, strings.Repeat(" ", len(outer)), 100+i)
		}
	}
	return []byte(b.String())
}

func infoLarge() []byte {
	var b strings.Builder
	for i := 0; i < 600; i++ {
		fmt.Fprintf(&b, "# unravis-sample 2001-09-09T%02d:%02d:%02dZ\n", i/3600, i/60%60, i%60)
		b.WriteString("# Server\nredis_version:7.2.4\n")
		fmt.Fprintf(&b, "uptime_in_seconds:%d\nconnected_clients:%d\nused_memory:%d\n", 1000+i, 10+i%7, 1048576+i)
		b.WriteString("# Stats\ninstantaneous_ops_per_sec:850\nrejected_connections:0\nevicted_keys:0\nlatest_fork_usec:2300\n")
		b.WriteString("# Replication\nrole:master\n# CPU\n")
		fmt.Fprintf(&b, "used_cpu_sys:%d.50\nused_cpu_user:%d.25\n", 10+i, 20+i)
		b.WriteString("# Keyspace\ndb0:keys=5,expires=1,avg_ttl=0\n")
	}
	return []byte(b.String())
}

func slowFixtures() importertest.Fixtures {
	o := hostOpts()
	o.Supplied = map[string]string{slowThresholdK: "10000"}
	return importertest.Fixtures{
		Good: []byte(slowGood), Bad: []byte(slowBad), Large: slowLarge(),
		NoFreeText: true, Options: o,
	}
}

func latFixtures() importertest.Fixtures {
	o := hostOpts()
	o.Supplied = map[string]string{latThresholdK: "10"}
	return importertest.Fixtures{
		Good: []byte(latGood), Bad: []byte(latBad), Large: latLarge(),
		NoFreeText: true, Options: o,
	}
}

func infoFixtures() importertest.Fixtures {
	return importertest.Fixtures{
		Good: []byte(infoGood), Bad: []byte(infoBad), Large: infoLarge(),
		NoFreeText: true, Options: hostOpts(),
	}
}

func collect(t testing.TB, imp importer.Importer, data string, opts importer.Options) ([]evidence.Event, importer.Report, error) {
	t.Helper()
	return importer.Collect(context.Background(), imp, strings.NewReader(data), opts)
}

func mustCollect(t testing.TB, imp importer.Importer, data string, opts importer.Options) ([]evidence.Event, importer.Report) {
	t.Helper()
	evs, rep, err := collect(t, imp, data, opts)
	if err != nil {
		t.Fatalf("%s: %v", imp.Name(), err)
	}
	return evs, rep
}
