package blackbox

// DiskSpace reads the free space of the volume that holds a path. The recorder
// uses it for the free-disk limit; install uses it for the 1 GB check. Tests use
// a fake.
type DiskSpace interface {
	Free(path string) (DiskInfo, error)
}

// DiskInfo is the space of one volume, in bytes. Free is what the current
// account may use.
type DiskInfo struct {
	Free  uint64
	Total uint64
}

// FreePercent is Free as a percent of Total (0 when Total is 0).
func (d DiskInfo) FreePercent() float64 {
	if d.Total == 0 {
		return 0
	}
	return float64(d.Free) / float64(d.Total) * 100
}
