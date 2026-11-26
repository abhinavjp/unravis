package localci

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestExclusiveTakesTurns(t *testing.T) {
	var inside, worst int32
	var waited int32
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := acquireExclusive(context.Background(), "test-turns", func() { atomic.AddInt32(&waited, 1) })
			if err != nil {
				t.Error(err)
				return
			}
			n := atomic.AddInt32(&inside, 1)
			for {
				w := atomic.LoadInt32(&worst)
				if n <= w || atomic.CompareAndSwapInt32(&worst, w, n) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond)
			atomic.AddInt32(&inside, -1)
			release()
		}()
	}
	wg.Wait()
	if worst != 1 {
		t.Fatalf("%d holders at once, want 1", worst)
	}
	if waited == 0 {
		t.Fatal("nobody waited, so the test did not overlap")
	}
}

func TestExclusiveNamesAreIndependent(t *testing.T) {
	a, err := acquireExclusive(context.Background(), "test-a", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a()
	b, err := acquireExclusive(context.Background(), "test-b", nil)
	if err != nil {
		t.Fatal(err)
	}
	b()
}

func TestExclusiveWaitEndsWithTheRun(t *testing.T) {
	held, err := acquireExclusive(context.Background(), "test-cancel", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer held()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := acquireExclusive(ctx, "test-cancel", nil); err == nil {
		t.Fatal("a cancelled wait must give up")
	}
}
