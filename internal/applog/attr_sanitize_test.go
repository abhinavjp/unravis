package applog

import (
	"bytes"
	"log/slog"
	"net/url"
	"strings"
	"testing"
)

type leakyStringer struct{ v string }

func (l leakyStringer) String() string { return l.v }

type leakyMarshaler struct{ v string }

func (l leakyMarshaler) MarshalText() ([]byte, error) { return []byte(l.v), nil }

type leakyStruct struct {
	Endpoint string
	Note     string
}

// A secret inside a non-string attribute value (Stringer, URL, slice, map,
// struct, bytes, text marshaler) must be cleaned like a string one: the handler
// would otherwise format the value itself, after sanitizing was skipped.
func TestSanitizeAttrCleansNonStringValues(t *testing.T) {
	const pass = "Zq9HiddenPass77"
	credURL := "mongodb://svc:" + pass + "@db.corp.local:27017/audit"
	u, err := url.Parse(credURL)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		val  any
	}{
		{"stringer", leakyStringer{"dial " + credURL}},
		{"url pointer", u},
		{"url value", *u},
		{"string slice", []string{"ok", credURL}},
		{"map", map[string]string{"dsn": credURL}},
		{"struct", leakyStruct{Endpoint: credURL, Note: "x"}},
		{"bytes", []byte("connect " + credURL)},
		{"text marshaler", leakyMarshaler{"password=" + pass}},
		{"any slice", []any{1, "token=" + pass}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			h := sanitizeHandler{inner: slog.NewTextHandler(&buf, nil)}
			slog.New(h).Info("event", "value", tc.val)
			if got := buf.String(); strings.Contains(got, pass) {
				t.Fatalf("the secret reached the log line: %q", got)
			}
		})
	}
}

// The same holds for attributes bound with With and for attributes inside a
// group.
func TestSanitizeAttrCleansNonStringValuesInGroupsAndWith(t *testing.T) {
	const pass = "Zq9HiddenPass77"
	val := leakyStringer{"password=" + pass}
	var buf bytes.Buffer
	h := sanitizeHandler{inner: slog.NewTextHandler(&buf, nil)}
	l := slog.New(h).With("bound", val)
	l.Info("event", slog.Group("g", slog.Any("inner", val)))
	if got := buf.String(); strings.Contains(got, pass) {
		t.Fatalf("the secret reached the log line: %q", got)
	}
}

// Ordinary non-string values keep their readable form.
func TestSanitizeAttrKeepsPlainValuesReadable(t *testing.T) {
	var buf bytes.Buffer
	h := sanitizeHandler{inner: slog.NewTextHandler(&buf, nil)}
	slog.New(h).Info("event", "list", []int{1, 2}, "n", 7)
	got := buf.String()
	if !strings.Contains(got, "list=\"[1 2]\"") || !strings.Contains(got, "n=7") {
		t.Fatalf("plain values changed: %q", got)
	}
}
