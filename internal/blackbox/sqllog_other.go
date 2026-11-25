//go:build !windows

package blackbox

// sqlLogInstancesPlatform is nil off Windows: there is no SQL Server registry key to read.
var sqlLogInstancesPlatform func() []sqlLogInstance
