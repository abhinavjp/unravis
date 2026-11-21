package server

import (
	"strings"
	"testing"

	"api-audit-explorer/internal/blackbox"
)

func TestBBUpdateFromStatus(t *testing.T) {
	if bbUpdateFrom(nil) != nil {
		t.Error("nil status gave a value")
	}
	if bbUpdateFrom(&blackbox.UpdateStatus{State: "<script>"}) != nil {
		t.Error("an unknown state passed")
	}
	long := strings.Repeat("é", 1000)
	u := bbUpdateFrom(&blackbox.UpdateStatus{Mode: "auto", State: "held", Latest: long, Result: long})
	if u == nil || u.State != "held" || len([]rune(u.Result)) != 300 || len([]rune(u.Latest)) != 40 {
		t.Errorf("got %+v", u)
	}
}

func TestBBUpdateEveryRecorderState(t *testing.T) {
	for _, st := range []string{"idle", "managed", "available", "held", "skipped", "downloading", "installing", "failed", "off"} {
		u := bbUpdateFrom(&blackbox.UpdateStatus{State: st, Result: "Could not write C:\\x: access denied"})
		if u == nil || u.State != st {
			t.Errorf("state %q was dropped", st)
		}
		if st == "failed" && u != nil && u.Result != "" {
			t.Error("failed state kept the recorder's error text")
		}
	}
}
