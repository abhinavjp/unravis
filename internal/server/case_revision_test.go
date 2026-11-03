package server

import (
	"context"
	"net/http"
	"strconv"
	"testing"

	"api-audit-explorer/internal/casestore"
)

// revisionOf reads the case revision through the store, on a handle of its own.
func revisionOf(t *testing.T, e *caseEnv, id string) int64 {
	t.Helper()
	ctx := context.Background()
	c, err := e.store.Open(ctx, casestore.CaseID(id))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	r, ok := c.(interface {
		Revision(context.Context) (int64, error)
	})
	if !ok {
		t.Fatalf("case %T has no Revision", c)
	}
	rev, err := r.Revision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return rev
}

func wantRevisionDelta(t *testing.T, e *caseEnv, id string, before int64, delta int64, when string) int64 {
	t.Helper()
	after := revisionOf(t, e, id)
	if after != before+delta {
		t.Fatalf("revision %s = %d, want %d (before %d, delta %d)", when, after, before+delta, before, delta)
	}
	return after
}

func TestRevisionHandlerImportCommit(t *testing.T) {
	ie := newIntEnv(t, intRepo())
	e := ie.caseEnv
	id := e.newCase("Revision import")
	rev := revisionOf(t, e, id)

	ie.commit(id, "iis-httperr", "web-a", intHTTPERR)
	rev = wantRevisionDelta(t, e, id, rev, 1, "after a committed import")

	// A preview stores nothing.
	if res := e.upload("/api/cases/"+id+"/imports?preview=1&host=mongo-a", nil, &upload{intFileName, intMongod}); res.Code != http.StatusOK {
		t.Fatalf("preview = %d %s", res.Code, res.Body)
	}
	rev = wantRevisionDelta(t, e, id, rev, 0, "after a preview")

	// A refused file (a binary export) stores nothing.
	bad := binaryUploads()[0]
	res := e.upload("/api/cases/"+id+"/imports?importer=iis-httperr&host=web-a", ackFields, &upload{bad.name, bad.content})
	if res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("refused upload = %d %s", res.Code, res.Body)
	}
	wantRevisionDelta(t, e, id, rev, 0, "after a refused upload")
}

func TestRevisionHandlerEnvironments(t *testing.T) {
	e := newIntEnv(t, intRepo()).caseEnv
	id := e.newCase("Revision envs")
	rev := revisionOf(t, e, id)
	res := e.json(http.MethodPut, "/api/cases/"+id+"/environments", `{"environments":["Production","staging"]}`)
	if res.Code != http.StatusOK {
		t.Fatalf("put environments = %d %s", res.Code, res.Body)
	}
	wantRevisionDelta(t, e, id, rev, 1, "after the environment list")
}

func TestRevisionHandlerSourceAddAndDelete(t *testing.T) {
	e := newIntEnv(t, intRepo()).caseEnv
	id := e.newCase("Revision sources")
	rev := revisionOf(t, e, id)
	res := e.json(http.MethodPost, "/api/cases/"+id+"/sources", `{"kind":"iis","label":"web-a logs","env":"Production"}`)
	if res.Code != http.StatusCreated {
		t.Fatalf("post source = %d %s", res.Code, res.Body)
	}
	var added sourceTagDTO
	decodeBody(t, res, &added)
	rev = wantRevisionDelta(t, e, id, rev, 1, "after adding a source")

	path := "/api/cases/" + id + "/sources/" + strconv.FormatInt(added.ID, 10)
	if del := e.json(http.MethodDelete, path, ""); del.Code != http.StatusOK {
		t.Fatalf("delete source = %d %s", del.Code, del.Body)
	}
	rev = wantRevisionDelta(t, e, id, rev, 1, "after deleting the source")

	if del := e.json(http.MethodDelete, path, ""); del.Code != http.StatusOK {
		t.Fatalf("second delete = %d %s", del.Code, del.Body)
	}
	wantRevisionDelta(t, e, id, rev, 0, "after deleting it again")
}

func TestRevisionHandlerMongoClassification(t *testing.T) {
	store, _ := tagStores(t)["sqlite"]()
	e, _, _ := mongoEnv(t, store, "auditdb")
	id := e.newCase("Revision mongo")
	rev := revisionOf(t, e, id)

	// An untested database is refused with a 422 and changes nothing.
	if res := classify(e, id, "auditdb", "audit", ""); res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("before any test = %d %s", res.Code, res.Body)
	}
	rev = wantRevisionDelta(t, e, id, rev, 0, "after a refused classification")

	testMongo(t, e)
	if res := classify(e, id, "auditdb", "audit", ""); res.Code != http.StatusCreated {
		t.Fatalf("classify = %d %s", res.Code, res.Body)
	}
	if after := revisionOf(t, e, id); after <= rev {
		t.Fatalf("revision after classifying = %d, want more than %d", after, rev)
	}
}

func TestRevisionHandlerRetryRun(t *testing.T) {
	store, _ := tagStores(t)["sqlite"]()
	e := tagEnv(t, store, &fakeRepository{})
	id := e.newCase("Revision retry")
	putRetries(e, id, `{"items":[`+
		`{"kind":"environments","key":"environments","label":"Environments","reason":"failed","payload":{"environments":["prod","staging"]}},`+
		`{"kind":"tag","key":"iis:web","label":"web","reason":"failed","payload":{"kind":"iis-logs","label":"web","env":"prod"}}]}`)
	items := e.view(t, id).Retries
	if len(items) != 2 {
		t.Fatalf("retries = %+v", items)
	}
	rev := revisionOf(t, e, id)
	for i, it := range items {
		res := e.json(http.MethodPost, "/api/cases/"+id+"/retries/"+strconv.FormatInt(it.ID, 10)+"/retry", "")
		if res.Code != http.StatusNoContent {
			t.Fatalf("retry %d = %d %s", i, res.Code, res.Body)
		}
		after := revisionOf(t, e, id)
		if after <= rev {
			t.Fatalf("revision after retry %d = %d, want more than %d", i, after, rev)
		}
		rev = after
	}
}
