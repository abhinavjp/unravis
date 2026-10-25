// Package perfbench measures Unravis's own CPU, memory and time on large
// synthetic data. It holds only tests (bench_*_test.go). Every test skips
// unless UNRAVIS_BENCH=1, so a normal "go test ./..." stays fast. Data is
// generated in a temporary directory and removed afterwards; nothing is
// committed. See docs/superpowers/plans/2026-10-25-performance-notes.md for how
// to run it and for the results.
package perfbench
