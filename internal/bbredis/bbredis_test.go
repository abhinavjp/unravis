package bbredis

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAllowedIsAnAllowlist(t *testing.T) {
	ok := [][]string{
		{"PING"}, {"ping"},
		{"INFO"}, {"INFO", "server"}, {"info", "Memory", "stats", "commandstats"},
		{"SLOWLOG", "GET"}, {"slowlog", "get", "32"}, {"SLOWLOG", "GET", "128"},
		{"LATENCY", "LATEST"}, {"CLIENT", "LIST"},
	}
	for _, a := range ok {
		if err := Allowed(a); err != nil {
			t.Errorf("%v refused: %v", a, err)
		}
	}
	bad := [][]string{
		nil, {}, {""},
		{"GET", "key"}, {"SET", "k", "v"}, {"KEYS", "*"}, {"SCAN", "0"}, {"FLUSHALL"}, {"FLUSHDB"}, {"DEL", "k"},
		{"AUTH", "secret"}, {"HELLO", "3"}, {"EVAL", "return 1", "0"}, {"SHUTDOWN"}, {"DEBUG", "SLEEP", "5"},
		{"MONITOR"}, {"SUBSCRIBE", "x"}, {"SAVE"}, {"BGSAVE"}, {"REPLICAOF", "h", "1"}, {"MIGRATE", "h", "1"}, {"ACL", "LIST"},
		{"PING", "extra"},
		{"INFO", "all"}, {"INFO", "everything"}, {"INFO", "default"}, {"INFO", "server\r\nFLUSHALL"},
		{"SLOWLOG"}, {"SLOWLOG", "RESET"}, {"SLOWLOG", "LEN"}, {"SLOWLOG", "GET", "0"}, {"SLOWLOG", "GET", "129"}, {"SLOWLOG", "GET", "-1"}, {"SLOWLOG", "GET", "x"}, {"SLOWLOG", "GET", "1", "2"},
		{"LATENCY"}, {"LATENCY", "RESET"}, {"LATENCY", "HISTORY", "command"}, {"LATENCY", "LATEST", "x"},
		{"CLIENT"}, {"CLIENT", "KILL", "ID", "3"}, {"CLIENT", "SETNAME", "x"}, {"CLIENT", "LIST", "TYPE", "pubsub"}, {"CLIENT", "PAUSE", "1000"},
		{"CONFIG", "GET", "maxmemory"}, {"CONFIG", "GET", "dir"}, {"CONFIG"}, {"CONFIG", "SET", "maxmemory", "1"}, {"CONFIG", "GET"}, {"CONFIG", "GET", "*"}, {"CONFIG", "GET", "requirepass"},
		{"CONFIG", "GET", "masterauth"}, {"CONFIG", "GET", "maxmemory", "dir"}, {"CONFIG", "REWRITE"}, {"CONFIG", "RESETSTAT"},
		{"PING\r\nFLUSHALL"}, {"INFO", "server\x00"},
	}
	for _, a := range bad {
		if err := Allowed(a); !errors.Is(err, ErrNotAllowed) {
			t.Errorf("%q was not refused (err=%v)", a, err)
		}
	}
}

// fakeServer answers each command with scripted bytes and records what it was sent.
type fakeServer struct {
	ln   net.Listener
	got  chan []string
	resp func(args []string) string
}

func newFakeServer(t *testing.T, resp func(args []string) string) *fakeServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeServer{ln: ln, got: make(chan []string, 64), resp: resp}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(conn)
		}
	}()
	return s
}

func (s *fakeServer) port() int { return s.ln.Addr().(*net.TCPAddr).Port }

func (s *fakeServer) serve(c net.Conn) {
	defer func() { _ = c.Close() }()
	r := bufio.NewReader(c)
	for {
		line, err := r.ReadString('\n')
		if err != nil || !strings.HasPrefix(line, "*") {
			return
		}
		n, _ := strconv.Atoi(strings.TrimSpace(line[1:]))
		args := make([]string, 0, n)
		for i := 0; i < n; i++ {
			l, err := r.ReadString('\n')
			if err != nil {
				return
			}
			sz, _ := strconv.Atoi(strings.TrimSpace(l[1:]))
			buf := make([]byte, sz+2)
			if _, err := io.ReadFull(r, buf); err != nil {
				return
			}
			args = append(args, string(buf[:sz]))
		}
		s.got <- args
		_, _ = io.WriteString(c, s.resp(args))
	}
}

func bulk(s string) string { return fmt.Sprintf("$%d\r\n%s\r\n", len(s), s) }

func dial(t *testing.T, s *fakeServer) *Client {
	t.Helper()
	c, err := Dial(s.port(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func TestClientSendsOnlyAllowedCommandsAndNothingGoesOutForARefusedOne(t *testing.T) {
	s := newFakeServer(t, func([]string) string { return "+PONG\r\n" })
	c := dial(t, s)
	if r, err := c.Do("PING"); err != nil || r.Str != "PONG" {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := c.Do("FLUSHALL"); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("FLUSHALL: %v", err)
	}
	if _, err := c.Do("AUTH", "x"); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("AUTH through Do: %v", err)
	}
	if got := <-s.got; len(got) != 1 || got[0] != "PING" {
		t.Fatalf("server saw %v", got)
	}
	select {
	case extra := <-s.got:
		t.Fatalf("a refused command reached the server: %v", extra)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestDialOnlyReachesThisPC(t *testing.T) {
	if _, err := Dial(0, time.Second); !errors.Is(err, ErrNotLocal) {
		t.Errorf("port 0: %v", err)
	}
	if _, err := Dial(70000, time.Second); !errors.Is(err, ErrNotLocal) {
		t.Errorf("port 70000: %v", err)
	}
	// There is no host argument: the address is built from the port alone (see Dial).
}

func TestAuthSendsThePasswordAndErrorsNeverCarryIt(t *testing.T) {
	const secret = "s3cr3t-pass"
	s := newFakeServer(t, func(a []string) string {
		if strings.EqualFold(a[0], "AUTH") {
			if a[1] == secret {
				return "+OK\r\n"
			}
			return "-WRONGPASS invalid username-password pair " + a[1] + "\r\n"
		}
		return "-NOAUTH Authentication required.\r\n"
	})
	c := dial(t, s)
	if _, err := c.Do("INFO", "server"); !IsNoAuth(err) {
		t.Fatalf("want NOAUTH, got %v", err)
	}
	err := c.Auth("wrong-" + secret)
	if !IsNoAuth(err) {
		t.Fatalf("want a refused password, got %v", err)
	}
	if strings.Contains(err.Error(), "wrong") || strings.Contains(err.Error(), secret) {
		t.Errorf("the error carries the password: %v", err)
	}
	if err := c.Auth(secret); err != nil {
		t.Fatalf("good password: %v", err)
	}
}

func TestReplyLimitsAndBadFraming(t *testing.T) {
	cases := map[string]string{
		"bulk over the cap":  fmt.Sprintf("$%d\r\n", maxBulk+1),
		"array over the cap": fmt.Sprintf("*%d\r\n", maxArray+1),
		"too deep":           "*1\r\n*1\r\n*1\r\n*1\r\n*1\r\n*1\r\n:1\r\n",
		"garbage":            "?what\r\n",
		"short bulk end":     "$3\r\nabcXY",
		"bad integer":        ":x\r\n",
	}
	for name, resp := range cases {
		resp := resp
		s := newFakeServer(t, func([]string) string { return resp })
		c := dial(t, s)
		if _, err := c.Do("INFO", "server"); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestErrorWordOnlyKeepsTheFirstWord(t *testing.T) {
	s := newFakeServer(t, func([]string) string { return "-ERR unknown key mykey-with-private-data\r\n" })
	c := dial(t, s)
	_, err := c.Do("INFO", "server")
	var re *RedisError
	if !errors.As(err, &re) || re.Word != "ERR" || strings.Contains(err.Error(), "private") {
		t.Fatalf("%v", err)
	}
}

func TestParseInfoKeepsOnlyTheFixedFields(t *testing.T) {
	text := "# Server\r\nredis_version:3.0.504\r\nrole:master\r\nrequirepass:hunter2\r\nconnected_clients:12\r\nused_memory:1048576\r\n" +
		"instantaneous_ops_per_sec:99\r\nmem_fragmentation_ratio:1.25\r\nbad:\r\nrdb_last_bgsave_status:ok\r\n" +
		"os:Windows 10 with a very long and odd text that is not a word\r\n" +
		"# Keyspace\r\ndb0:keys=10,expires=4,avg_ttl=0\r\ndb1:keys=5,expires=0,avg_ttl=0\r\n" +
		"# Commandstats\r\ncmdstat_get:calls=100,usec=500,usec_per_call=5.00\r\ncmdstat_set:calls=10,usec=900,usec_per_call=90.00\r\ncmdstat_Bad Name:calls=1,usec=1\r\n"
	info := ParseInfo(text)
	n := Numbers(info)
	if n["connected_clients"] != 12 || n["used_memory"] != 1048576 || n["mem_fragmentation_ratio"] != 1.25 || n["instantaneous_ops_per_sec"] != 99 {
		t.Errorf("numbers %v", n)
	}
	if _, ok := n["requirepass"]; ok {
		t.Error("requirepass came out")
	}
	w := Words(info)
	if w["redis_version"] != "3.0.504" || w["role"] != "master" || w["rdb_last_bgsave_status"] != "ok" {
		t.Errorf("words %v", w)
	}
	for k, v := range w {
		if strings.Contains(v, "hunter2") || k == "os" {
			t.Errorf("unexpected word %s=%s", k, v)
		}
	}
	if k, e := Keyspace(info); k != 15 || e != 4 {
		t.Errorf("keyspace %d/%d", k, e)
	}
	cs := CommandStats(info)
	if len(cs) != 2 || cs[0].Name != "set" || cs[0].Usec != 900 || cs[1].Name != "get" || cs[1].Calls != 100 {
		t.Errorf("commandstats %+v", cs)
	}
}

func TestCommandStatsAreCapped(t *testing.T) {
	info := map[string]string{}
	for i := 0; i < MaxCommandStats+30; i++ {
		info[fmt.Sprintf("cmdstat_c%03d", i)] = fmt.Sprintf("calls=1,usec=%d", i)
	}
	cs := CommandStats(info)
	if len(cs) != MaxCommandStats || cs[0].Usec != int64(MaxCommandStats+29) {
		t.Errorf("%d entries, first %+v", len(cs), cs[0])
	}
}

func slowReply(entries ...Reply) Reply { return Reply{Kind: '*', Arr: entries} }
func slowEntry(id, ts, usec int64, args ...string) Reply {
	a := Reply{Kind: '*'}
	for _, s := range args {
		a.Arr = append(a.Arr, Reply{Kind: '$', Str: s})
	}
	return Reply{Kind: '*', Arr: []Reply{{Kind: ':', Int: id}, {Kind: ':', Int: ts}, {Kind: ':', Int: usec}, a,
		{Kind: '$', Str: "10.1.2.3:5555"}, {Kind: '$', Str: "clientname-secret"}}}
}

func TestSlowEntriesKeepTheCommandNameOnly(t *testing.T) {
	r := slowReply(
		slowEntry(3, 1700000300, 20000, "GET", "user:42:session"),
		slowEntry(2, 1700000200, 15000, "EVAL", "return redis.call('get','x')", "0"),
		slowEntry(1, 1700000100, 12000, "AUTH", "hunter2"),
		slowEntry(4, 1700000400, 9000, "bad cmd!", "x"),
	)
	got := SlowEntries(r, 0, false)
	if len(got) != 4 || got[0].ID != 1 || got[3].ID != 4 {
		t.Fatalf("%+v", got)
	}
	b, _ := fmt.Sprintf("%+v", got), 0
	for _, leak := range []string{"user:42", "return redis", "hunter2", "10.1.2.3", "clientname", "secret"} {
		if strings.Contains(b, leak) {
			t.Errorf("%q leaked: %s", leak, b)
		}
	}
	if got[3].Cmd != "other" {
		t.Errorf("an odd command name should be other, got %q", got[3].Cmd)
	}
	if after := SlowEntries(r, 2, false); len(after) != 2 || after[0].ID != 3 {
		t.Errorf("cursor: %+v", after)
	}
}

func TestSlowFirstArgumentOnlyForKeyCommandsAndOnlyWhenAsked(t *testing.T) {
	r := slowReply(
		slowEntry(1, 1, 1, "GET", "user:42"),
		slowEntry(2, 1, 1, "EVAL", "return 1"),
		slowEntry(3, 1, 1, "AUTH", "hunter2"),
		slowEntry(4, 1, 1, "CONFIG", "SET", "requirepass", "x"),
		slowEntry(5, 1, 1, "GET", strings.Repeat("k", 200)+"\r\n"),
	)
	got := SlowEntries(r, 0, true)
	byID := map[int64]SlowEntry{}
	for _, e := range got {
		byID[e.ID] = e
	}
	if byID[1].Arg != "user:42" {
		t.Errorf("GET key: %+v", byID[1])
	}
	for _, id := range []int64{2, 3, 4} {
		if byID[id].Arg != "" {
			t.Errorf("command %s kept an argument: %+v", byID[id].Cmd, byID[id])
		}
	}
	if len(byID[5].Arg) != maxArgChars {
		t.Errorf("long key kept %d chars", len(byID[5].Arg))
	}
	for _, e := range SlowEntries(r, 0, false) {
		if e.Arg != "" {
			t.Errorf("argument kept without the tick: %+v", e)
		}
	}
}

func TestLatencyAndClientCounts(t *testing.T) {
	lr := Reply{Kind: '*', Arr: []Reply{
		{Kind: '*', Arr: []Reply{{Kind: '$', Str: "command"}, {Kind: ':', Int: 1700000000}, {Kind: ':', Int: 12}, {Kind: ':', Int: 40}}},
		{Kind: '*', Arr: []Reply{{Kind: '$', Str: "bad event!"}, {Kind: ':', Int: 1}, {Kind: ':', Int: 1}, {Kind: ':', Int: 1}}},
	}}
	if l := LatencyEntries(lr); len(l) != 1 || l[0].Event != "command" || l[0].Last != 12 || l[0].Max != 40 {
		t.Errorf("%+v", l)
	}
	list := "id=3 addr=10.0.0.1:4000 fd=8 name=app-secret age=5 idle=0 flags=N db=0 cmd=get\n" +
		"id=4 addr=10.0.0.2:4001 fd=9 age=500 idle=30 flags=b db=0 cmd=blpop\n" +
		"id=5 addr=10.0.0.3:4002 fd=10 age=900 idle=120 flags=S db=0 cmd=psync\n" +
		"id=6 addr=10.0.0.4:4003 fd=11 age=9000 idle=4000 flags=N db=0 cmd=ping\n\n"
	c := CountClients(list)
	want := ClientCounts{Total: 4, Blocked: 1, Replicas: 1, Idle10s: 1, Idle60s: 1, Idle5m: 1, IdleMore: 1}
	if c != want {
		t.Errorf("%+v, want %+v", c, want)
	}
}

type xorProtector struct{}

func (xorProtector) Protect(b []byte) ([]byte, error)   { return xor(b), nil }
func (xorProtector) Unprotect(b []byte) ([]byte, error) { return xor(b), nil }
func xor(b []byte) []byte {
	out := make([]byte, len(b))
	for i, c := range b {
		out[i] = c ^ 0x5a
	}
	return out
}

func TestPasswordStoreRoundTripAndNeverPlain(t *testing.T) {
	dir := t.TempDir()
	if _, found, err := LoadPassword(dir, xorProtector{}); found || err != nil {
		t.Fatalf("empty store: found=%v err=%v", found, err)
	}
	if err := SavePassword(dir, "hunter2", xorProtector{}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, PasswordFile)) //nolint:gosec // test file
	if bytes.Contains(raw, []byte("hunter2")) {
		t.Error("the password is stored in plain text")
	}
	pw, found, err := LoadPassword(dir, xorProtector{})
	if err != nil || !found || pw != "hunter2" {
		t.Fatalf("%q %v %v", pw, found, err)
	}
	if !HasPassword(dir) {
		t.Error("HasPassword")
	}
	if err := RemovePassword(dir); err != nil || HasPassword(dir) {
		t.Errorf("remove: %v", err)
	}
	if err := RemovePassword(dir); err != nil {
		t.Errorf("removing twice: %v", err)
	}
}

func TestPasswordStoreRefusals(t *testing.T) {
	dir := t.TempDir()
	for _, bad := range []string{"", strings.Repeat("a", 257), "line\nbreak", "tab\t", "nul\x00"} {
		if err := SavePassword(dir, bad, xorProtector{}); !errors.Is(err, ErrPassword) {
			t.Errorf("%q was accepted", bad)
		}
	}
	if err := SavePassword(filepath.Join(dir, "missing"), "x", xorProtector{}); !errors.Is(err, ErrPassword) {
		t.Error("a folder that does not exist was created or accepted")
	}
	if err := SavePassword(dir, "x", nil); !errors.Is(err, ErrPassword) {
		t.Error("no protector was accepted")
	}
	// A file that is there but unreadable is an error, not "no password".
	if err := os.WriteFile(filepath.Join(dir, PasswordFile), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, found, err := LoadPassword(dir, xorProtector{}); !found || !errors.Is(err, ErrPassword) {
		t.Errorf("garbage: found=%v err=%v", found, err)
	}
	if err := os.WriteFile(filepath.Join(dir, PasswordFile), make([]byte, maxPasswordFile+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, found, err := LoadPassword(dir, xorProtector{}); !found || !errors.Is(err, ErrPassword) {
		t.Errorf("oversize: found=%v err=%v", found, err)
	}
}

func TestWholeAnswerLimits(t *testing.T) {
	big := strings.Repeat("x", 1<<20)
	manyBulks := "*20\r\n" + strings.Repeat(bulk(big), 20) // 20 MB: each item is inside its cap, the sum is not
	manyItems := "*10000\r\n" + strings.Repeat("*5\r\n:1\r\n:1\r\n:1\r\n:1\r\n:1\r\n", 10000)
	for name, resp := range map[string]string{"bytes": manyBulks, "items": manyItems} {
		resp := resp
		s := newFakeServer(t, func([]string) string { return resp })
		c := dial(t, s)
		if _, err := c.Do("INFO", "server"); !errors.Is(err, ErrTooLarge) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A normal answer is far inside the limits.
	s := newFakeServer(t, func([]string) string { return bulk(big) })
	if r, err := dial(t, s).Do("INFO", "server"); err != nil || len(r.Str) != len(big) {
		t.Errorf("a 1 MB answer: %v", err)
	}
}

func TestOverallDeadlineStopsACommandBeforeItIsSent(t *testing.T) {
	s := newFakeServer(t, func([]string) string { return "+PONG\r\n" })
	c := dial(t, s)
	c.SetOverall(time.Now().Add(-time.Millisecond))
	if _, err := c.Do("PING"); err == nil {
		t.Fatal("a command ran past the overall deadline")
	}
	select {
	case got := <-s.got:
		t.Fatalf("the server was sent %v", got)
	case <-time.After(50 * time.Millisecond):
	}
	c.SetOverall(time.Time{})
	if _, err := c.Do("PING"); err != nil {
		t.Fatal(err)
	}
}

func TestErrorWordIsPlainCapitalsOrUnknown(t *testing.T) {
	for in, want := range map[string]string{"-err x\r\n": "ERR", "-NOPERM no\r\n": "NOPERM", "-\x1b[31mred x\r\n": "UNKNOWN", "-é x\r\n": "UNKNOWN", "-" + strings.Repeat("A", 40) + "\r\n": "UNKNOWN"} {
		in := in
		s := newFakeServer(t, func([]string) string { return in })
		_, err := dial(t, s).Do("INFO", "server")
		var re *RedisError
		if !errors.As(err, &re) || re.Word != want {
			t.Errorf("%q: %v want %s", in, err, want)
		}
	}
	if IsNoAuth(&RedisError{Word: "NOPERM"}) || !IsNoPerm(&RedisError{Word: "NOPERM"}) {
		t.Error("NOPERM is not a missing password")
	}
}

func TestSlowEntryZeroIsReportedWithTheStartCursor(t *testing.T) {
	r := slowReply(slowEntry(0, 1, 1, "GET", "k"))
	if got := SlowEntries(r, -1, false); len(got) != 1 || got[0].ID != 0 {
		t.Errorf("%+v", got)
	}
}

func TestPathUnder(t *testing.T) {
	for file, want := range map[string]bool{
		`C:\Program Files\Redis\redis-server.exe`: true, // winok: string compare only, no filesystem
		`c:\program files\Memurai\memurai.exe`:    true,
		`C:/Program Files/Redis/x.exe`:            true,
		`C:\Program Files (x86)\Redis\x.exe`:      false, // winok: string compare only, no filesystem
		`C:\Users\bob\Program Files\x.exe`:        false, // winok: string compare only, no filesystem
		`C:\Program Files\..\Users\bob\x.exe`:     false, // winok: string compare only, no filesystem
		`C:\Program FilesEvil\x.exe`:              false, // winok: string compare only, no filesystem
		`C:\Program Files`:                        false, // winok: string compare only, no filesystem
	} {
		if got := pathUnder(file, `C:\Program Files`); got != want { // winok: string compare only, no filesystem
			t.Errorf("%s: %v", file, got)
		}
	}
	if pathUnder(`C:\x.exe`, "") {
		t.Error("an empty folder matched")
	}
}
