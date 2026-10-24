package store

import (
	"context"
	"crypto/tls"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func TestMongoClientOptionsSetsAppNameAndTimeouts(t *testing.T) {
	opts := mongoClientOptions("mongodb://db.example/", nil)
	if opts.AppName == nil || !strings.HasPrefix(*opts.AppName, "Unravis/") {
		t.Fatalf("AppName = %v, want Unravis/ prefix", opts.AppName)
	}
	if opts.ServerSelectionTimeout == nil || *opts.ServerSelectionTimeout != 30*time.Second {
		t.Fatalf("ServerSelectionTimeout = %v, want 30s", opts.ServerSelectionTimeout)
	}
	if opts.ConnectTimeout == nil || *opts.ConnectTimeout != 10*time.Second {
		t.Fatalf("ConnectTimeout = %v, want 10s", opts.ConnectTimeout)
	}
	if opts.MaxPoolSize == nil || *opts.MaxPoolSize != 4 || opts.MaxConnIdleTime == nil || *opts.MaxConnIdleTime != 2*time.Minute {
		t.Fatal("existing pool and idle settings were not kept")
	}
}

func TestMongoClientOptionsOverridesURIAppName(t *testing.T) {
	opts := mongoClientOptions("mongodb://db.example/?appName=other", nil)
	if opts.AppName == nil || !strings.HasPrefix(*opts.AppName, "Unravis/") {
		t.Fatalf("AppName = %v, want the URI appName overridden by Unravis/", opts.AppName)
	}
}

func TestMongoClientOptionsKeepsTLSConfig(t *testing.T) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12}
	opts := mongoClientOptions("mongodb://db.example/", cfg)
	if opts.TLSConfig == nil || opts.TLSConfig.MinVersion != tls.VersionTLS12 {
		t.Fatal("TLS configuration was not applied")
	}
	if opts.TLSConfig == cfg {
		t.Fatal("TLS configuration must be cloned")
	}
}

func TestAppNameIsBounded(t *testing.T) {
	name := appName()
	if !strings.HasPrefix(name, "Unravis/") || len(name) > 128 {
		t.Fatalf("appName() = %q", name)
	}
	if got := truncateAppName("Unravis/" + strings.Repeat("v", 300)); len(got) != 128 {
		t.Fatalf("truncated appName length = %d, want 128", len(got))
	}
}

func TestAggregateCommandHasNoMaxTimeField(t *testing.T) {
	command := aggregateCommand("Audit", mongo.Pipeline{bson.D{{Key: "$match", Value: bson.D{}}}})
	for _, element := range command {
		if strings.EqualFold(element.Key, "maxTimeMS") {
			t.Fatal("aggregate command must not carry maxTimeMS; the driver derives it from the deadline")
		}
	}
}

// blockingCursor models a getMore that waits on the server until its context ends.
type blockingCursor struct {
	closed atomic.Bool
}

func (c *blockingCursor) Next(ctx context.Context) bool { <-ctx.Done(); return false }
func (*blockingCursor) Decode(any) error                { return nil }
func (*blockingCursor) Err() error                      { return nil }
func (c *blockingCursor) Close(context.Context) error   { c.closed.Store(true); return nil }

func TestBudgetCursorStopsAtCommandDeadline(t *testing.T) {
	ctx := withQueryBudget(context.Background(), 50*time.Millisecond)
	cmdCtx, cancel := commandContext(ctx)
	deadline, _ := cmdCtx.Deadline()
	var cancelled atomic.Bool
	inner := &blockingCursor{}
	cursor := &budgetCursor{inner: inner, deadline: deadline, cancel: func() { cancelled.Store(true); cancel() }}
	started := time.Now()
	done := make(chan bool, 1)
	go func() { done <- cursor.Next(context.Background()) }()
	select {
	case got := <-done:
		if got {
			t.Fatal("Next returned true from a blocked cursor")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Next did not stop within 500ms of the command deadline")
	}
	if time.Since(started) > 500*time.Millisecond {
		t.Fatal("Next outlived the budget")
	}
	if err := cursor.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !cancelled.Load() || !inner.closed.Load() {
		t.Fatal("Close must cancel the command context and close the inner cursor")
	}
}

func TestModernAggregateSendsBoundedMaxTimeMSAndAppName(t *testing.T) {
	uri := os.Getenv("AUDIT_EXPLORER_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("set AUDIT_EXPLORER_TEST_MONGO_URI to run against a disposable MongoDB")
	}
	var aggregateMax, getMoreMax atomic.Int64
	var sawGetMore atomic.Bool
	monitor := &event.CommandMonitor{Started: func(_ context.Context, started *event.CommandStartedEvent) {
		value := started.Command.Lookup("maxTimeMS")
		var n int64
		if v, ok := value.AsInt64OK(); ok {
			n = v
		}
		switch started.CommandName {
		case "aggregate":
			aggregateMax.Store(n)
		case "getMore":
			sawGetMore.Store(true)
			getMoreMax.Store(n)
		}
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	handle, err := newModernMongoHandleWithMonitor(ctx, uri, nil, monitor)
	if err != nil {
		t.Fatal("could not initialize test client")
	}
	defer func() { _ = handle.Close(context.Background()) }()
	cursor, err := handle.Aggregate(ctx, "audit_fixture_one", "ApiAuditLog", mongo.Pipeline{
		bson.D{{Key: "$match", Value: bson.D{}}},
		bson.D{{Key: "$limit", Value: int32(250)}},
	})
	if err != nil {
		t.Fatal("aggregate failed")
	}
	for cursor.Next(ctx) {
	}
	_ = cursor.Close(ctx)
	if got := aggregateMax.Load(); got < 1 || got > audit.InteractiveQueryBudget.Milliseconds() {
		t.Fatalf("aggregate maxTimeMS=%d; want 1..%d", got, audit.InteractiveQueryBudget.Milliseconds())
	}
	if sawGetMore.Load() && getMoreMax.Load() != 0 {
		t.Fatalf("getMore carried maxTimeMS=%d; want none", getMoreMax.Load())
	}
}
