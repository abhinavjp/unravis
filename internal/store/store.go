package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"api-audit-explorer/internal/audit"
)

const maxSources = 8

var (
	errInvalidProfile = fmt.Errorf("%w: invalid source profile", audit.ErrInvalidInput)
	errSourceLimit    = fmt.Errorf("%w: maximum source count reached", audit.ErrLimit)
	errDuplicate      = fmt.Errorf("%w: source profile already exists", audit.ErrInvalidInput)
	errSourceNotFound = fmt.Errorf("%w: source not found", audit.ErrNotFound)
	errSourceBusy     = fmt.Errorf("%w: source is busy", audit.ErrBusy)
)

type Store struct {
	mu        sync.RWMutex
	addMu     sync.Mutex
	sources   map[string]*profile
	clients   map[string]*sharedClient
	newClient clientFactory
	global    chan struct{}
	perSource map[string]chan struct{}
	// now is the server clock an incomplete-age range is measured against. It is a
	// field so tests can inject one; nil means the wall clock.
	now func() time.Time
	// allowBroadScans permits census scans against live sources (dashboard only).
	allowBroadScans bool
	// docsExaminedBudget caps docsExamined on a query; 0 means unset.
	docsExaminedBudget int64
	// margins caches calibrated _id margins per source, guarded by marginMu.
	marginMu sync.Mutex
	margins  map[string]audit.IDMargin
}

// clock returns the current time in UTC at millisecond precision, the precision
// MongoDB dates carry, so the literal rendered into a pipeline is exact.
func (s *Store) clock() time.Time {
	now := time.Now
	if s.now != nil {
		now = s.now
	}
	return now().UTC().Truncate(time.Millisecond)
}

// New creates a store with bounded MongoDB connection settings. Options are
// applied in order; with none the store is unchanged.
func New(opts ...Option) *Store {
	s := newStore(defaultClientFactory)
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func newStore(factory clientFactory) *Store {
	return &Store{
		sources:   make(map[string]*profile),
		clients:   make(map[string]*sharedClient),
		newClient: factory,
		global:    make(chan struct{}, 4),
		perSource: make(map[string]chan struct{}),
		margins:   make(map[string]audit.IDMargin),
	}
}

func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func closeClientWithTimeout(client clientHandle) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = client.Close(ctx)
}

// Close releases every unique MongoDB client owned by this store.
func (s *Store) Close(ctx context.Context) error {
	s.addMu.Lock()
	defer s.addMu.Unlock()
	s.mu.Lock()
	clients := make([]clientHandle, 0, len(s.clients))
	for _, shared := range s.clients {
		shared.refs = 0
		shared.removed = true
		if shared.uses == 0 {
			clients = append(clients, shared.client)
		}
	}
	s.sources = make(map[string]*profile)
	s.clients = make(map[string]*sharedClient)
	s.mu.Unlock()

	var first error
	for _, c := range clients {
		if err := c.Close(ctx); err != nil && first == nil {
			first = errors.New("MongoDB connection cleanup failed")
		}
	}
	return first
}
