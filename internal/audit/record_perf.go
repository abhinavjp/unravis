package audit

// Limits for the perf block of one record's detail.
const (
	PerfMaxFilters  = 20
	PerfMaxSlow     = 3
	PerfMaxSteps    = 10
	PerfMaxPressure = 8
	PerfMaxName     = 100
)

// RecordPerf is the performance block of one record's detail. A nil scalar
// means "not recorded", never zero.
type RecordPerf struct {
	HitCount      *float64    `json:"hitCount"`
	FilterTotalMs *float64    `json:"filterTotalMs"`
	SQLCount      *float64    `json:"sqlCount"`
	SQLMs         *float64    `json:"sqlMs"`
	RedisCount    *float64    `json:"redisCount"`
	RedisMs       *float64    `json:"redisMs"`
	HTTPCount     *float64    `json:"httpCount"`
	HTTPMs        *float64    `json:"httpMs"`
	Filters       []PerfNamed `json:"filters"`
	Steps         []PerfNamed `json:"steps"`
	SlowSQL       []PerfSlow  `json:"slowSql"`
	SlowHTTP      []PerfSlow  `json:"slowHttp"`
	Pressure      []PerfValue `json:"pressure"`
	TextStored    *bool       `json:"textStored"`
}

// PerfNamed is a named timing, such as a filter or a pipeline step.
type PerfNamed struct {
	Name string   `json:"name"`
	Ms   *float64 `json:"ms"`
}

// PerfSlow is one slow SQL or HTTP entry: how often it ran and how long.
type PerfSlow struct {
	Count *float64 `json:"count"`
	Ms    *float64 `json:"ms"`
}

// PerfValue is a named measurement, such as a pressure reading.
type PerfValue struct {
	Name  string   `json:"name"`
	Value *float64 `json:"value"`
}
