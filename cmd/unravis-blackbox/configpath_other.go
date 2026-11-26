//go:build !windows

package main

func driveIsFixed(byte) bool { return true }
