//go:build !windows

package blackbox

import "errors"

// OSDisk reads real volumes. Only Windows is supported.
type OSDisk struct{}

// Free is not available off Windows.
func (OSDisk) Free(path string) (DiskInfo, error) {
	return DiskInfo{}, errors.New("disk space is read only on Windows")
}
