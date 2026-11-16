package blackbox

import "testing"

// W10-02: the host recorder must retain the REMAINING share of the owner's single
// maxGB budget after the SQL folder's reserve, so host+SQL admitted bytes satisfy
// one documented ceiling instead of 1.25x it.

func TestHostAllocationIsTheRemainingShare(t *testing.T) {
	if SQLBudgetShare != 4 {
		t.Fatalf("SQLBudgetShare = %d, want 4 (must match cmd/unravis-blackbox sqlBudgetShare)", SQLBudgetShare)
	}
	for _, maxGB := range []float64{1, 2, 4, 8} {
		r := NewRecorder(RecorderOptions{Config: Config{MaxGB: maxGB, KeepDays: 7, MinFreePercent: 10}})
		lim := r.limits()
		ceiling := r.o.Config.MaxBytes()
		sql := ceiling / SQLBudgetShare
		host := lim.MaxBytes
		if host != ceiling-ceiling/SQLBudgetShare {
			t.Errorf("maxGB=%v: host allocation = %d, want the remaining share %d", maxGB, host, ceiling-ceiling/SQLBudgetShare)
		}
		if host+sql > ceiling {
			t.Errorf("maxGB=%v: host %d + sql %d = %d exceeds the combined ceiling %d", maxGB, host, sql, host+sql, ceiling)
		}
		// The host keeps three quarters; SQL one quarter.
		if host != ceiling/SQLBudgetShare*(SQLBudgetShare-1) {
			t.Errorf("maxGB=%v: host %d is not three quarters of %d", maxGB, host, ceiling)
		}
	}
}
