package bbsql

// readback.go is the small public face the READER of the sample files needs. The
// samplers write; Unravis (a different process, usually a different account)
// reads the hourly files back. Both sides must agree on the same shapes, so the
// reader checks every line against the SAME rules the writer applied: the wait
// type shape (C9), the row caps (C6) and the availability-group state domain.
// Nothing here opens a connection or touches a file.

// SampleKinds are the only "kind" values a sample line may carry, mapped to the
// most rows one line of that kind may hold (the C6 caps). A line with another
// kind, or with more rows than its cap, was not written by this program.
var SampleKinds = map[string]int{
	"requests":   CapRequests,
	"waits":      CapWaits,
	"schedulers": CapSchedulers,
	"semaphores": CapSemaphores,
	"tempdb":     CapTempdb,
	"filestats":  CapFileStats,
	"ag":         CapAG,
}

// ValidWaitType says whether s is a wait type the writer could have stored: the
// strict C9 shape, or the fixed sentinel for running requests.
func ValidWaitType(s string) bool {
	return s == WaitTypeRunning || waitTypeRe.MatchString(s)
}

// ValidSyncState says whether n is inside the availability-group state domain.
func ValidSyncState(n uint8) bool { return n <= agSyncStateMax }

// SamplePrefix and SampleSuffix frame the hourly file name: bb-sql-<hour>.ndjson.
const (
	SamplePrefix = "bb-sql-"
	SampleSuffix = ".ndjson"
)

// SampleHourLayout is the hour part of the file name.
const SampleHourLayout = hourLayout
