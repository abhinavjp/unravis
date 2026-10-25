package perfbench

import (
	"bufio"
	"math/rand/v2"
	"os"
	"strconv"
	"time"
)

// Everything generated here is synthetic: web-a, *.example.test, 2001 dates and
// the sentinel SENTINEL-7f3a in places a real log would hold a secret.

var (
	iisDay     = time.Date(2001, 10, 25, 0, 0, 0, 0, time.UTC)
	iisPaths   = buildPaths()
	iisMethods = []string{"GET", "GET", "GET", "GET", "POST", "PUT", "DELETE"}
	iisStatus  = []int{200, 200, 200, 200, 200, 200, 200, 201, 204, 301, 304, 400, 401, 404, 404, 500, 503}
)

func buildPaths() []string {
	var p []string
	for i := 0; i < 40; i++ {
		p = append(p, "/api/v1/orders/"+strconv.Itoa(10000+i*37))
	}
	for i := 0; i < 20; i++ {
		p = append(p, "/api/v1/items/123e4567-e89b-12d3-a456-4266141740"+strconv.Itoa(10+i)+"/lines")
	}
	p = append(p, "/health", "/static/app.js", "/static/site.css", "/api/v1/orders", "/api/v1/customers", "/login", "/")
	return p
}

type fastFile struct {
	f   *os.File
	w   *bufio.Writer
	buf []byte
	n   int64
}

func newFastFile(path string) (*fastFile, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	return &fastFile{f: f, w: bufio.NewWriterSize(f, 1<<20), buf: make([]byte, 0, 4096)}, nil
}

func (ff *fastFile) flushLine() error {
	n, err := ff.w.Write(ff.buf)
	ff.n += int64(n)
	ff.buf = ff.buf[:0]
	return err
}

func (ff *fastFile) writeString(s string) error {
	n, err := ff.w.WriteString(s)
	ff.n += int64(n)
	return err
}

func (ff *fastFile) close() error {
	if err := ff.w.Flush(); err != nil {
		_ = ff.f.Close()
		return err
	}
	return ff.f.Close()
}

func appendPad2(b []byte, v int) []byte {
	return append(b, byte('0'+v/10), byte('0'+v%10))
}

// appendDateTime appends "YYYY-MM-DD HH:MM:SS" for sec seconds after iisDay.
func appendDateTime(b []byte, sec int64, cache *dtCache) []byte {
	if cache.sec != sec || cache.s == nil {
		t := iisDay.Add(time.Duration(sec) * time.Second)
		cache.s = t.AppendFormat(cache.s[:0], "2006-01-02 15:04:05")
		cache.sec = sec
	}
	return append(b, cache.s...)
}

type dtCache struct {
	sec int64
	s   []byte
}

const w3cFields = "date time s-sitename s-computername s-ip cs-method cs-uri-stem cs-uri-query s-port cs-username c-ip cs(User-Agent) cs(Cookie) cs(Referer) sc-status sc-substatus sc-win32-status time-taken"

const w3cHeader = "#Software: Microsoft Internet Information Services 10.0\n#Version: 1.0\n#Date: 2001-10-25 00:00:00\n#Fields: " + w3cFields + "\n"

// iisLine appends one W3C line for second sec.
func iisLine(b []byte, rng *rand.Rand, sec int64, c *dtCache) []byte {
	b = appendDateTime(b, sec, c)
	b = append(b, " W3SVC1 WEB-A 192.0.2.10 "...)
	b = append(b, iisMethods[rng.IntN(len(iisMethods))]...)
	b = append(b, ' ')
	b = append(b, iisPaths[rng.IntN(len(iisPaths))]...)
	if rng.IntN(4) == 0 {
		b = append(b, " q=SENTINEL-7f3a 443 "...)
	} else {
		b = append(b, " - 443 "...)
	}
	if rng.IntN(3) == 0 {
		b = append(b, "alice"...)
	} else {
		b = append(b, '-')
	}
	b = append(b, " 192.0.2."...)
	b = strconv.AppendInt(b, int64(20+rng.IntN(200)), 10)
	b = append(b, " Mozilla/5.0+SENTINEL-7f3a - http://ref.example.test/page "...)
	st := iisStatus[rng.IntN(len(iisStatus))]
	b = strconv.AppendInt(b, int64(st), 10)
	b = append(b, " 0 0 "...)
	// time-taken: mostly small, a long tail.
	tt := 5 + rng.IntN(120)
	if rng.IntN(20) == 0 {
		tt += rng.IntN(5000)
	}
	b = strconv.AppendInt(b, int64(tt), 10)
	return append(b, '\n')
}

// genIIS writes n W3C lines spread evenly over spanSec seconds starting at
// 2001-10-25 00:00:00 and returns the bytes written.
func genIIS(path string, n int, spanSec int64) (int64, error) {
	ff, err := newFastFile(path)
	if err != nil {
		return 0, err
	}
	rng := rand.New(rand.NewPCG(7, 11))
	if err := ff.writeString(w3cHeader); err != nil {
		return 0, err
	}
	var c dtCache
	for i := 0; i < n; i++ {
		sec := int64(i) * spanSec / int64(n)
		ff.buf = iisLine(ff.buf, rng, sec, &c)
		if err := ff.flushLine(); err != nil {
			return 0, err
		}
	}
	return ff.n, ff.close()
}

// genBigIIS writes W3C lines at linesPerSec until the file reaches size bytes.
// It records the byte offset of the first line start at or after each multiple
// of markEvery bytes, so a test can open a window at a known line boundary.
func genBigIIS(path string, size int64, linesPerSec int64, markEvery int64) (marks []int64, lines int64, err error) {
	ff, err := newFastFile(path)
	if err != nil {
		return nil, 0, err
	}
	rng := rand.New(rand.NewPCG(13, 17))
	if err := ff.writeString(w3cHeader); err != nil {
		return nil, 0, err
	}
	var c dtCache
	next := markEvery
	for i := int64(0); ff.n < size; i++ {
		if ff.n >= next {
			marks = append(marks, ff.n)
			next += markEvery
		}
		ff.buf = iisLine(ff.buf, rng, i/linesPerSec, &c)
		if err := ff.flushLine(); err != nil {
			return nil, 0, err
		}
		lines++
	}
	return marks, lines, ff.close()
}

var (
	log4Levels  = []string{"INFO ", "INFO ", "INFO ", "INFO ", "DEBUG", "WARN ", "ERROR", "INFO "}
	log4Loggers = []string{"Synthetic.Api.OrdersController", "Synthetic.Api.CustomersController", "Synthetic.Data.OrderRepository", "StackExchange.Redis.ConnectionMultiplexer", "Synthetic.Jobs.Nightly"}
)

// genLog4net writes about n entries (continuation lines of an ERROR do not count
// as entries) in the log4net default pattern. It returns the bytes and lines.
func genLog4net(path string, n int) (bytes, lines int64, err error) {
	ff, err := newFastFile(path)
	if err != nil {
		return 0, 0, err
	}
	rng := rand.New(rand.NewPCG(19, 23))
	var c dtCache
	for i := 0; i < n; i++ {
		ms := int64(i) * 86400_000 / int64(n)
		b := ff.buf
		b = appendDateTime(b, ms/1000, &c)
		b = append(b, ',')
		m := int(ms % 1000)
		b = append(b, byte('0'+m/100), byte('0'+(m/10)%10), byte('0'+m%10))
		b = append(b, " ["...)
		b = strconv.AppendInt(b, int64(1+rng.IntN(60)), 10)
		b = append(b, "] "...)
		lv := log4Levels[rng.IntN(len(log4Levels))]
		b = append(b, lv...)
		b = append(b, ' ')
		b = append(b, log4Loggers[rng.IntN(len(log4Loggers))]...)
		b = append(b, " - Request handled for order-"...)
		b = strconv.AppendInt(b, int64(rng.IntN(100000)), 10)
		b = append(b, " user alice@example.test in "...)
		b = strconv.AppendInt(b, int64(rng.IntN(900)), 10)
		b = append(b, "ms\n"...)
		lines++
		if lv == "ERROR" {
			b = append(b, "System.TimeoutException: The operation has timed out. SENTINEL-7f3a\n   at Synthetic.Api.OrdersController.Get(Int32 id) in C:\\src\\Synthetic\\OrdersController.cs:line 42\n"...)
			lines += 2
		}
		ff.buf = b
		if err := ff.flushLine(); err != nil {
			return 0, 0, err
		}
	}
	return ff.n, lines, ff.close()
}

// pdhCols returns 200 counter paths for WEB-A: host CPU, per-core CPU, memory,
// and four counters for each of 41 worker processes.
func pdhCols() []string {
	cols := []string{`\\WEB-A\Processor(_Total)\% Processor Time`}
	for i := 0; i < 32; i++ {
		cols = append(cols, `\\WEB-A\Processor(`+strconv.Itoa(i)+`)\% Processor Time`)
	}
	cols = append(cols, `\\WEB-A\Memory\Available MBytes`, `\\WEB-A\Memory\Committed Bytes`, `\\WEB-A\Memory\Pool Nonpaged Bytes`)
	for k := 0; len(cols) < 200; k++ {
		inst := `w3wp#` + strconv.Itoa(k)
		for _, c := range []string{`% Processor Time`, `Working Set`, `Private Bytes`, `Thread Count`} {
			if len(cols) < 200 {
				cols = append(cols, `\\WEB-A\Process(`+inst+`)\`+c)
			}
		}
	}
	return cols
}

// genPDH writes a relog-style PDH CSV: len(pdhCols()) counters, rows every
// intervalSec for spanSec seconds from 2001-10-24 00:00:00, about 1 percent of
// the cells missing. It returns the bytes, rows and columns.
func genPDH(path string, spanSec, intervalSec int) (bytes int64, rows, cols int, err error) {
	ff, err := newFastFile(path)
	if err != nil {
		return 0, 0, 0, err
	}
	names := pdhCols()
	cols = len(names)
	b := append(ff.buf, `"(PDH-CSV 4.0) (UTC)(0)"`...)
	for _, c := range names {
		b = append(b, ',', '"')
		b = append(b, c...)
		b = append(b, '"')
	}
	b = append(b, '\n')
	ff.buf = b
	if err := ff.flushLine(); err != nil {
		return 0, 0, 0, err
	}
	rng := rand.New(rand.NewPCG(29, 31))
	start := time.Date(2001, 10, 24, 0, 0, 0, 0, time.UTC)
	for s := 0; s < spanSec; s += intervalSec {
		t := start.Add(time.Duration(s) * time.Second)
		b := append(ff.buf, '"')
		b = t.AppendFormat(b, "01/02/2006 15:04:05.000")
		b = append(b, '"')
		for i := 0; i < cols; i++ {
			b = append(b, ',', '"')
			if rng.IntN(100) == 0 {
				b = append(b, ' ')
			} else if i < 33 || (i >= 36 && (i-36)%4 == 0) {
				b = strconv.AppendFloat(b, rng.Float64()*100, 'f', 2, 64)
			} else {
				b = strconv.AppendInt(b, int64(rng.IntN(1<<30)), 10)
			}
			b = append(b, '"')
		}
		b = append(b, '\n')
		ff.buf = b
		if err := ff.flushLine(); err != nil {
			return 0, 0, 0, err
		}
		rows++
	}
	return ff.n, rows, cols, ff.close()
}

var evMessages = []string{
	"SYNTHETIC: pool failed health check, with a comma",
	"SYNTHETIC: service entered the running state",
	"SYNTHETIC: a worker process serving application pool exceeded its memory limit",
	"SYNTHETIC: the request timed out waiting for a connection",
}

// genEvents writes n win-events CSV rows from 2001-10-24 00:00:00 UTC, 100 ms
// apart.
func genEvents(path string, n int) (int64, error) {
	ff, err := newFastFile(path)
	if err != nil {
		return 0, err
	}
	if err := ff.writeString("#TYPE Selected.System.Diagnostics.Eventing.Reader.EventLogRecord\n\"TimeCreatedUtc\",\"MachineName\",\"LogName\",\"ProviderName\",\"Id\",\"Level\",\"AppPool\",\"Message\"\n"); err != nil {
		return 0, err
	}
	rng := rand.New(rand.NewPCG(37, 41))
	start := time.Date(2001, 10, 24, 0, 0, 0, 0, time.UTC)
	ids := []int{5002, 5011, 1000, 1001, 7036, 5117, 2276}
	for i := 0; i < n; i++ {
		t := start.Add(time.Duration(i) * 100 * time.Millisecond)
		b := append(ff.buf, '"')
		b = t.AppendFormat(b, "2006-01-02T15:04:05.0000000Z")
		b = append(b, `","WEB-A.example.test","System","Microsoft-Windows-WAS","`...)
		b = strconv.AppendInt(b, int64(ids[rng.IntN(len(ids))]), 10)
		b = append(b, `","`...)
		b = strconv.AppendInt(b, int64(2+rng.IntN(3)), 10)
		b = append(b, `","DefaultAppPool","`...)
		b = append(b, evMessages[rng.IntN(len(evMessages))]...)
		b = append(b, `"`+"\n"...)
		ff.buf = b
		if err := ff.flushLine(); err != nil {
			return 0, err
		}
	}
	return ff.n, ff.close()
}
