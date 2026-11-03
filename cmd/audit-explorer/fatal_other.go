//go:build !windows

package main

func showMessageBox(title, text string) {}

func stderrIsUnusable() bool { return false }
