package perfdata

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
)

// rawSink writes the same rows as plain text beside the case (W3C lines for IIS,
// CSV lines for SQL), so the files can also be fed to the importers by hand. A
// nil *rawSink ignores every call.
type rawSink struct {
	f   *os.File
	w   *bufio.Writer
	err error
}

func newRawSink(cfg Config, lane, name string) *rawSink {
	if !cfg.RawFiles {
		return nil
	}
	dir := filepath.Join(cfg.Out, "raw")
	s := &rawSink{}
	if s.err = os.MkdirAll(dir, 0o755); s.err != nil {
		return s
	}
	if s.f, s.err = os.Create(filepath.Join(dir, name)); s.err != nil {
		return s
	}
	s.w = bufio.NewWriterSize(s.f, 1<<20)
	if lane == LaneIIS {
		s.str("#Software: perfdata\n#Version: 1.0\n#Fields: date time s-ip cs-method cs-uri-stem sc-status sc-substatus sc-win32-status time-taken c-ip\n")
	} else {
		s.str("timestamp,event,wait_type,duration_ms,error_number,sql_severity\n")
	}
	return s
}

func (s *rawSink) str(v string) {
	if s == nil || s.err != nil || s.w == nil {
		return
	}
	_, s.err = s.w.WriteString(v)
}

func (s *rawSink) iis(r iisRow) {
	s.str(r.ts.Format("2006-01-02 15:04:05") + " 10.0.0.5 " + r.method + " " + r.stem + " " + strconv.Itoa(r.status) + " 0 0 " + strconv.Itoa(r.ms) + " " + r.clientI + "\n")
}

func (s *rawSink) sql(r sqlRow) {
	s.str(r.ts.Format("2006-01-02T15:04:05.000Z") + "," + r.event + "," + r.waitType + "," + strconv.FormatFloat(r.ms, 'f', 0, 64) + "," + strconv.Itoa(r.errNo) + "," + strconv.Itoa(r.sqlSev) + "\n")
}

func (s *rawSink) close() error {
	if s == nil {
		return nil
	}
	if s.w != nil && s.err == nil {
		s.err = s.w.Flush()
	}
	if s.f != nil {
		if e := s.f.Close(); s.err == nil {
			s.err = e
		}
	}
	return s.err
}
