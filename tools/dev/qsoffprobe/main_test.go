package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/dbhealth"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func neverOpen(t *testing.T) opener {
	return func(context.Context, dbhealth.SQLTarget) (dbhealth.Queryer, func(), error) {
		t.Fatal("the probe tried to connect")
		return nil, nil, nil
	}
}

func TestRefusesWithoutOptIn(t *testing.T) {
	var out bytes.Buffer
	err := run([]string{"-database", "AuditDb"}, env(nil), &out, neverOpen(t), time.Now)
	if !errors.Is(err, errNoOptIn) || !strings.Contains(err.Error(), envServer) || out.Len() != 0 {
		t.Fatalf("err = %v out = %q", err, out.String())
	}
	// A database name or a user alone is not an opt-in.
	err = run(nil, env(map[string]string{envDB: "AuditDb", envUser: "sa"}), &out, neverOpen(t), time.Now)
	if !errors.Is(err, errNoOptIn) {
		t.Fatalf("err = %v", err)
	}
}

func TestNeedsDatabaseName(t *testing.T) {
	err := run(nil, env(map[string]string{envServer: `localhost\SQLEXPRESS`}), &bytes.Buffer{}, neverOpen(t), time.Now)
	if err == nil || !strings.Contains(err.Error(), "name the database") {
		t.Fatalf("err = %v", err)
	}
}

func TestTargetFromEnv(t *testing.T) {
	tg, err := targetFromEnv(env(map[string]string{envServer: "h,1433"}), "D")
	if err != nil || tg.Auth != "windows" || tg.Database != "D" || tg.Username != "" || tg.TrustServerCertificate {
		t.Fatalf("windows: %+v %v", tg, err)
	}
	tg, _ = targetFromEnv(env(map[string]string{envServer: "h", envUser: "u", envLoginKey: "p", envTrust: "1"}), "D")
	if tg.Auth != "sql" || tg.Username != "u" || tg.Password != "p" || !tg.TrustServerCertificate {
		t.Fatalf("sql: %+v", tg)
	}
}

func TestConnectFailureIsPlain(t *testing.T) {
	open := func(context.Context, dbhealth.SQLTarget) (dbhealth.Queryer, func(), error) {
		return nil, nil, &dbhealth.ConnectError{Code: "unreachable", Message: "Could not reach the server."}
	}
	err := run([]string{"-database", "D"}, env(map[string]string{envServer: "h"}), &bytes.Buffer{}, open, time.Now)
	if err == nil || !strings.Contains(err.Error(), "could not connect: Could not reach the server.") {
		t.Fatalf("err = %v", err)
	}
}

// fake answers the app's fixed texts by their /* unravis:... */ marker, like the repo's lane tests do.
type fake map[string][]dbhealth.Row

func (f fake) Query(_ context.Context, text string) ([]dbhealth.Row, error) {
	for k, r := range f {
		if strings.Contains(text, k) {
			return r, nil
		}
	}
	return nil, nil
}

func TestPrintsTheProbeForAnOffDatabase(t *testing.T) {
	now := time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC)
	first, last := now.Add(-30*24*time.Hour), now.Add(-5*24*time.Hour)
	q := fake{
		"unravis:base":       {{"version": "16.0.4135.4", "edition": "Developer Edition", "hadr": int64(0), "utc_offset_min": int64(0)}},
		"unravis:qs_options": {{"actual_state_desc": "OFF", "desired_state_desc": "OFF", "readonly_reason": int64(0), "interval_length_minutes": int64(60)}},
		"unravis:qs_range":   {{"first_start": first, "last_start": last}},
		"unravis:qs_intervals": {
			{"start_time": last.Add(-2 * time.Hour), "execs": int64(40), "avg_ms": 12.0, "win": "inc"},
			{"start_time": last.Add(-time.Hour), "execs": int64(60), "avg_ms": 13.0, "win": "inc"},
		},
	}
	open := func(_ context.Context, tg dbhealth.SQLTarget) (dbhealth.Queryer, func(), error) {
		if tg.Database != "OffDb" || tg.Server != `PC\SQL` {
			t.Errorf("target = %+v", tg)
		}
		return q, func() {}, nil
	}
	var out bytes.Buffer
	if err := run([]string{"-database", "OffDb"}, env(map[string]string{envServer: `PC\SQL`}), &out, open, func() time.Time { return now }); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	for _, want := range []string{
		"Server version:              16.0.4135.4 Developer Edition",
		"Database:                    OffDb",
		"Query Store desired state:   OFF",
		"Query Store actual state:    OFF",
		"Lane state:                  off",
		"First kept block (start):    " + first.Format("2006-01-02 15:04") + " UTC",
		"Last kept block (start):     " + last.Format("2006-01-02 15:04") + " UTC",
		"Rows read (blocks):          2 (100 executions)",
		"Texts refused by the read-only guard: 0",
		"VERDICT: " + dbhealth.VerdictRows,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output lacks %q:\n%s", want, s)
		}
	}
}
