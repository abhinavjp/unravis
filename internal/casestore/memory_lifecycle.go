package casestore

import (
	"context"
	"fmt"
	"time"
)

var _ Lifecycle = (*memStore)(nil)

func appendHistory(h []HistoryEntry, at time.Time, kind string) []HistoryEntry {
	h = append(h, HistoryEntry{At: at.UTC().Truncate(time.Millisecond), Kind: kind})
	if len(h) > MaxHistory {
		h = append([]HistoryEntry(nil), h[len(h)-MaxHistory:]...)
	}
	return h
}

// ReopenCase clears the Closed stamp (see Lifecycle).
func (s *memStore) ReopenCase(ctx context.Context, id CaseID) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d, err := s.get(id)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := CheckMark(d.info.Mark); err != nil {
		return err
	}
	if err := CheckBelief(ActionReopen, "", StateOf(d.info)); err != nil {
		return err
	}
	d.info.Closed = nil
	d.info.History = appendHistory(d.info.History, s.now(), HistoryReopened)
	return nil
}

// SetMark stamps the case; "" clears the stamp.
func (s *memStore) SetMark(ctx context.Context, id CaseID, mark string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	switch mark {
	case "", MarkArchiving, MarkFreeing, MarkErasing:
	default:
		return fmt.Errorf("casestore: unknown mark")
	}
	d, err := s.get(id)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.info.Mark = mark
	return nil
}

// Mark reads the stamp.
func (s *memStore) Mark(ctx context.Context, id CaseID) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	d, err := s.get(id)
	if err != nil {
		return "", err
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.info.Mark, nil
}

// CreateFor makes a case once per request id (see Lifecycle).
func (s *memStore) CreateFor(ctx context.Context, title, requestID string) (CaseInfo, bool, error) {
	if requestID == "" {
		info, err := s.Create(ctx, title)
		return info, false, err
	}
	if !ValidRequestID(requestID) {
		return CaseInfo{}, false, fmt.Errorf("casestore: bad request id")
	}
	s.createMu.Lock()
	defer s.createMu.Unlock()
	all, err := s.List(ctx)
	if err != nil {
		return CaseInfo{}, false, err
	}
	for _, info := range all {
		if info.RequestID != requestID {
			continue
		}
		if StateOf(info) == StateArchiving || info.Mark == MarkFreeing || info.Mark == MarkErasing {
			return CaseInfo{}, false, ErrAlreadyMade
		}
		return info, true, nil
	}
	if err := ctx.Err(); err != nil {
		return CaseInfo{}, false, err
	}
	info, err := s.Create(ctx, title)
	if err != nil {
		return CaseInfo{}, false, err
	}
	d, err := s.get(info.ID)
	if err != nil {
		return CaseInfo{}, false, err
	}
	d.mu.Lock()
	d.info.RequestID = requestID
	info = copyInfo(d.info)
	d.mu.Unlock()
	return info, false, nil
}
