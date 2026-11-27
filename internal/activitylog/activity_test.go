package activitylog

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMissingLogIsEmpty(t *testing.T) {
	got, skipped, err := (Log{Dir: filepath.Join(t.TempDir(), "none")}).Read(Filter{})
	if err != nil || len(got) != 0 || skipped != 0 {
		t.Fatalf("Read = %v, %d, %v", got, skipped, err)
	}
}

func TestAppendAndFilter(t *testing.T) {
	l := Log{Dir: t.TempDir()}
	t0 := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	for i, a := range []string{ActorCLI, ActorUI, ActorClaude, ActorCLI} {
		if err := l.Append(Entry{Time: t0.Add(time.Duration(i) * time.Minute), Actor: a, Person: "pat", Action: "hold.add", Target: fmt.Sprintf("h-%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	all, _, err := l.Read(Filter{})
	if err != nil || len(all) != 4 || all[0].Target != "h-0" || all[3].Target != "h-3" {
		t.Fatalf("all = %+v, %v", all, err)
	}
	cli, _, _ := l.Read(Filter{Actor: ActorCLI})
	if len(cli) != 2 || cli[0].Target != "h-0" || cli[1].Target != "h-3" {
		t.Fatalf("cli = %+v", cli)
	}
	last, _, _ := l.Read(Filter{Limit: 2})
	if len(last) != 2 || last[0].Target != "h-2" || last[1].Target != "h-3" {
		t.Fatalf("limit 2 = %+v", last)
	}
}

func TestAppendRefusesBadEntries(t *testing.T) {
	l := Log{Dir: t.TempDir()}
	if err := l.Append(Entry{Actor: "admin", Action: "x"}); err == nil {
		t.Error("an unknown actor was accepted")
	}
	if err := l.Append(Entry{Actor: ActorCLI, Action: " "}); err == nil {
		t.Error("an empty action was accepted")
	}
}

func TestAppendCleansText(t *testing.T) {
	l := Log{Dir: t.TempDir()}
	err := l.Append(Entry{Actor: ActorCLI, Person: "pat\r\nfake line", Action: "hold.add",
		Reason: "freeze \x1b[31m password=hunter2 " + strings.Repeat("y", 400)})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(l.Path())
	if n := strings.Count(string(b), "\n"); n != 1 {
		t.Fatalf("one entry made %d lines", n)
	}
	if strings.Contains(string(b), "hunter2") {
		t.Fatalf("a secret reached the log: %s", b)
	}
	got, _, _ := l.Read(Filter{})
	if strings.ContainsAny(got[0].Person+got[0].Reason, "\r\n\x1b") || len([]rune(got[0].Reason)) > maxReason {
		t.Fatalf("entry not cleaned: %+v", got[0])
	}
}

func TestTornLineIsSkipped(t *testing.T) {
	l := Log{Dir: t.TempDir()}
	if err := l.Append(Entry{Actor: ActorCLI, Action: "hold.add"}); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(l.Path(), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(`{"time":"2026-11-27T12:00:00Z","actor":"cli","act`)
	_ = f.Close()
	got, skipped, err := l.Read(Filter{})
	if err != nil || len(got) != 1 || skipped != 1 {
		t.Fatalf("Read = %d entries, %d skipped, %v; want 1, 1, nil", len(got), skipped, err)
	}
}

// Many writers at once, each with its own file handle (as separate processes have): every line stays whole.
func TestConcurrentAppendKeepsLinesWhole(t *testing.T) {
	l := Log{Dir: t.TempDir()}
	const writers, each = 8, 50
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				if err := l.Append(Entry{Actor: ActorCLI, Action: "test.write", Target: fmt.Sprintf("w%d-%d", w, i), Reason: strings.Repeat("r", 250)}); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	got, skipped, err := l.Read(Filter{})
	if err != nil || skipped != 0 || len(got) != writers*each {
		t.Fatalf("Read = %d entries, %d skipped, %v; want %d, 0", len(got), skipped, err, writers*each)
	}
}
