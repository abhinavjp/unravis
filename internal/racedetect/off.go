//go:build !race

// Package racedetect tells a test whether the Go race detector is on (go test -race).
// Tests with a time or memory budget use it to skip: the detector slows code 5 to 20 times,
// so a budget set for a normal run proves nothing there. Real races are caught by the race
// run itself, which still executes every other test.
package racedetect

// Enabled is true when the binary was built with -race.
const Enabled = false
