//go:build windows

package main

import (
	"errors"
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

// On the CI runner (an administrator with UAC off, spec 08 D4) the token type is
// default, so the gate must let it run (spec 01 D13). This proves the allow path
// on a live token; the refuse path on a live full token is a manual check.
func TestUACOffAdminIsNotRefusedOnCI(t *testing.T) {
	if os.Getenv("GITHUB_ACTIONS") != "true" {
		t.Skip("only meaningful on the CI runner, whose token is a UAC-off administrator")
	}
	if isElevated() {
		t.Fatal("the CI runner's token was refused; a UAC-off administrator must run (spec 01 D13)")
	}
}

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
		level    uint32
		err      error
		want     bool
	}{
		{"full token is elevated", true, tokenElevationTypeFull, 0x3000, nil, true},
		{"full token, flag only", true, tokenElevationTypeFull, 0x2000, nil, true},
		{"full token, high integrity only", false, tokenElevationTypeFull, 0x3000, nil, true},
		{"limited token is not elevated", false, tokenElevationTypeLimited, 0x2000, nil, false},
		{"default token, not elevated", false, tokenElevationTypeDefault, 0x2000, nil, false},
		{"UAC-off admin is allowed (elevated, high integrity)", true, tokenElevationTypeDefault, 0x3000, nil, false},
		{"service token (system integrity) is not refused", true, tokenElevationTypeDefault, 0x4000, nil, false},
		{"read error fails closed", false, 0, 0, errors.New("access denied"), true},
		{"read error with elevated flag fails closed", true, 0, 0x3000, errors.New("access denied"), true},
	}
	for _, c := range cases {
		if got := elevationDecision(c.elevated, c.kind, c.level, c.err); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestIntegrityLevelReadable(t *testing.T) {
	level, err := integrityLevel(windows.GetCurrentProcessToken())
	if err != nil {
		t.Fatal(err)
	}
	if level < 0x1000 {
		t.Fatalf("integrity level %#x is below Low", level)
	}
}
