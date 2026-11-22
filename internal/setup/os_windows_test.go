//go:build windows

package setup

import (
	"os"
	"testing"
)

func TestProcessSIDOfThisProcessIsTheCurrentUser(t *testing.T) {
	want, err := CurrentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	if got := processSID(uint32(os.Getpid())); got != want {
		t.Fatalf("processSID of this process = %q, want %q", got, want)
	}
}

func TestProcessSIDOfAGoneProcessIsUnknown(t *testing.T) {
	if got := processSID(0xFFFFFFF0); got != UnknownOwner {
		t.Fatalf("processSID of a process that is not there = %q, want %q", got, UnknownOwner)
	}
}
