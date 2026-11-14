//go:build !windows

package blackbox

import "errors"

// tcpTableCounts is not available off Windows.
func tcpTableCounts() ([]TCPStateCount, error) {
	return nil, errors.New("TCP state counts are read only on Windows")
}