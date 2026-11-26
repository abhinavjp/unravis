package blackbox

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"api-audit-explorer/internal/bbredis"
	"api-audit-explorer/internal/sealedfile"
)

type redisFake struct {
	ln       net.Listener
	mu       sync.Mutex
	seen     [][]string
	password string // "" = no password needed
	authed   map[net.Conn]bool
	unknown  map[string]bool // commands answered with -ERR unknown command
	noperm   map[string]bool // commands answered with -NOPERM
	delay    time.Duration   // every answer after PING waits this long
}

func redisBulk(s string) string { return fmt.Sprintf("$%d\r\n%s\r\n", len(s), s) }

const fakeInfo = "# Server\r\nredis_version:3.0.504\r\nrole:master\r\nrequirepass:hunter2\r\nconnected_clients:7\r\nused_memory:2048\r\ninstantaneous_ops_per_sec:42\r\n# Keyspace\r\ndb0:keys=9,expires=2,avg_ttl=0\r\n"
const fakeCmdstats = "# Commandstats\r\ncmdstat_get:calls=10,usec=100,usec_per_call=10.00\r\n"
const fakeClients = "id=3 addr=10.9.9.9:1 name=secretname idle=2 flags=N cmd=get\nid=4 addr=10.9.9.8:1 idle=100 flags=b cmd=blpop\n"

func newRedisFake(t *testing.T) *redisFake {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &redisFake{ln: ln, authed: map[net.Conn]bool{}, unknown: map[string]bool{}, noperm: map[string]bool{}}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	// Every test talks to the fake instead of the real port.
	old := redisDial
	redisDial = func(_ int, timeout time.Duration) (*bbredis.Client, error) {
		return bbredis.Dial(ln.Addr().(*net.TCPAddr).Port, timeout)
	}
	t.Cleanup(func() { redisDial = old })
	return f
}

func (f *redisFake) commands() [][]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]string(nil), f.seen...)
}

func (f *redisFake) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	for {
		line, err := r.ReadString('\n')
		if err != nil || !strings.HasPrefix(line, "*") {
			return
		}
		n, _ := strconv.Atoi(strings.TrimSpace(line[1:]))
		var args []string
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
		f.mu.Lock()
		f.seen = append(f.seen, args)
		authed := f.password == "" || f.authed[c]
		f.mu.Unlock()
		cmd := strings.ToUpper(args[0])
		var resp string
		switch {
		case cmd == "AUTH":
			if len(args) == 2 && args[1] == f.password {
				f.mu.Lock()
				f.authed[c] = true
				f.mu.Unlock()
				resp = "+OK\r\n"
			} else {
				resp = "-WRONGPASS invalid password " + strings.Join(args[1:], " ") + "\r\n"
			}
		case !authed:
			resp = "-NOAUTH Authentication required.\r\n"
		case f.noperm[cmd]:
			resp = "-NOPERM this user has no permissions to run the '" + cmd + "' command\r\n"
		case f.delay > 0 && cmd != "PING" && cmd != "AUTH":
			time.Sleep(f.delay)
			resp = redisBulk(fakeInfo)
		case f.unknown[cmd]:
			resp = "-ERR unknown command '" + cmd + "'\r\n"
		case cmd == "PING":
			resp = "+PONG\r\n"
		case cmd == "INFO" && len(args) == 1:
			resp = redisBulk(fakeInfo)
		case cmd == "INFO" && len(args) == 2 && args[1] == "commandstats":
			resp = redisBulk(fakeCmdstats)
		case cmd == "SLOWLOG":
			resp = "*2\r\n" +
				"*6\r\n:2\r\n:1700000200\r\n:15000\r\n*2\r\n" + redisBulk("GET") + redisBulk("user:42") + redisBulk("10.9.9.9:1") + redisBulk("secretname") +
				"*6\r\n:1\r\n:1700000100\r\n:12000\r\n*2\r\n" + redisBulk("AUTH") + redisBulk("hunter2") + redisBulk("10.9.9.9:1") + redisBulk("")
		case cmd == "LATENCY":
			resp = "*1\r\n*4\r\n" + redisBulk("command") + ":1700000000\r\n:12\r\n:40\r\n"
		case cmd == "CLIENT":
			resp = redisBulk(fakeClients)
		default:
			resp = "-ERR no\r\n"
		}
		io.WriteString(c, resp)
	}
}

type xorSealer struct{}

func (xorSealer) Protect(b []byte) ([]byte, error)   { return xorBytes(b), nil }
func (xorSealer) Unprotect(b []byte) ([]byte, error) { return xorBytes(b), nil }
func xorBytes(b []byte) []byte {
	out := make([]byte, len(b))
	for i, c := range b {
		out[i] = c ^ 0x5a
	}
	return out
}

func redisTestConfig(t *testing.T, profile string) Config {
	t.Helper()
	cfg := testConfig(t)
	cfg.Redis = &RedisSettings{Profile: profile}
	if err := os.MkdirAll(RedisDataDir(cfg.Folder), 0o700); err != nil {
		t.Fatal(err)
	}
	old := redisProtector
	redisProtector = func() (sealedfile.Protector, error) { return xorSealer{}, nil }
	t.Cleanup(func() { redisProtector = old })
	return cfg
}

func collectN(t *testing.T, g Group, n int) []*RedisGroup {
	t.Helper()
	t0 := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	var out []*RedisGroup
	for i := 0; i < n; i++ {
		v, err := g.Collect(t0.Add(time.Duration(i)*15*time.Second), 15)
		if err != nil {
			t.Fatalf("tick %d: %v", i, err)
		}
		out = append(out, v.(*RedisGroup))
	}
	return out
}

func TestRedisGroupAbsentWhenNothingListens(t *testing.T) {
	old := redisDial
	redisDial = func(int, time.Duration) (*bbredis.Client, error) { return nil, errors.New("refused") }
	defer func() { redisDial = old }()
	if _, err := newRedisGroup(GroupDeps{Config: testConfig(t)}); !errors.Is(err, errNotApplicable) {
		t.Fatalf("got %v, want errNotApplicable (silent, looks again later)", err)
	}
}

func TestRedisGroupReadsFiguresAndTheSlowerReadsOnePerSample(t *testing.T) {
	f := newRedisFake(t)
	cfg := redisTestConfig(t, RedisProfileEverything)
	g, err := newRedisGroup(GroupDeps{Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	defer g.(*redisGroup).Close()
	got := collectN(t, g, 4)
	first := got[0]
	if first.State != RedisOK || first.Numbers["connected_clients"] != 7 || first.Numbers["instantaneous_ops_per_sec"] != 42 || first.Words["redis_version"] != "3.0.504" || first.Keys != 9 || first.Expires != 2 {
		t.Fatalf("figures: %+v", first)
	}
	// One slower read per sample: commandstats, then slowlog, latency, clients.
	if len(got[0].Cmds) != 1 || got[0].Cmds[0].Name != "get" {
		t.Errorf("sample 1 cmds: %+v", got[0].Cmds)
	}
	if len(got[1].Slow) != 2 || got[1].Slow[0].Cmd != "auth" || got[1].Slow[1].Cmd != "get" || got[1].Slow[1].Arg != "" {
		t.Errorf("sample 2 slow: %+v", got[1].Slow)
	}
	if len(got[2].Latency) != 1 || got[2].Latency[0].Event != "command" {
		t.Errorf("sample 3 latency: %+v", got[2].Latency)
	}
	if got[3].Clients == nil || got[3].Clients.Total != 2 || got[3].Clients.Blocked != 1 {
		t.Errorf("sample 4 clients: %+v", got[3].Clients)
	}
	// Nothing the server holds that is private reaches a sample.
	for i, s := range got {
		b, _ := json.Marshal(s)
		for _, leak := range []string{"hunter2", "user:42", "10.9.9.9", "secretname", "requirepass"} {
			if strings.Contains(string(b), leak) {
				t.Errorf("sample %d leaks %q: %s", i+1, leak, b)
			}
		}
	}
	// Only allowed commands ever reached the server.
	for _, cmd := range f.commands() {
		if err := bbredis.Allowed(cmd); err != nil && !strings.EqualFold(cmd[0], "PING") {
			t.Errorf("a command outside the allowlist was sent: %v", cmd)
		}
	}
}

func TestRedisSlowCursorAndProfileDefaults(t *testing.T) {
	newRedisFake(t)
	cfg := redisTestConfig(t, "")
	g, _ := newRedisGroup(GroupDeps{Config: cfg})
	defer g.(*redisGroup).Close()
	t0 := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	var slowSeen int
	for i := 0; i < 12; i++ { // 3 minutes of 15 s samples: the slowlog is read three times
		v, err := g.Collect(t0.Add(time.Duration(i)*15*time.Second), 15)
		if err != nil {
			t.Fatal(err)
		}
		slowSeen += len(v.(*RedisGroup).Slow)
		if v.(*RedisGroup).Latency != nil || v.(*RedisGroup).Clients != nil {
			t.Fatal("the standard profile must not read latency or client counts")
		}
	}
	if slowSeen != 2 {
		t.Errorf("slow entries seen %d, want 2: the cursor must not repeat an entry", slowSeen)
	}
	if !cfg.RedisSlowlog() || cfg.RedisLatency() || cfg.RedisClients() || cfg.RedisSlowArgs() {
		t.Error("standard defaults are wrong")
	}
	off, on := false, true
	cfg.Redis = &RedisSettings{Profile: RedisProfileEverything, Slowlog: &off}
	if cfg.RedisSlowlog() || !cfg.RedisLatency() || !cfg.RedisClients() {
		t.Error("everything with slowlog off")
	}
	cfg.Redis = &RedisSettings{Latency: &on}
	if !cfg.RedisLatency() || cfg.RedisClients() {
		t.Error("a single row switched on against the standard profile")
	}
}

func TestRedisFirstArgumentOnlyWhenTicked(t *testing.T) {
	newRedisFake(t)
	cfg := redisTestConfig(t, "")
	cfg.Redis.SlowArgs = true
	g, _ := newRedisGroup(GroupDeps{Config: cfg})
	defer g.(*redisGroup).Close()
	var args []string
	for _, s := range collectN(t, g, 3) {
		for _, e := range s.Slow {
			args = append(args, e.Cmd+":"+e.Arg)
		}
	}
	if strings.Join(args, ",") != "auth:,get:user:42" && strings.Join(args, ",") != "get:user:42,auth:" {
		t.Errorf("args %v: only the key of GET may be kept, never AUTH's", args)
	}
}

func TestRedisPasswordNeededSavedRefusedAndAccepted(t *testing.T) {
	f := newRedisFake(t)
	f.password = "s3cr3t"
	cfg := redisTestConfig(t, "")
	g, _ := newRedisGroup(GroupDeps{Config: cfg})
	defer g.(*redisGroup).Close()
	if s := collectN(t, g, 1)[0]; s.State != RedisPasswordNeeded || s.Numbers != nil {
		t.Fatalf("no password saved: %+v", s)
	}
	if err := bbredis.SavePassword(RedisDataDir(cfg.Folder), "wrong", xorSealer{}); err != nil {
		t.Fatal(err)
	}
	s := collectN(t, g, 1)[0]
	if s.State != RedisPasswordRefused {
		t.Fatalf("wrong password: %+v", s)
	}
	if b, _ := json.Marshal(s); strings.Contains(string(b), "wrong") {
		t.Error("the refused password is in the sample")
	}
	if err := bbredis.SavePassword(RedisDataDir(cfg.Folder), "s3cr3t", xorSealer{}); err != nil {
		t.Fatal(err)
	}
	if s := collectN(t, g, 1)[0]; s.State != RedisOK || s.Numbers["connected_clients"] != 7 {
		t.Fatalf("right password: %+v", s)
	}
}

func TestRedisOldServerThatDoesNotKnowACommandIsSkippedAndSaid(t *testing.T) {
	f := newRedisFake(t)
	f.unknown["LATENCY"] = true
	cfg := redisTestConfig(t, RedisProfileEverything)
	g, _ := newRedisGroup(GroupDeps{Config: cfg})
	defer g.(*redisGroup).Close()
	t0 := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	var last *RedisGroup
	for i := 0; i < 12; i++ {
		v, err := g.Collect(t0.Add(time.Duration(i)*15*time.Second), 15)
		if err != nil {
			t.Fatal(err)
		}
		last = v.(*RedisGroup)
	}
	if len(last.Unsupported) != 1 || last.Unsupported[0] != "latency" {
		t.Errorf("unsupported %v", last.Unsupported)
	}
	n := 0
	for _, c := range f.commands() {
		if strings.EqualFold(c[0], "LATENCY") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("LATENCY was sent %d times, want once", n)
	}
}

func TestRedisSettingsValidationAndDefaults(t *testing.T) {
	cfg := testConfig(t)
	if cfg.RedisPort() != 6379 {
		t.Errorf("default port %d", cfg.RedisPort())
	}
	for _, bad := range []RedisSettings{{Profile: "deep"}, {Port: -1}, {Port: 70000}} {
		c := cfg
		b := bad
		c.Redis = &b
		if c.Validate() == nil {
			t.Errorf("%+v was accepted", bad)
		}
	}
	c := cfg
	c.Redis = &RedisSettings{Port: 6380, Profile: RedisProfileEverything}
	if err := c.Validate(); err != nil || c.RedisPort() != 6380 {
		t.Errorf("%v %d", err, c.RedisPort())
	}
	data, _ := cfg.Marshal()
	if strings.Contains(string(data), `"redis"`) {
		t.Error("a default config writes a redis key")
	}
	if _, err := ParseConfig([]byte(`{"name":"bb","folder":` + jsonString(cfg.Folder) + `,"redis":{"password":"x"}}`)); err == nil {
		t.Error("a password field in blackbox.json was accepted: it must never be kept there")
	}
}

func TestRedisGroupIsRegisteredInTheCatalogue(t *testing.T) {
	info, ok := CollectorOfGroup("redis")
	if !ok || info.ID != "redis.info" || info.Always {
		t.Fatalf("%+v %v", info, ok)
	}
}

func TestRedisDownIsHeldAndNotDialledEverySample(t *testing.T) {
	f := newRedisFake(t)
	cfg := redisTestConfig(t, "")
	g, _ := newRedisGroup(GroupDeps{Config: cfg})
	defer g.(*redisGroup).Close()
	if s := collectN(t, g, 1)[0]; s.State != RedisOK {
		t.Fatal(s.State)
	}
	dials := 0
	old := redisDial
	redisDial = func(int, time.Duration) (*bbredis.Client, error) { dials++; return nil, errors.New("refused") }
	defer func() { redisDial = old }()
	f.ln.Close()
	g.(*redisGroup).drop() // the connection died with the server
	got := collectN(t, g, 6)
	for i, s := range got {
		if s.State != RedisDown {
			t.Errorf("sample %d: %s", i, s.State)
		}
	}
	if dials != 1 {
		t.Errorf("dialled %d times in 6 samples; a stopped Redis must be left alone for a while", dials)
	}
}

func TestRedisRefusedPasswordIsNotRetriedUntilTheFileChanges(t *testing.T) {
	f := newRedisFake(t)
	f.password = "s3cr3t"
	cfg := redisTestConfig(t, "")
	dir := RedisDataDir(cfg.Folder)
	if err := bbredis.SavePassword(dir, "wrong", xorSealer{}); err != nil {
		t.Fatal(err)
	}
	g, _ := newRedisGroup(GroupDeps{Config: cfg})
	defer g.(*redisGroup).Close()
	for _, s := range collectN(t, g, 5) {
		if s.State != RedisPasswordRefused {
			t.Fatal(s.State)
		}
	}
	auths := 0
	for _, c := range f.commands() {
		if strings.EqualFold(c[0], "AUTH") {
			auths++
		}
	}
	if auths != 1 {
		t.Errorf("AUTH sent %d times for 5 samples; each is a failed login in a Redis 6 log", auths)
	}
	// Make the first file clearly older, so the change is seen on any clock granularity.
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(dir, bbredis.PasswordFile), old, old); err != nil {
		t.Fatal(err)
	}
	if err := bbredis.SavePassword(dir, "s3cr3t", xorSealer{}); err != nil {
		t.Fatal(err)
	}
	if s := collectN(t, g, 1)[0]; s.State != RedisOK {
		t.Fatalf("a new saved password should be tried at once: %s", s.State)
	}
}

func TestRedisPasswordIsNotSentToAnUntrustedListener(t *testing.T) {
	f := newRedisFake(t)
	f.password = "s3cr3t"
	cfg := redisTestConfig(t, "")
	if err := bbredis.SavePassword(RedisDataDir(cfg.Folder), "s3cr3t", xorSealer{}); err != nil {
		t.Fatal(err)
	}
	old := bbredis.ListenerCheck
	bbredis.ListenerCheck = func(int) error { return bbredis.ErrUntrustedListener }
	defer func() { bbredis.ListenerCheck = old }()
	g, _ := newRedisGroup(GroupDeps{Config: cfg})
	defer g.(*redisGroup).Close()
	if s := collectN(t, g, 1)[0]; s.State != RedisNotTrusted {
		t.Fatalf("state %s", s.State)
	}
	for _, c := range f.commands() {
		if strings.EqualFold(c[0], "AUTH") {
			t.Fatal("the password was sent to a listener that is not trusted")
		}
	}
}

func TestRedisNoPermIsSaidAndNotARetryLoop(t *testing.T) {
	f := newRedisFake(t)
	f.noperm["CLIENT"] = true
	cfg := redisTestConfig(t, RedisProfileEverything)
	g, _ := newRedisGroup(GroupDeps{Config: cfg})
	defer g.(*redisGroup).Close()
	t0 := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	var last *RedisGroup
	for i := 0; i < 12; i++ {
		v, _ := g.Collect(t0.Add(time.Duration(i)*15*time.Second), 15)
		last = v.(*RedisGroup)
	}
	if last.State != RedisOK || len(last.Denied) != 1 || last.Denied[0] != "clients" || len(last.Unsupported) != 0 {
		t.Fatalf("%+v", last)
	}
	clients, conns := 0, 0
	for _, c := range f.commands() {
		if strings.EqualFold(c[0], "CLIENT") {
			clients++
		}
		if strings.EqualFold(c[0], "PING") {
			conns++
		}
	}
	if clients != 1 || conns != 1 {
		t.Errorf("CLIENT sent %d times and %d connections opened; want 1 and 1", clients, conns)
	}
	// INFO itself denied: its own state, and the connection stays.
	f.noperm["INFO"] = true
	if s := collectN(t, g, 1)[0]; s.State != RedisDenied {
		t.Errorf("INFO denied: %s", s.State)
	}
}

func TestRedisSlowerReadIsSkippedWhenLittleTimeIsLeft(t *testing.T) {
	f := newRedisFake(t)
	f.delay = 800 * time.Millisecond // INFO takes most of the 900 ms budget: nothing is left for the extra read
	cfg := redisTestConfig(t, RedisProfileEverything)
	g, _ := newRedisGroup(GroupDeps{Config: cfg})
	defer g.(*redisGroup).Close()
	v, _ := g.Collect(time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC), 15)
	if s := v.(*RedisGroup); s.State != RedisOK && s.State != RedisDown {
		t.Fatalf("state %s", s.State)
	}
	for _, c := range f.commands() {
		if len(c) == 2 && strings.EqualFold(c[0], "INFO") && c[1] == "commandstats" {
			t.Fatal("the slower read ran although the sample budget was almost used up")
		}
	}
}

func TestRedisCloseWhileCollectingIsSafe(t *testing.T) {
	f := newRedisFake(t)
	f.delay = 200 * time.Millisecond
	cfg := redisTestConfig(t, "")
	g, _ := newRedisGroup(GroupDeps{Config: cfg})
	done := make(chan struct{})
	go func() {
		g.Collect(time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC), 15)
		close(done)
	}()
	for i := 0; i < 400 && len(f.commands()) == 0; i++ { // wait until Collect is really talking to the server
		time.Sleep(5 * time.Millisecond) // winok: polling a condition with a generous limit
	}
	g.(*redisGroup).Close()
	<-done
}
