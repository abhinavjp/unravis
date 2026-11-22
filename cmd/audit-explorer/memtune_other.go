//go:build !windows

package main

// totalRAM is unknown off Windows (the app ships for Windows only), so the soft
// limit stays at its 256 MiB floor.
func totalRAM() uint64 { return 0 }
