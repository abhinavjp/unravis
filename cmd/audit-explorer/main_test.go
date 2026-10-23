package main

import (
	"strings"
	"testing"
	"time"
)

func TestDefaultPort(t *testing.T) {
	if defaultPort != 18765 {
		t.Fatalf("defaultPort=%d, want 18765", defaultPort)
	}
}

func TestSessionTokenUsesExplicitValueWithoutChangingIt(t *testing.T) {
	want := strings.Repeat("x", 40)
	got, err := sessionToken(func(name string) string {
		if name == "AUDIT_SESSION_TOKEN" {
			return want
		}
		return ""
	})
	if err != nil || got != want {
		t.Fatalf("sessionToken()=%q, %v", got, err)
	}
}

func TestSessionTokenRejectsShortExplicitValue(t *testing.T) {
	if _, err := sessionToken(func(string) string { return strings.Repeat("x", 31) }); err == nil {
		t.Fatal("expected short token to be rejected")
	}
	if _, err := sessionToken(func(string) string { return strings.Repeat("雪", 16) }); err == nil {
		t.Fatal("byte length must not substitute for the required character count")
	}
}

func TestSessionTokenRejectsHeaderControlCharacters(t *testing.T) {
	if _, err := sessionToken(func(string) string { return strings.Repeat("x", 31) + "\n" }); err == nil {
		t.Fatal("expected control character to be rejected")
	}
}

func TestSessionTokenGeneratesUnloggedRandomValue(t *testing.T) {
	got, err := sessionToken(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 32 {
		t.Fatalf("generated token length=%d", len(got))
	}
	other, err := sessionToken(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if got == other {
		t.Fatal("independent generated tokens unexpectedly matched")
	}
}

func TestHTTPEnvelopeIncludesReadAndFiveMinuteOperation(t *testing.T) {
	if httpWriteTimeout < httpReadTimeout+5*time.Minute+10*time.Second {
		t.Fatal("HTTP response deadline clips the operation after a slow request body")
	}
}
