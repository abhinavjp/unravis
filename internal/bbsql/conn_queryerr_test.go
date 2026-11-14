package bbsql

// H1 of the wave-10 review: the C7/C10 classification was dead on the
// production path, because conn.go flattened every driver error into one
// generic code before the runner could see it. Then fail()'s permission test
// could only ever fire for the one error the ceiling check builds itself, and a
// login that lost rights kept the service sampling every 15 s instead of going
// denied and auto-off. These tests go through the real helpers with a real
// driver error, never a pre-classified one.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	mssql "github.com/microsoft/go-mssqldb"
)

func TestFixedQueryErrorKeepsTheDriverCodes(t *testing.T) {
	ctx := context.Background()
	// What survives is the C7 decision, not the driver's prose: a rights or
	// login fact goes to the code that means "stop and wait for the owner",
	// and everything that is not such a fact becomes CodeQuery - one honest
	// code for "this sample did not run", including a deadlock, which the
	// classifier alone would have called CodeFailed.
	for name, tc := range map[string]struct {
		err  error
		code string
	}{
		"permission denied 229": {mssql.Error{Number: 229, Message: `The SELECT permission was denied on the object 'dm_exec_requests', database 'mssqlsystemresource', schema 'sys'.`}, CodePermission},
		"permission denied 230": {mssql.Error{Number: 230, Message: `Permission denied.`}, CodePermission},
		"metadata 916":          {mssql.Error{Number: 916, Message: `The server principal is not able to access the database.`}, CodePermission},
		"login failed 18456":    {mssql.Error{Number: 18456, Message: `Login failed for user 'CORP\svc-blackbox'.`}, CodeLoginFailed},
		"cannot open db 4060":   {mssql.Error{Number: 4060, Message: `Cannot open database "ProdFin" requested by the login.`}, CodeDatabase},
		"plain text permission": {errors.New("permission was denied for server principal"), CodePermission},
		"deadlock phrase":       {errors.New("transaction (process id 61) was deadlocked"), CodeQuery},
		"unreadable result":     {errors.New("something the classifier has never seen"), CodeQuery},
	} {
		got := fixedQueryError(ctx, tc.err)
		var e *Error
		if !errors.As(got, &e) || e.Code != tc.code {
			t.Errorf("%s: fixedQueryError = %#v, want code %q", name, got, tc.code)
			continue
		}
		for _, secret := range []string{"svc-blackbox", "ProdFin", "dm_exec_requests", "mssqlsystemresource", "process id 61"} {
			if strings.Contains(e.Message, secret) {
				t.Errorf("%s: the fixed message leaks %q: %q", name, secret, e.Message)
			}
		}
	}
}

// A query stopped by its own budget is a timeout, whatever the driver decided
// to call it: C4's budget is the reason, and C7 must not read it as a
// permission problem.
func TestFixedQueryErrorUsesTheContextReason(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	got := fixedQueryError(canceled, mssql.Error{Number: 1205, Message: `deadlock`})
	var e *Error
	if !errors.As(got, &e) || e.Code != CodeTimeout {
		t.Fatalf("err = %#v, want %q (the budget expired, so the budget is the answer)", got, CodeTimeout)
	}
	// An expired budget with no error at all is still the budget's fault.
	deadline, dfail := context.WithTimeout(context.Background(), time.Nanosecond)
	defer dfail()
	time.Sleep(2 * time.Millisecond)
	if err := fixedQueryError(deadline, context.DeadlineExceeded); !errors.As(err, &e) || e.Code != CodeTimeout {
		t.Fatalf("err = %#v, want %q", err, CodeTimeout)
	}
}

// The end-to-end half of H1: a raw driver permission error at the send seam,
// converted by the same helper production uses, must reach denied + auto-off.
// TestPermissionFailureIsDeniedAndOwnerOnly injects an already-classified
// error, so it cannot see this regression.
func TestRawPermissionErrorReachesDenied(t *testing.T) {
	now := time.Date(2026, 3, 4, 9, 15, 0, 0, time.UTC)
	drvErr := mssql.Error{Number: 229, Message: `The SELECT permission was denied on the object 'dm_exec_requests', database 'mssqlsystemresource', schema 'sys'.`}
	q := &recQuery{replies: map[string][]Row{Statements["bb:waits"]: fakeWaitRows(1)}}
	q.errFor = func(text string) error {
		if text == Statements["bb:ceiling_role"] {
			return fixedQueryError(context.Background(), drvErr)
		}
		return nil
	}
	r := newFakeRunner(t, q, runnerOpts{
		now:      now,
		samplers: []Sampler{baseSampler("bb:waits", "waits")},
	})
	err := r.Tick(now)
	var e *Error
	if !errors.As(err, &e) || e.Code != CodePermission {
		t.Fatalf("tick error = %v, want code %q", err, CodePermission)
	}
	if strings.Contains(e.Message, "dm_exec_requests") || strings.Contains(e.Message, "sys") {
		t.Errorf("the driver text survived: %q", e.Message)
	}
	st := r.CurrentState()
	if !st.AutoOff || st.State != StateDenied {
		t.Fatalf("state = %+v, want auto-off/denied (C7 must stop the loop, not back off)", st)
	}
	if status := readStatusRaw(t, r.Dir); status.Enabled || status.State != StateDenied {
		t.Errorf("status = %+v, want disabled/denied", status)
	}
}
