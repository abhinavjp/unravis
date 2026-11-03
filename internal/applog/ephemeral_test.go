package applog

import (
	"fmt"
	"strings"
	"testing"
)

// resetEphemeral empties the ephemeral list now and again when the test ends.
func resetEphemeral(t *testing.T) {
	t.Helper()
	reset := func() {
		secretsMu.Lock()
		ephemeral = nil
		secretsMu.Unlock()
	}
	reset()
	t.Cleanup(reset)
}

func ephemeralValue(i int) string { return fmt.Sprintf("launchcode-%05d", i) } // 16 characters

func TestEphemeralSecretsAreBounded(t *testing.T) {
	resetEphemeral(t)
	for i := 0; i < 100; i++ {
		RegisterEphemeralSecret(ephemeralValue(i))
	}
	secretsMu.RLock()
	defer secretsMu.RUnlock()
	if len(ephemeral) != 8 {
		t.Fatalf("ephemeral holds %d values, want 8", len(ephemeral))
	}
	for i, v := range ephemeral {
		if want := ephemeralValue(92 + i); v != want {
			t.Errorf("ephemeral[%d] = %q, want %q", i, v, want)
		}
	}
}

func TestEphemeralSecretIsRedacted(t *testing.T) {
	resetEphemeral(t)
	v := ephemeralValue(1)
	RegisterEphemeralSecret(v)
	if got := Sanitize("open /launch?c=" + v + " now"); strings.Contains(got, v) {
		t.Errorf("Sanitize kept the value: %q", got)
	}
}

func TestEphemeralOldestIsForgotten(t *testing.T) {
	resetEphemeral(t)
	for i := 1; i <= 9; i++ {
		RegisterEphemeralSecret(ephemeralValue(i))
	}
	if got := Sanitize("x " + ephemeralValue(1)); !strings.Contains(got, ephemeralValue(1)) {
		t.Errorf("the 1st value is still replaced: %q", got)
	}
	if got := Sanitize("x " + ephemeralValue(9)); strings.Contains(got, ephemeralValue(9)) {
		t.Errorf("the 9th value was not replaced: %q", got)
	}
}

func TestEphemeralIgnoresShortAndDuplicateValues(t *testing.T) {
	resetEphemeral(t)
	RegisterEphemeralSecret("short")
	RegisterEphemeralSecret(ephemeralValue(1))
	RegisterEphemeralSecret(ephemeralValue(1))
	secretsMu.RLock()
	defer secretsMu.RUnlock()
	if len(ephemeral) != 1 {
		t.Errorf("ephemeral holds %d values, want 1", len(ephemeral))
	}
}
