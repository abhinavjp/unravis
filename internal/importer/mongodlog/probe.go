package mongodlog

import (
	"encoding/json"
	"time"

	"api-audit-explorer/internal/importer"
)

// ProbeTime implements importer.TimeProber: the t.$date of a structured mongod
// log line. A date without an offset is refused, as Parse refuses it.
func (Importer) ProbeTime(line []byte, _ importer.Options) (time.Time, bool) {
	if len(line) == 0 || line[0] != '{' {
		return time.Time{}, false
	}
	var ll struct {
		T struct {
			Date json.RawMessage `json:"$date"`
		} `json:"t"`
	}
	if json.Unmarshal(line, &ll) != nil {
		return time.Time{}, false
	}
	t, why := parseTime(ll.T.Date)
	if why != "" {
		return time.Time{}, false
	}
	return t, true
}

var _ importer.TimeProber = Importer{}
