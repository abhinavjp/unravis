package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"api-audit-explorer/internal/update"
)

// stalledBody holds back its bytes until released, and says when the handler first asked for them.
type stalledBody struct {
	asked   chan struct{}
	release chan struct{}
	once    sync.Once
	data    *strings.Reader
}

func (b *stalledBody) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.asked) })
	<-b.release
	return b.data.Read(p)
}

// Two settings saves that change different fields must both stick. The first request is slow to send its body; the second one
// finishes in the meantime. Reading the current settings before the slow body arrived made the first one write the old value back.
func TestSettingsPutsChangingDifferentFieldsBothStick(t *testing.T) {
	setVersion(t, "0.1.0-rc.47")
	k, _ := rcKey(t)
	u := NewUpdater(t.TempDir(), []update.Key{k})
	h := updHandler(u)

	slow := &stalledBody{asked: make(chan struct{}), release: make(chan struct{}), data: strings.NewReader(`{"auto":false}`)}
	req := httptest.NewRequest(http.MethodPut, "/api/update/settings", slow)
	req.Host = "127.0.0.1:8765"
	req.Header.Set("X-Audit-Token", testToken)
	req.Header.Set("Content-Type", "application/json")
	first := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(first, req)
	}()
	<-slow.asked

	if rec := request(h, http.MethodPut, "/api/update/settings", `{"intervalMinutes":60}`, true); rec.Code != http.StatusOK {
		t.Fatalf("second save: %d %s", rec.Code, rec.Body.String())
	}
	close(slow.release)
	<-done
	if first.Code != http.StatusOK {
		t.Fatalf("first save: %d %s", first.Code, first.Body.String())
	}
	if s := u.Settings(); s.Auto || s.IntervalMinutes != 60 {
		t.Fatalf("one save undid the other: %+v (want auto off and 60 minutes)", s)
	}
	// and what is on disk is the same, so a restart keeps both
	if s := NewUpdater(u.dir, []update.Key{k}).Settings(); s.Auto || s.IntervalMinutes != 60 {
		t.Fatalf("on disk: %+v", s)
	}
}
