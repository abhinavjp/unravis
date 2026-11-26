package blackbox

import (
	"errors"
	"path/filepath"
	"time"

	"api-audit-explorer/internal/bbredis"
	"api-audit-explorer/internal/sealedfile"
)

// B4: the Redis collector. It runs inside the host recorder, talks only to 127.0.0.1 on the Redis port, sends only the
// commands in bbredis.Allowed, and keeps numbers and fixed names (never a key, a value or a client address). A
// password, when the server wants one, is read from a sealed file in the Blackbox's locked redis folder; it never
// appears in blackbox.json, a pack, a log line or the sample.
//
// Redis for Windows is old (3.x) and Memurai differs; a command the server does not know is reported once in
// "unsupported" and skipped from then on, never faked.

func init() { RegisterGroup("redis", true, newRedisGroup) }

// RedisDataSubdir holds the sealed password, inside the Blackbox folder.
const RedisDataSubdir = "redis"

// RedisDataDir is the locked folder for the Redis password of the Blackbox at folder.
func RedisDataDir(folder string) string { return filepath.Join(folder, RedisDataSubdir) }

// Redis profiles: what the collector reads beyond the server figures (INFO, always).
const (
	RedisProfileStandard   = "standard"   // INFO and slow commands
	RedisProfileEverything = "everything" // also latency spikes and client counts
)

// RedisSettings is the "redis" block of blackbox.json. Settings only: a password is never kept in this file.
type RedisSettings struct {
	// Profile is "standard" (default) or "everything".
	Profile string `json:"profile,omitempty"`
	// Port is the Redis port on this PC. 0 means 6379.
	Port int `json:"port,omitempty"`
	// Slowlog, Latency and Clients switch one row on or off against the profile.
	Slowlog *bool `json:"slowlog,omitempty"`
	Latency *bool `json:"latency,omitempty"`
	Clients *bool `json:"clients,omitempty"`
	// SlowArgs also keeps the first argument of slow commands that take a key first (off unless ticked). Key names can
	// still say something about a business, which is why it is a separate, explicit choice.
	SlowArgs bool `json:"slowArgs,omitempty"`
}

func (s RedisSettings) validate() error {
	switch s.Profile {
	case "", RedisProfileStandard, RedisProfileEverything:
	default:
		return invalidf(`"redis.profile" must be "standard" or "everything"`)
	}
	if s.Port != 0 && (s.Port < 1 || s.Port > 65535) {
		return invalidf(`"redis.port" must be 1 to 65535`)
	}
	return nil
}

// RedisPort is the port the collector connects to on this PC.
func (c Config) RedisPort() int {
	if c.Redis == nil || c.Redis.Port == 0 {
		return bbredis.DefaultPort
	}
	return c.Redis.Port
}

func (c Config) redisRow(pick func(*RedisSettings) *bool, everythingDefault bool) bool {
	if c.Redis != nil {
		if v := pick(c.Redis); v != nil {
			return *v
		}
		if c.Redis.Profile == RedisProfileEverything {
			return true
		}
	}
	return everythingDefault
}

// RedisSlowlog, RedisLatency and RedisClients say which rows are on under this configuration.
func (c Config) RedisSlowlog() bool {
	return c.redisRow(func(r *RedisSettings) *bool { return r.Slowlog }, true)
}
func (c Config) RedisLatency() bool {
	return c.redisRow(func(r *RedisSettings) *bool { return r.Latency }, false)
}
func (c Config) RedisClients() bool {
	return c.redisRow(func(r *RedisSettings) *bool { return r.Clients }, false)
}

// RedisSlowArgs says whether the first argument of slow key commands is kept.
func (c Config) RedisSlowArgs() bool { return c.Redis != nil && c.Redis.SlowArgs }

// Sample states of the Redis block.
const (
	RedisOK              = "ok"
	RedisPasswordNeeded  = "password-needed"
	RedisPasswordRefused = "password-refused"
)

// RedisGroup is the sample block of the Redis collector.
type RedisGroup struct {
	State   string                 `json:"state"`
	Numbers map[string]float64     `json:"numbers,omitempty"`
	Words   map[string]string      `json:"words,omitempty"`
	Keys    int64                  `json:"keys,omitempty"`
	Expires int64                  `json:"expires,omitempty"`
	Cmds    []bbredis.CommandStat  `json:"cmds,omitempty"`
	Slow    []bbredis.SlowEntry    `json:"slow,omitempty"`
	Latency []bbredis.LatencyEntry `json:"latency,omitempty"`
	Clients *bbredis.ClientCounts  `json:"clients,omitempty"`
	// Unsupported names the commands this server does not know (old Redis for Windows), so a missing block is explained.
	Unsupported []string `json:"unsupported,omitempty"`
}

// redisProtector is the machine-wide protector for the password file. A variable so tests run without Windows.
var redisProtector = func() (sealedfile.Protector, error) { return sealedfile.LocalMachine() }

// redisDial opens the connection. A variable so tests can use a fake server on any port.
var redisDial = bbredis.Dial

const (
	redisTimeout  = 700 * time.Millisecond // per command: INFO plus one extra still fits the one-second group budget
	redisExtraGap = time.Minute            // the slower reads run once a minute
)

type redisGroup struct {
	cfg  Config
	dir  string
	c    *bbredis.Client
	pass string // read once per connection, never logged

	lastExtra   map[string]time.Time
	unsupported map[string]bool
	slowAfter   int64
}

func newRedisGroup(d GroupDeps) (Group, error) {
	g := &redisGroup{cfg: d.Config, dir: RedisDataDir(d.Config.Folder), lastExtra: map[string]time.Time{}, unsupported: map[string]bool{}}
	// Is there a Redis on this PC? A refused connection means no: the group stays silent and looks again later. Any
	// answer, even "password needed", means yes.
	c, err := redisDial(g.cfg.RedisPort(), redisTimeout)
	if err != nil {
		return nil, errNotApplicable
	}
	_ = c.Close()
	return g, nil
}

func (g *redisGroup) Name() string   { return "redis" }
func (g *redisGroup) Optional() bool { return true }

func (g *redisGroup) Close() error { g.drop(); return nil }

func (g *redisGroup) drop() {
	if g.c != nil {
		_ = g.c.Close()
		g.c = nil
	}
	g.pass = ""
}

// connect opens the connection and signs in if a password is saved.
func (g *redisGroup) connect() (state string, err error) {
	c, err := redisDial(g.cfg.RedisPort(), redisTimeout)
	if err != nil {
		return "", errGroupUnavailable
	}
	g.c = c
	if _, err := c.Do("PING"); err != nil {
		if !bbredis.IsNoAuth(err) {
			g.drop()
			return "", errGroupUnavailable
		}
		prot, perr := redisProtector()
		if perr != nil {
			g.drop()
			return RedisPasswordNeeded, nil
		}
		pw, found, lerr := bbredis.LoadPassword(g.dir, prot)
		if lerr != nil || !found {
			g.drop()
			return RedisPasswordNeeded, nil
		}
		if aerr := c.Auth(pw); aerr != nil {
			g.drop()
			if bbredis.IsNoAuth(aerr) {
				return RedisPasswordRefused, nil
			}
			return "", errGroupUnavailable
		}
	}
	return RedisOK, nil
}

func (g *redisGroup) Collect(now time.Time, _ float64) (any, error) {
	if g.c == nil {
		state, err := g.connect()
		if err != nil {
			return nil, err
		}
		if state != RedisOK {
			return &RedisGroup{State: state}, nil
		}
	}
	rep, err := g.c.Do("INFO")
	if err != nil {
		g.drop()
		if bbredis.IsNoAuth(err) {
			return &RedisGroup{State: RedisPasswordNeeded}, nil
		}
		return nil, errGroupUnavailable
	}
	info := bbredis.ParseInfo(rep.Str)
	out := &RedisGroup{State: RedisOK, Numbers: bbredis.Numbers(info), Words: bbredis.Words(info)}
	out.Keys, out.Expires = bbredis.Keyspace(info)
	g.extra(now, out)
	for name := range g.unsupported {
		out.Unsupported = append(out.Unsupported, name)
	}
	sortStringsStable(out.Unsupported)
	return out, nil
}

// extra runs at most ONE slower read per sample, the one that has waited longest, so a sample stays inside its budget.
func (g *redisGroup) extra(now time.Time, out *RedisGroup) {
	order := []string{"commandstats", "slowlog", "latency", "clients"}
	enabled := map[string]bool{"commandstats": true, "slowlog": g.cfg.RedisSlowlog(), "latency": g.cfg.RedisLatency(), "clients": g.cfg.RedisClients()}
	pick := ""
	var oldest time.Time
	for _, name := range order {
		if !enabled[name] || g.unsupported[name] {
			continue
		}
		last := g.lastExtra[name]
		if !last.IsZero() && now.Sub(last) < redisExtraGap-redisExtraGap/10 && !now.Before(last) {
			continue
		}
		if pick == "" || last.Before(oldest) {
			pick, oldest = name, last
		}
	}
	if pick == "" {
		return
	}
	g.lastExtra[pick] = now
	var rep bbredis.Reply
	var err error
	switch pick {
	case "commandstats":
		rep, err = g.c.Do("INFO", "commandstats")
	case "slowlog":
		rep, err = g.c.Do("SLOWLOG", "GET", "64")
	case "latency":
		rep, err = g.c.Do("LATENCY", "LATEST")
	case "clients":
		rep, err = g.c.Do("CLIENT", "LIST")
	}
	if err != nil {
		var re *bbredis.RedisError
		if errors.As(err, &re) && re.Word == "ERR" {
			g.unsupported[pick] = true // an old server that does not know the command
			return
		}
		if bbredis.IsNoAuth(err) || !errors.As(err, &re) {
			g.drop() // the connection is no good; the next sample opens a new one
		}
		return
	}
	switch pick {
	case "commandstats":
		out.Cmds = bbredis.CommandStats(bbredis.ParseInfo(rep.Str))
	case "slowlog":
		entries := bbredis.SlowEntries(rep, g.slowAfter, g.cfg.RedisSlowArgs())
		// A restart of Redis starts the ids again from 0: a newest id below the cursor means the log was reset.
		if len(entries) == 0 && len(rep.Arr) > 0 && rep.Arr[0].Kind == '*' && len(rep.Arr[0].Arr) > 0 && rep.Arr[0].Arr[0].Int < g.slowAfter {
			g.slowAfter = 0
			entries = bbredis.SlowEntries(rep, 0, g.cfg.RedisSlowArgs())
		}
		for _, e := range entries {
			if e.ID > g.slowAfter {
				g.slowAfter = e.ID
			}
		}
		out.Slow = entries
	case "latency":
		out.Latency = bbredis.LatencyEntries(rep)
	case "clients":
		cc := bbredis.CountClients(rep.Str)
		out.Clients = &cc
	}
}

func sortStringsStable(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
