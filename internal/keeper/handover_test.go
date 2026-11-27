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

// helperClient is a second client with an ELEVATED token, in the same Windows session under another account (an administrator who typed
// their password at the elevation prompt), or in another session.
func (h *harness) helperClient(sid string, session uint32) *Client {
	return h.clientAs(Caller{SID: sid, Name: `PC\helper`, PID: 5151, Session: session, Elevated: true})
}

func (h *harness) clientAs(c Caller) *Client {
	return NewClient(recordingDialer{inner: h.server.slotDialer(h.l, c), buf: h.wire})
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
	if _, err := takeAs(h.helperClient(testSID, 1), ho.ID); !isRefused(err) {
		t.Fatalf("an expired hand-over must be refused: %v", err)
	}
	if got := h.backend.handovers().held(); got != 0 {
		t.Fatalf("an expired hand-over is still held: %d", got)
	}
}

func TestHandoverTimerForgetsAnUnusedLogin(t *testing.T) {
	st := newHandoverStore(20*time.Millisecond, nil)
	doc := []byte("login-bytes")
	id, err := st.put(1, "sid-a", "conn", doc)
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
	if d, err := takeAs(h.helperClient(testSID, 1), ho.ID); err != nil || d.Password != sqlSecret {
		t.Fatalf("the real take failed after a wrong guess: %v", err)
	}
}

func TestHandoverFromACallerThatIsNotElevatedIsRefusedAndDoesNotUseItUp(t *testing.T) {
	h := newHarness(t, nil)
	rec := h.saveSQL(sqlconn.AuthSQL)
	ho, err := NewSQLStore(h.client).HandoverSaved(context.Background(), SQLRef{ID: rec.ID})
	if err != nil {
		t.Fatal(err)
	}
	// Same session, same account as the person (their own Unravis, or malware running as them), and another account in the same session
	// that is not elevated: neither may read the login.
	plain := []*Client{
		h.client,
		h.clientAs(Caller{SID: testSID, Name: `PC\analyst`, PID: 6001, Session: 1}),
		h.clientAs(Caller{SID: "S-1-5-21-1000-1000-1000-2002", Name: `PC\guest`, PID: 6002, Session: 1}),
	}
	_, errUnknown := takeAs(h.helperClient(testSID, 1), "00000000000000000000000000000000")
	for i, c := range plain {
		d, err := takeAs(c, ho.ID)
		if !isRefused(err) || d.Password != "" {
			t.Fatalf("caller %d: a non-elevated take must be refused: %v", i, err)
		}
		// The refusal is word for word the one for an unknown id.
		if err.Error() != errUnknown.Error() {
			t.Fatalf("caller %d: the refusal tells it apart:\n%v\n%v", i, err, errUnknown)
		}
	}
	// Nothing was used up: the elevated helper in the same session still gets the login, once.
	if got := h.backend.handovers().held(); got != 1 {
		t.Fatalf("a refused take used the hand-over up: held %d", got)
	}
	d, err := takeAs(h.helperClient("S-1-5-21-1000-1000-1000-500", 1), ho.ID)
	if err != nil || d.Password != sqlSecret || d.User != "auditor" {
		t.Fatalf("the elevated helper in the same session did not get the login: %v", err)
	}
	if _, err := takeAs(h.helperClient("S-1-5-21-1000-1000-1000-500", 1), ho.ID); !isRefused(err) {
		t.Fatalf("a second elevated take must be refused: %v", err)
	}
}

func TestHandoverAnElevatedCallerInTheSameSessionAndAccountGetsItOnce(t *testing.T) {
	// The person who is themselves an administrator: Unravis and the helper are the same account, the helper elevated.
	h := newHarness(t, nil)
	rec := h.saveSQL(sqlconn.AuthSQL)
	ho, err := NewSQLStore(h.client).HandoverSaved(context.Background(), SQLRef{ID: rec.ID})
	if err != nil {
		t.Fatal(err)
	}
	d, err := takeAs(h.helperClient(testSID, 1), ho.ID)
	if err != nil || d.Password != sqlSecret {
		t.Fatalf("take: %v", err)
	}
	if _, err := takeAs(h.helperClient(testSID, 1), ho.ID); !isRefused(err) {
		t.Fatalf("a second read must be refused: %v", err)
	}
}

func TestHandoverAnotherAccountInTheSameSessionCannotReplaceOrRevokeIt(t *testing.T) {
	h := newHarness(t, nil)
	rec := h.saveSQL(sqlconn.AuthSQL)
	mine, err := NewSQLStore(h.client).HandoverSaved(context.Background(), SQLRef{ID: rec.ID})
	if err != nil {
		t.Fatal(err)
	}
	// Another account's process in the person's session asks for its own hand-over and tries to revoke theirs.
	intruder := NewSQLStore(h.clientAs(Caller{SID: "S-1-5-21-1000-1000-1000-2002", Name: `PC\guest`, PID: 6002, Session: 1}))
	theirs, err := intruder.HandoverSaved(context.Background(), SQLRef{ID: rec.ID})
	if err != nil || theirs.ID == mine.ID {
		t.Fatalf("the intruder's own hand-over: %+v %v", theirs, err)
	}
	if got := h.backend.handovers().held(); got != 2 {
		t.Fatalf("held %d: the intruder replaced the person's hand-over", got)
	}
	if err := intruder.RevokeHandover(context.Background(), mine.ID); err != nil {
		t.Fatal(err)
	}
	if got := h.backend.handovers().held(); got != 2 {
		t.Fatalf("held %d: the intruder revoked the person's hand-over", got)
	}
	// The person's own helper still gets the person's login.
	if d, err := takeAs(h.helperClient(testSID, 1), mine.ID); err != nil || d.Password != sqlSecret {
		t.Fatalf("the person's hand-over was lost: %v", err)
	}
}

func TestHandoverIntrudersCountAgainstTheBoundAndCannotChurnPastIt(t *testing.T) {
	st := newHandoverStore(HandoverLife, nil)
	keep := []byte("person-login")
	if _, err := st.put(1, "sid-person", "c", keep); err != nil {
		t.Fatal(err)
	}
	// Many different accounts in the same session fill the store; the person's entry is never dropped to make room.
	for i := 0; i < maxHandovers-1; i++ {
		if _, err := st.put(1, "sid-other-"+string(rune('a'+i)), "c", []byte("x")); err != nil {
			t.Fatalf("put %d: %v", i, err)
		}
	}
	if _, err := st.put(1, "sid-one-more", "c", []byte("x")); err == nil {
		t.Fatal("the store grew past its bound")
	}
	if strings.Trim(string(keep), "\x00") == "" {
		t.Fatal("the person's login was wiped by someone else's request")
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
		if _, err := st.put(uint32(i+1), "sid-a", "c", d); err != nil {
			t.Fatalf("put %d: %v", i, err)
		}
	}
	extra := []byte("secret-login")
	if _, err := st.put(1000, "sid-a", "c", extra); err == nil {
		t.Fatal("the store grew past its bound")
	}
	if strings.Trim(string(extra), "\x00") != "" {
		t.Fatal("a refused login was not zeroed")
	}
	// A new one from the same account in a session that already has one replaces it and wipes the old one.
	repl := []byte("secret-login")
	if _, err := st.put(1, "sid-a", "c", repl); err != nil {
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
	t.Setenv("TMPDIR", tmp) // winok: TMP and TEMP (what Windows reads) are set on the lines beside it
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
