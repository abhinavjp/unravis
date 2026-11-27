package keeper

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"api-audit-explorer/internal/sqlconn"
)

// takeAs runs sql.handover.take as another client, the way the elevated Blackbox helper does.
func takeAs(c *Client, id string) (loginDoc, error) {
	var raw json.RawMessage
	err := c.Call(context.Background(), OpSQLHandoverTake, HandoverArgs{ID: id}, &raw)
	var d loginDoc
	if err == nil {
		err = json.Unmarshal(raw, &d)
	}
	return d, err
}

type loginDoc struct{ Server, User, Password string }

// helperClient is a second client in the same Windows session under another account (an administrator who typed their password at the
// elevation prompt), or in another session.
func (h *harness) helperClient(sid string, session uint32) *Client {
	return NewClient(recordingDialer{inner: h.server.slotDialer(h.l, Caller{SID: sid, Name: `PC\helper`, PID: 5151, Session: session}), buf: h.wire})
}

func isRefused(err error) bool {
	var we *WireError
	return errors.As(err, &we) && we.Code == CodeRefused
}

func TestHandoverGivesTheLoginOnceAndRefusesASecondRead(t *testing.T) {
	h := newHarness(t, nil)
	s := NewSQLStore(h.client)
	rec := h.saveSQL(sqlconn.AuthSQL)

	ho, err := s.HandoverSaved(context.Background(), SQLRef{ID: rec.ID})
	if err != nil || len(ho.ID) != 32 {
		t.Fatalf("hand-over: %+v %v", ho, err)
	}
	// The answer to the person's Unravis holds no part of the login.
	raw, _ := json.Marshal(ho)
	if strings.Contains(string(raw), sqlSecret) || strings.Contains(string(raw), "auditor") {
		t.Fatalf("the answer to the app holds the login: %s", raw)
	}
	h.noSecrets()
	if got := h.backend.handovers().held(); got != 1 {
		t.Fatalf("held %d", got)
	}

	// The elevated helper is another account in the same session.
	helper := h.helperClient("S-1-5-21-1000-1000-1000-500", 1)
	d, err := takeAs(helper, ho.ID)
	if err != nil || d.Password != sqlSecret || d.User != "auditor" || d.Server != "sql01.example.test,1433" {
		t.Fatalf("the helper did not get the saved login: %v", err)
	}
	if _, err := takeAs(helper, ho.ID); !isRefused(err) {
		t.Fatalf("a second read must be refused: %v", err)
	}
	if _, err := takeAs(h.client, ho.ID); !isRefused(err) {
		t.Fatalf("a second read by the person must be refused: %v", err)
	}
	if got := h.backend.handovers().held(); got != 0 {
		t.Fatalf("the keeper still holds %d after the take", got)
	}
}

func TestHandoverExpiredIsRefusedAndForgotten(t *testing.T) {
	h := newHarness(t, nil)
	var mu sync.Mutex
	now := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	h.backend.hand = newHandoverStore(HandoverLife, clock)
	rec := h.saveSQL(sqlconn.AuthSQL)

	ho, err := NewSQLStore(h.client).HandoverSaved(context.Background(), SQLRef{ID: rec.ID})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	now = now.Add(HandoverLife + time.Second)
	mu.Unlock()
	if _, err := takeAs(h.client, ho.ID); !isRefused(err) {
		t.Fatalf("an expired hand-over must be refused: %v", err)
	}
	if got := h.backend.handovers().held(); got != 0 {
		t.Fatalf("an expired hand-over is still held: %d", got)
	}
}

func TestHandoverTimerForgetsAnUnusedLogin(t *testing.T) {
	st := newHandoverStore(20*time.Millisecond, nil)
	doc := []byte("login-bytes")
	id, err := st.put(1, "conn", doc)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for st.held() != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if st.held() != 0 {
		t.Fatal("the timer did not forget the hand-over")
	}
	if strings.Trim(string(doc), "\x00") != "" {
		t.Fatal("the forgotten login was not zeroed")
	}
	if _, _, ok := st.take(id, 1); ok {
		t.Fatal("took a forgotten hand-over")
	}
}

func TestHandoverFromAnotherSessionIsRefusedAndDoesNotUseItUp(t *testing.T) {
	h := newHarness(t, nil)
	rec := h.saveSQL(sqlconn.AuthSQL)
	ho, err := NewSQLStore(h.client).HandoverSaved(context.Background(), SQLRef{ID: rec.ID})
	if err != nil {
		t.Fatal(err)
	}
	other := h.helperClient(testSID, 2)
	_, errOther := takeAs(other, ho.ID)
	_, errUnknown := takeAs(other, "00000000000000000000000000000000")
	if !isRefused(errOther) || !isRefused(errUnknown) {
		t.Fatalf("another session must be refused: %v / %v", errOther, errUnknown)
	}
	// The refusals are word for word the same, so they cannot tell a guesser which ids exist.
	if errOther.Error() != errUnknown.Error() {
		t.Fatalf("the refusals differ:\n%v\n%v", errOther, errUnknown)
	}
	// The wrong guess did not use up the real one.
	if d, err := takeAs(h.client, ho.ID); err != nil || d.Password != sqlSecret {
		t.Fatalf("the real take failed after a wrong guess: %v", err)
	}
}

func TestHandoverNeedsASessionAndASqlLogin(t *testing.T) {
	h := newHarness(t, nil)
	s := NewSQLStore(h.client)
	win := h.saveSQL(sqlconn.AuthWindows)
	for _, id := range []string{win.ID, "nope"} {
		if _, err := s.HandoverSaved(context.Background(), SQLRef{ID: id}); err == nil {
			t.Fatalf("%s: kept a login", id)
		}
	}
	// A caller in session 0 (a service) never gets a hand-over minted, and a take from there is refused like any other.
	rec := h.saveSQL(sqlconn.AuthSQL)
	svc := h.helperClient(testSID, 0)
	if _, err := NewSQLStore(svc).HandoverSaved(context.Background(), SQLRef{ID: rec.ID}); !isRefused(err) {
		t.Fatalf("session 0 got a hand-over: %v", err)
	}
	if got := h.backend.handovers().held(); got != 0 {
		t.Fatalf("held %d", got)
	}
}

func TestHandoverAuditLineNamesTheConnectionNeverTheHandoverId(t *testing.T) {
	h := newHarness(t, nil)
	rec := h.saveSQL(sqlconn.AuthSQL)
	ho, err := NewSQLStore(h.client).HandoverSaved(context.Background(), SQLRef{ID: rec.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := takeAs(h.helperClient(testSID, 1), ho.ID); err != nil {
		t.Fatal(err)
	}
	// A refused take is audited too.
	_, _ = takeAs(h.client, ho.ID)
	log := h.log.String()
	if !strings.Contains(log, OpSQLHandoverTake) || !strings.Contains(log, rec.ID) {
		t.Fatalf("the take is not audited with the saved connection:\n%s", log)
	}
	if strings.Contains(log, ho.ID) {
		t.Fatalf("the audit log holds the one-time id:\n%s", log)
	}
	if strings.Contains(log, sqlSecret) {
		t.Fatal("the audit log holds the password")
	}
}

func TestHandoverANewOneReplacesTheSessionsOlderAndRevokeForgets(t *testing.T) {
	h := newHarness(t, nil)
	s := NewSQLStore(h.client)
	rec := h.saveSQL(sqlconn.AuthSQL)
	first, err := s.HandoverSaved(context.Background(), SQLRef{ID: rec.ID})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.HandoverSaved(context.Background(), SQLRef{ID: rec.ID})
	if err != nil || second.ID == first.ID {
		t.Fatalf("second: %+v %v", second, err)
	}
	if got := h.backend.handovers().held(); got != 1 {
		t.Fatalf("held %d, want the newest only", got)
	}
	if _, err := takeAs(h.client, first.ID); !isRefused(err) {
		t.Fatalf("the replaced hand-over was served: %v", err)
	}

	// Revoke by another session does nothing; by the owner it forgets; both answer the same.
	if err := NewSQLStore(h.helperClient(testSID, 2)).RevokeHandover(context.Background(), second.ID); err != nil {
		t.Fatal(err)
	}
	if got := h.backend.handovers().held(); got != 1 {
		t.Fatalf("another session revoked it: %d", got)
	}
	if err := s.RevokeHandover(context.Background(), second.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeHandover(context.Background(), second.ID); err != nil {
		t.Fatalf("revoke twice: %v", err)
	}
	if got := h.backend.handovers().held(); got != 0 {
		t.Fatalf("held %d after revoke", got)
	}
	if _, err := takeAs(h.client, second.ID); !isRefused(err) {
		t.Fatalf("a revoked hand-over was served: %v", err)
	}
}

func TestHandoverStoreIsBoundedAndWipesWhatItDrops(t *testing.T) {
	st := newHandoverStore(HandoverLife, nil)
	docs := make([][]byte, 0, maxHandovers)
	for i := 0; i < maxHandovers; i++ {
		d := []byte("secret-login")
		docs = append(docs, d)
		if _, err := st.put(uint32(i+1), "c", d); err != nil {
			t.Fatalf("put %d: %v", i, err)
		}
	}
	extra := []byte("secret-login")
	if _, err := st.put(1000, "c", extra); err == nil {
		t.Fatal("the store grew past its bound")
	}
	if strings.Trim(string(extra), "\x00") != "" {
		t.Fatal("a refused login was not zeroed")
	}
	// A new one from a session that already has one replaces it and wipes the old one.
	repl := []byte("secret-login")
	if _, err := st.put(1, "c", repl); err != nil {
		t.Fatal(err)
	}
	if strings.Trim(string(docs[0]), "\x00") != "" {
		t.Fatal("a replaced login was not zeroed")
	}
	// What was taken is zeroed once it has been encoded for the pipe.
	taken := []byte(`{"password":"x"}`)
	h := &handoverDoc{doc: taken}
	out, err := h.MarshalJSON()
	if err != nil || string(out) != `{"password":"x"}` {
		t.Fatalf("marshal: %q %v", out, err)
	}
	if strings.Trim(string(taken), "\x00") != "" {
		t.Fatal("the keeper's copy was not zeroed after encoding")
	}
}

func TestHandoverWritesNoFileAnywhereInATempFolder(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMP", tmp)
	t.Setenv("TEMP", tmp)
	t.Setenv("TMPDIR", tmp)
	h := newHarness(t, nil)
	rec := h.saveSQL(sqlconn.AuthSQL)
	ho, err := NewSQLStore(h.client).HandoverSaved(context.Background(), SQLRef{ID: rec.ID})
	if err != nil {
		t.Fatal(err)
	}
	if d, err := takeAs(h.helperClient(testSID, 1), ho.ID); err != nil || d.Password != sqlSecret {
		t.Fatalf("take: %v", err)
	}
	if err := NewSQLStore(h.client).RevokeHandover(context.Background(), ho.ID); err != nil {
		t.Fatal(err)
	}
	if ents, _ := os.ReadDir(tmp); len(ents) != 0 {
		t.Fatalf("the hand-over left %d entries in the temp folder", len(ents))
	}
}
