package localci

import (
	"context"
	"sync"
)

// exclusiveLocks has one one-slot channel per name in local-ci.yml "exclusive". A job instance that names a resource
// holds it for its whole run, so two of them (the legs of a matrix, or jobs of two runs the agent has going) never use
// the same per-PC object at the same time. The holder already owns a run slot, so waiting cannot deadlock the slots.
var exclusiveLocks sync.Map // name -> chan struct{}

// acquireExclusive waits for the resource, calling onWait once when it is busy. release must be called exactly once.
func acquireExclusive(ctx context.Context, name string, onWait func()) (release func(), err error) {
	v, _ := exclusiveLocks.LoadOrStore(name, make(chan struct{}, 1))
	ch := v.(chan struct{})
	select {
	case ch <- struct{}{}:
	default:
		if onWait != nil {
			onWait()
		}
		select {
		case ch <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return func() { <-ch }, nil
}
