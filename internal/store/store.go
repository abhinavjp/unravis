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
}

// New creates a store with bounded MongoDB connection settings.
func New() *Store {
	return newStore(defaultClientFactory)
}

func newStore(factory clientFactory) *Store {
	return &Store{
		sources:   make(map[string]*profile),
		clients:   make(map[string]*sharedClient),
		newClient: factory,
		global:    make(chan struct{}, 4),
		perSource: make(map[string]chan struct{}),
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
