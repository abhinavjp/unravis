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

// The attribute key says the value is a secret, whatever the value looks like.
func TestSanitizeAttrRedactsByKey(t *testing.T) {
	for _, key := range []string{
		"password", "Password", "passwd", "pwd", "secret", "token", "apikey", "api_key", "api-key", "ApiKey", "accesskey", "access_key",
		"accountkey", "sharedaccesskey", "authorization", "Authorization", "DB_PASSWORD", "mysql.pwd", "client_secret", "refresh_token",
		"X-Api-Key", "db_pass",
	} {
		t.Run(key, func(t *testing.T) {
			var buf bytes.Buffer
			slog.New(sanitizeHandler{inner: slog.NewTextHandler(&buf, nil)}).Info("x", key, "hunter two", "other", "kept")
			got := buf.String()
			if strings.Contains(got, "hunter") || !strings.Contains(got, key+"="+redacted) || !strings.Contains(got, "other=kept") {
				t.Errorf("line %q", got)
			}
		})
	}
	t.Run("not a string", func(t *testing.T) {
		var buf bytes.Buffer
		slog.New(sanitizeHandler{inner: slog.NewTextHandler(&buf, nil)}).Info("x", "pin_token", 123456, "pwd", []byte("hunter two"))
		if got := buf.String(); strings.Contains(got, "123456") || strings.Contains(got, "hunter") {
			t.Errorf("line %q", got)
		}
	})
	t.Run("With attributes and groups", func(t *testing.T) {
		var buf bytes.Buffer
		l := slog.New(sanitizeHandler{inner: slog.NewTextHandler(&buf, nil)}).With("password", "hunter two")
		l.Info("x", slog.Group("db", "pwd", "a b", "host", "h1"))
		got := buf.String()
		if strings.Contains(got, "hunter") || strings.Contains(got, "a b") || !strings.Contains(got, "db.host=h1") {
			t.Errorf("line %q", got)
		}
	})
	for _, key := range []string{"tokens", "passwordPolicy", "bypass", "host", "key", "message", "tokenCount"} {
		t.Run("stays "+key, func(t *testing.T) {
			var buf bytes.Buffer
			slog.New(sanitizeHandler{inner: slog.NewTextHandler(&buf, nil)}).Info("x", key, "plain")
			if got := buf.String(); !strings.Contains(got, key+"=plain") {
				t.Errorf("line %q", got)
			}
		})
	}
	t.Run("empty value stays visible", func(t *testing.T) {
		var buf bytes.Buffer
		slog.New(sanitizeHandler{inner: slog.NewTextHandler(&buf, nil)}).Info("x", "password", "")
		if got := buf.String(); !strings.Contains(got, `password=""`) {
			t.Errorf("line %q", got)
		}
	})
}
