package server

import (
	"errors"
	"reflect"
	"strconv"
	"testing"

	"api-audit-explorer/internal/calltrace"
	"api-audit-explorer/internal/casestore"
)

// TestTracingWarningsKeepEveryGroup provokes a selection warning (more than 50
// sources), a lane warning (a source with no file) and the audit listing
// warning in one answer, and checks that all three arrive, in that order, and
// that the case lanes carry the same asOf as the answer.
func TestTracingWarningsKeepEveryGroup(t *testing.T) {
	for name, mk := range tagStores(t) {
		t.Run(name, func(t *testing.T) {
			store, _ := mk()
			e := tagEnv(t, store, &auditFake{listErr: errors.New("listing down")})
			id := e.newCase("warnings")
			for i := 0; i < tracingMaxSources+1; i++ {
				seedTracingTag(t, store, id, casestore.SourceTag{Kind: "iis", Label: "tag " + strconv.Itoa(i)})
			}
			out, _ := tracingPost(t, e, id, tracingBody(""))
			want := []string{tracingCappedWarn, tracingNoFileWarn, tracingAuditListWarn}
			if !reflect.DeepEqual(out.Warnings, want) {
				t.Errorf("warnings %q, want %q", out.Warnings, want)
			}
			if len(out.Sources) != tracingMaxSources {
				t.Errorf("sources %d, want %d", len(out.Sources), tracingMaxSources)
			}
			for _, key := range []string{"sql", "iis"} {
				if l := tracingLaneOf(t, out, key); !l.AsOf.Equal(out.AsOf) {
					t.Errorf("%s asOf %v differs from the answer asOf %v", key, l.AsOf, out.AsOf)
				}
			}
			// The listing failure keeps an incomplete audit lane in the answer.
			if l := tracingLaneOf(t, out, "audit"); l.State != calltrace.StateIncomplete || l.Complete || l.ReasonCode != "error" {
				t.Errorf("audit lane %+v, want incomplete with reason code error", l)
			}
		})
	}
}
