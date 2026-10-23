package store

import (
	"context"
	"errors"

	"api-audit-explorer/internal/audit"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func sourceQueryError(ctx context.Context, err error) (string, string) {
	code := "query_failed"
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) || mongo.IsTimeout(err) {
		code = "query_timeout"
	} else if errors.Is(err, context.Canceled) {
		code = "query_cancelled"
	} else {
		for _, cause := range connectionErrorCauses(err) {
			if number, ok := mongoCommandCode(cause); ok {
				switch number {
				case 13:
					code = "permission_denied"
				case 18:
					code = "authentication_failed"
				case 50, 262:
					code = "query_timeout"
				case 146, 292:
					code = "query_memory_limit"
				}
				if code != "query_failed" {
					break
				}
			}
		}
		if code == "query_failed" && mongo.IsNetworkError(err) {
			code = "connection_failed"
		}
	}
	return code, audit.SourceQueryErrorMessage(code)
}

func sourceAdmissionError(err error) (string, string) {
	code := "capacity_timeout"
	if errors.Is(err, context.Canceled) {
		code = "query_cancelled"
	}
	return code, audit.SourceQueryErrorMessage(code)
}
