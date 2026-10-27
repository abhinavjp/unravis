package server

import (
	"context"
	"errors"
	"strings"

	"api-audit-explorer/internal/casestore"
)

// The server keeps the "needs another try" line for an import (C17, A14).
// The wizard writes the list when it finishes, but an import that fails in a
// later session, or is killed in the middle, never reaches the wizard. So the
// line is written before the import starts and removed when the file is in:
// a failure, a crash or a kill leaves it behind, and the case lists the file as
// "Import failed" on the next open. The line holds the file's name only.

const importRetryReason = "import-failed"

func importRetryKey(fileName string) string { return "file:" + fileName }

// noteImportAttempt writes the line for fileName. It is best effort: a case
// store without a retry list, or a name the list refuses, only means no line.
func noteImportAttempt(ctx context.Context, c casestore.Case, fileName string) {
	rs, ok := innerCase(c).(casestore.RetryStore)
	if !ok || strings.TrimSpace(fileName) == "" {
		return
	}
	_, _ = rs.NoteRetry(context.WithoutCancel(ctx), casestore.RetryItem{
		Kind: casestore.RetryImport, Key: importRetryKey(fileName), Label: fileName, Reason: importRetryReason,
	})
}

// clearImportAttempt removes the lines for fileName once the file is in the
// case: the one the server wrote, and the one the wizard wrote for the same file
// (its key is the source key, its label the file's name).
func clearImportAttempt(ctx context.Context, c casestore.Case, fileName string) {
	rs, ok := innerCase(c).(casestore.RetryStore)
	if !ok || strings.TrimSpace(fileName) == "" {
		return
	}
	ctx = context.WithoutCancel(ctx)
	items, err := rs.Retries(ctx)
	if err != nil {
		return
	}
	for _, it := range items {
		if it.Kind != casestore.RetryImport {
			continue
		}
		if strings.EqualFold(it.Key, importRetryKey(fileName)) || it.Label == fileName {
			_ = rs.RemoveRetry(ctx, it.ID)
		}
	}
}

// importLanded: the file is in the case, or the same bytes already were.
func importLanded(err error) bool {
	if err == nil {
		return true
	}
	var duplicate *casestore.DuplicateError
	return errors.As(err, &duplicate)
}
