//go:build !windows

package server

import "testing"

func shortPathOf(t *testing.T, p string) string { return "" }
