//go:build windows

package main

import (
	"errors"
	"testing"

	"golang.org/x/sys/windows"
)

func TestIsElevatedAgreesWithTheTokenFlag(t *testing.T) {
	if !windows.GetCurrentProcessToken().IsElevated() && isElevated() {
		t.Fatal("isElevated reported true for a token that is not elevated")
	}
}

func TestElevationDecision(t *testing.T) {
	cases := []struct {
		name     string
		elevated bool
		kind     uint32
		err      error
		want     bool
	}{
		{"full token is elevated", true, tokenElevationTypeFull, nil, true},
		{"limited token is not elevated", false, tokenElevationTypeLimited, nil, false},
		{"default token, not elevated", false, tokenElevationTypeDefault, nil, false},
		{"default token, elevated flag (UAC off)", true, tokenElevationTypeDefault, nil, false},
		{"read error fails closed", false, 0, errors.New("access denied"), true},
		{"read error with elevated flag fails closed", true, 0, errors.New("access denied"), true},
	}
	for _, c := range cases {
		if got := elevationDecision(c.elevated, c.kind, c.err); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
