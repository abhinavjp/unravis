package ledger

import (
	"bytes"
	"errors"
	"fmt"
)

// ErrUnreadable is returned (wrapped) by OpenFunc when the stored bytes are not a
// valid ledger.
var ErrUnreadable = errors.New("the stored ledger is not a valid ledger")

// OpenFunc returns a Store whose persistence is supplied by the caller: load
// returns the stored ledger bytes (nil or empty = an empty ledger) and save
// receives the whole ledger as JSON after every accepted change. The JSON format,
// validation, limits, guards and size budget are those of Open; only the place
// the bytes live differs (a case keeps them inside its own encrypted database).
//
// load is called once, here. Bytes that are not a valid ledger make OpenFunc
// return an error, and nothing is written: the stored bytes are never replaced
// by an empty ledger. A change is kept in memory only when save succeeded.
// The returned Store is not safe against a second Store over the same bytes, so
// callers serialise per underlying ledger.
func OpenFunc(load func() ([]byte, error), save func([]byte) error) (Store, error) {
	if load == nil || save == nil {
		return nil, fmt.Errorf("the ledger needs both a load and a save function")
	}
	s := &store{write: save, items: []Hypothesis{}}
	data, err := load()
	if err != nil {
		return nil, fmt.Errorf("the ledger could not be read: %w", err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return s, nil
	}
	items, err := decode(data)
	if err != nil {
		return nil, fmt.Errorf("%w and was left untouched: %v", ErrUnreadable, err)
	}
	s.items = items
	return s, nil
}

// Importer is implemented by the Store that Open, OpenSealed and OpenFunc return.
// It is a separate interface so adding it breaks no other Store implementation.
type Importer interface {
	// Import appends copies of items, all or none: each copy keeps its text, state,
	// evidence references, notes, settling observation, facts, history and times,
	// and gets a new ID. It is refused when the ledger would pass MaxHypotheses or
	// the size budget. It returns the copies as List shows them.
	Import(items []Hypothesis) ([]Hypothesis, error)
}

func (s *store) Import(items []Hypothesis) ([]Hypothesis, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.items)+len(items) > MaxHypotheses {
		return nil, fullErr()
	}
	next := s.snapshot()
	taken := map[string]bool{}
	added := make([]Hypothesis, 0, len(items))
	for _, src := range items {
		h := normalize(clone(src))
		for {
			h.ID = s.newID()
			if !taken[h.ID] {
				break
			}
		}
		taken[h.ID] = true
		next = append(next, h)
		added = append(added, h)
	}
	if len(added) == 0 {
		return []Hypothesis{}, nil
	}
	if err := s.checkBudget(next); err != nil {
		return nil, err
	}
	if err := s.commit(next); err != nil {
		return nil, err
	}
	out := make([]Hypothesis, 0, len(added))
	for _, h := range added {
		out = append(out, present(h))
	}
	return out, nil
}
