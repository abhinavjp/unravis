package applog

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
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
		"X-Api-Key", "db_pass", "cookie", "Cookie", "set-cookie", "Set-Cookie", "credentials", "credential", "cred", "private_key", "privatekey",
		"passphrase", "key_passphrase", "dsn", "DSN", "connection_string", "ConnectionString", "pgpass", "PGPASS",
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

// A group, or a WithGroup, named like a secret hides everything below it.
func TestSanitizeAttrHidesSecretGroups(t *testing.T) {
	newLogger := func(buf *bytes.Buffer) *slog.Logger {
		return slog.New(sanitizeHandler{inner: slog.NewTextHandler(buf, nil)})
	}
	t.Run("Group", func(t *testing.T) {
		var buf bytes.Buffer
		newLogger(&buf).Info("m", slog.Group("password", "value", "Secr3tG", "n", 5), slog.Group("db", "host", "h1"))
		got := buf.String()
		if strings.Contains(got, "Secr3tG") || strings.Contains(got, "password.n=5") || !strings.Contains(got, "db.host=h1") {
			t.Errorf("line %q", got)
		}
	})
	t.Run("WithGroup", func(t *testing.T) {
		var buf bytes.Buffer
		newLogger(&buf).WithGroup("credentials").Info("m", "value", "Secr3tW")
		if got := buf.String(); strings.Contains(got, "Secr3tW") {
			t.Errorf("line %q", got)
		}
	})
	t.Run("WithGroup then With", func(t *testing.T) {
		var buf bytes.Buffer
		newLogger(&buf).WithGroup("secret").With("value", "Secr3tWW").Info("m")
		if got := buf.String(); strings.Contains(got, "Secr3tWW") {
			t.Errorf("line %q", got)
		}
	})
	t.Run("a plain group is untouched", func(t *testing.T) {
		var buf bytes.Buffer
		newLogger(&buf).WithGroup("db").Info("m", "host", "h1")
		if got := buf.String(); !strings.Contains(got, "db.host=h1") {
			t.Errorf("line %q", got)
		}
	})
}

// Header-like values hide the secret entries by key, whatever the value looks like.
func TestSanitizeAttrHidesSecretHeaderValues(t *testing.T) {
	for name, val := range map[string]any{
		"http.Header":         http.Header{"X-Api-Key": {"Secr3tH"}, "Accept": {"*/*"}},
		"map of string lists": map[string][]string{"Authorization": {"Secr3tH"}, "Accept": {"*/*"}},
		"map of strings":      map[string]string{"Set-Cookie": "Secr3tH", "Accept": "*/*"},
		"cookie header":       http.Header{"Cookie": {"sid=Secr3tH"}, "Accept": {"*/*"}},
	} {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			slog.New(sanitizeHandler{inner: slog.NewTextHandler(&buf, nil)}).Info("m", "headers", val)
			got := buf.String()
			if strings.Contains(got, "Secr3tH") || !strings.Contains(got, "Accept") {
				t.Errorf("line %q", got)
			}
		})
	}
}

// A nil pointer whose MarshalText or Error has a value receiver must not panic the logger.
func TestSanitizeAttrNilPointerValues(t *testing.T) {
	var tp *time.Time
	var buf bytes.Buffer
	l := slog.New(sanitizeHandler{inner: slog.NewTextHandler(&buf, nil)})
	l.Info("m", "t", tp, "err", error((*nilError)(nil)))
	if got := buf.String(); !strings.Contains(got, "t=") || !strings.Contains(got, "err=") {
		t.Errorf("line %q", got)
	}
}

type nilError struct{ msg string }

func (e nilError) Error() string { return e.msg }

// Every attribute pays for the key check, so it must not allocate for an ordinary key.
func TestSanitizeAttrKeyCheckIsCheap(t *testing.T) {
	a := slog.Int("rows", 3)
	if n := testing.AllocsPerRun(100, func() { _ = sanitizeAttr(a) }); n > 1 {
		t.Errorf("%v allocations for an int attribute", n)
	}
	b := slog.Int("db_rows", 3)
	if n := testing.AllocsPerRun(100, func() { _ = sanitizeAttr(b) }); n > 2 {
		t.Errorf("%v allocations for a key with a separator", n)
	}
}

func BenchmarkSanitizeAttrInt(b *testing.B) {
	a := slog.Int("rows", 3)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = sanitizeAttr(a)
	}
}
