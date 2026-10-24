package audit

import (
	"context"
	"errors"
)

// ValidatePattern checks a PatternInput. STUB: Task A replaces this file.
func ValidatePattern(in PatternInput) error {
	return errors.New("patterns: not implemented")
}

// RunPatterns runs rules B1, B2, B6 and B12 over the window. STUB: Task A replaces this file.
func RunPatterns(ctx context.Context, repo Repository, in PatternInput) (PatternReport, error) {
	return PatternReport{}, errors.New("patterns: not implemented")
}
