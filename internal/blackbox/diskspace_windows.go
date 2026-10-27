//go:build windows

package blackbox

import "golang.org/x/sys/windows"

// OSDisk reads real volumes.
type OSDisk struct{}

// Free reads the volume of path with GetDiskFreeSpaceEx. path may be a folder
// that does not exist yet: the nearest existing parent is used.
func (OSDisk) Free(path string) (DiskInfo, error) {
	p, err := windows.UTF16PtrFromString(nearestExisting(path))
	if err != nil {
		return DiskInfo{}, err
	}
	var free, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &free, &total, &totalFree); err != nil {
		return DiskInfo{}, err
	}
	return DiskInfo{Free: free, Total: total}, nil
}
