//go:build windows

package discover

import (
	"context"
	"strings"
	"testing"
)

func TestSystemEnvRealChannels(t *testing.T) {
	env, err := NewSystemEnv()
	if err != nil {
		t.Fatal(err)
	}
	names, err := env.Channels.List()
	if IsAccessDenied(err) {
		t.Skip("channel listing denied")
	}
	if err != nil || len(names) < 2 {
		t.Fatalf("List: %d names, %v", len(names), err)
	}
	if err := env.Channels.CanRead("No-Such-Channel/Operational"); !IsNotFound(err) {
		t.Fatalf("missing channel: %v", err)
	}
	if err := env.Channels.CanRead("Security"); err == nil {
		t.Fatal("Security channel probe succeeded")
	}
	if err := env.Channels.CanRead("Application"); err != nil && !IsAccessDenied(err) {
		t.Fatalf("Application: %v", err)
	}

	// The detector over the real Env: every wanted channel is a finding or a
	// gap with a reason, never silence, and never Security.
	fs, gs := NewEventsDetector().Detect(context.Background(), env)
	if len(fs)+len(gs) != 3 {
		t.Fatalf("findings %d gaps %v", len(fs), gs)
	}
	for _, f := range fs {
		if strings.Contains(strings.ToLower(f.Display), "security") {
			t.Fatalf("security offered: %+v", f)
		}
	}
	for _, g := range gs {
		if g.Reason != ReasonNeedsElevation && g.Reason != ReasonNotFound {
			t.Errorf("unexpected gap %+v", g)
		}
	}
}
