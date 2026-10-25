package store

import (
	"context"
	"fmt"

	"api-audit-explorer/internal/audit"
)

// admitStage says which step of admission refused a source.
type admitStage int

const (
	// admitGlobal: the global query slot could not be taken.
	admitGlobal admitStage = iota + 1
	// admitRemoved: the source was removed before its slot was looked up.
	admitRemoved
	// admitSource: the per-source query slot could not be taken.
	admitSource
)

// admitFailure describes a refused admission. Code and Message are the curated
// per-source error; Err is the raw acquire error (nil for admitRemoved).
type admitFailure struct {
	Stage   admitStage
	Code    string
	Message string
	Err     error
}

// lookupError maps a refusal to the error the single-record lookups return.
func (f *admitFailure) lookupError() error {
	switch f.Stage {
	case admitRemoved:
		return fmt.Errorf("%w: source was removed before the query started", audit.ErrNotFound)
	case admitSource:
		return fmt.Errorf("%w: source query capacity is busy", audit.ErrBusy)
	default:
		return fmt.Errorf("%w: query capacity is busy", audit.ErrBusy)
	}
}

// admit takes the per-source and then the global query slot. The source slot comes
// first so a read that is queued behind another read of the same source holds no
// global slot while it waits: otherwise two reads of one source could park two of
// the four global slots and leave only two operations running. On failure the
// release function is nil and the failure says why the source was skipped; nothing
// is held. This is the one place the two-slot protocol lives.
func (s *Store) admit(ctx context.Context, p *profile) (func(), *admitFailure) {
	s.mu.RLock()
	per := s.perSource[p.source.ID]
	s.mu.RUnlock()
	if per == nil {
		return nil, &admitFailure{Stage: admitRemoved, Code: "source_unavailable", Message: audit.SourceQueryErrorMessage("source_unavailable")}
	}
	if err := acquire(ctx, per); err != nil {
		code, message := sourceAdmissionError(err)
		return nil, &admitFailure{Stage: admitSource, Code: code, Message: message, Err: err}
	}
	if err := acquire(ctx, s.global); err != nil {
		<-per
		code, message := sourceAdmissionError(err)
		return nil, &admitFailure{Stage: admitGlobal, Code: code, Message: message, Err: err}
	}
	return func() {
		<-s.global
		<-per
	}, nil
}
