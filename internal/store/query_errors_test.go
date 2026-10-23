package store

import (
	"api-audit-explorer/internal/audit"
	"context"
	"errors"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"strings"
	"testing"
)

func TestSourceQueryErrorKeepsTimeoutAndPermissionsDistinct(t *testing.T) {
	for _, item := range []struct {
		err  error
		code string
	}{{context.Canceled, "query_cancelled"}, {context.DeadlineExceeded, "query_timeout"}, {mongo.CommandError{Code: 50, Message: "secret"}, "query_timeout"}, {mongo.CommandError{Code: 13, Message: "secret"}, "permission_denied"}, {mongo.CommandError{Code: 18, Message: "secret"}, "authentication_failed"}, {mongo.CommandError{Code: 292, Message: "secret"}, "query_memory_limit"}, {errors.New("mongodb://user:secret@host"), "query_failed"}} {
		code, message := sourceQueryError(context.Background(), item.err)
		if code != item.code || message != audit.SourceQueryErrorMessage(item.code) || strings.Contains(message, "secret") {
			t.Fatalf("classification = %q/%q", code, message)
		}
	}
}
