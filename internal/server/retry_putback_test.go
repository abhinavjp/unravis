package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"

	"api-audit-explorer/internal/casestore"
)

// flakyRetryStore opens cases whose retry list can be told to fail, to show what a retry does when the store goes bad half way (RV-SRV-9).
type flakyRetryStore struct {
	casestore.Store
	failNote   atomic.Bool
	failRemove atomic.Bool
}

func (s *flakyRetryStore) Open(ctx context.Context, id casestore.CaseID) (casestore.Case, error) {
	c, err := s.Store.Open(ctx, id)
	if err != nil {
		return nil, err
	}
	return &flakyRetryCase{Case: c, s: s}, nil
}

type flakyRetryCase struct {
	casestore.Case
	s *flakyRetryStore
}

var errFlaky = errors.New("flaky store")

func (c *flakyRetryCase) tagger() casestore.Tagger      { return c.Case.(casestore.Tagger) }
func (c *flakyRetryCase) retries() casestore.RetryStore { return c.Case.(casestore.RetryStore) }

func (c *flakyRetryCase) SetEnvs(ctx context.Context, n []string) error {
	return c.tagger().SetEnvs(ctx, n)
}
func (c *flakyRetryCase) Envs(ctx context.Context) ([]string, error) { return c.tagger().Envs(ctx) }
func (c *flakyRetryCase) AddSource(ctx context.Context, s casestore.SourceTag) (casestore.SourceTag, error) {
	return c.tagger().AddSource(ctx, s)
}
func (c *flakyRetryCase) Sources(ctx context.Context) ([]casestore.SourceTag, error) {
	return c.tagger().Sources(ctx)
}
func (c *flakyRetryCase) RemoveSource(ctx context.Context, id int64) error {
	return c.tagger().RemoveSource(ctx, id)
}
func (c *flakyRetryCase) UpdateSourceTags(ctx context.Context, id int64, env, portal, api string) (casestore.SourceTag, error) {
	return c.tagger().UpdateSourceTags(ctx, id, env, portal, api)
}

func (c *flakyRetryCase) Retries(ctx context.Context) ([]casestore.RetryItem, error) {
	return c.retries().Retries(ctx)
}
func (c *flakyRetryCase) ReplaceRetries(ctx context.Context, items []casestore.RetryItem) ([]casestore.RetryItem, error) {
	return c.retries().ReplaceRetries(ctx, items)
}
func (c *flakyRetryCase) RemoveRetry(ctx context.Context, id int64) error {
	if c.s.failRemove.Load() {
		return errFlaky
	}
	return c.retries().RemoveRetry(ctx, id)
}
func (c *flakyRetryCase) NoteRetry(ctx context.Context, item casestore.RetryItem) (casestore.RetryItem, error) {
	if c.s.failNote.Load() {
		return casestore.RetryItem{}, errFlaky
	}
	return c.retries().NoteRetry(ctx, item)
}

func forEachFlakyRetryStore(t *testing.T, fn func(t *testing.T, e *caseEnv, id string, s *flakyRetryStore)) {
	for name, mk := range tagStores(t) {
		t.Run(name, func(t *testing.T) {
			inner, _ := mk()
			s := &flakyRetryStore{Store: inner}
			e := tagEnv(t, s, &fakeRepository{})
			fn(t, e, e.newCase("flaky"), s)
		})
	}
}

// A retry that fails while the store cannot put the item back must not lose the item: it was never taken off the list.
func TestRetryThatFailsWhileThePutBackWouldFailKeepsTheItem(t *testing.T) {
	forEachFlakyRetryStore(t, func(t *testing.T, e *caseEnv, id string, s *flakyRetryStore) {
		names := make([]string, casestore.MaxEnvs+1)
		for i := range names {
			names[i] = "e" + strconv.Itoa(i)
		}
		body := `{"items":[{"kind":"environments","key":"environments","label":"Environments","reason":"failed","payload":{"environments":["` + joinQuoted(names) + `"]}}]}`
		if res := putRetries(e, id, body); res.Code != 200 {
			t.Fatalf("put = %d %s", res.Code, res.Body)
		}
		it := e.view(t, id).Retries[0]
		s.failNote.Store(true)
		if res := e.believing(http.MethodPost, retryPath(id, it), "", "open"); res.Code != http.StatusUnprocessableEntity {
			t.Fatalf("over-cap retry = %d %s", res.Code, res.Body)
		}
		s.failNote.Store(false)
		if v := e.view(t, id); len(v.Retries) != 1 || v.Retries[0].Kind != "environments" {
			t.Fatalf("a failed retry lost the item: %+v", v.Retries)
		}
	})
}

// A run that worked and a list update that then fails leaves the item listed; pressing it again takes it off and the tag is in once.
func TestRetryWhoseRemovalFailsDoesNotAddTheTagTwice(t *testing.T) {
	forEachFlakyRetryStore(t, func(t *testing.T, e *caseEnv, id string, s *flakyRetryStore) {
		putRetries(e, id, `{"items":[{"kind":"tag","key":"iis:web","label":"web","reason":"failed","payload":{"kind":"iis-logs","label":"web","env":"prod"}}]}`)
		it := e.view(t, id).Retries[0]
		s.failRemove.Store(true)
		if res := e.believing(http.MethodPost, retryPath(id, it), "", "open"); res.Code == http.StatusNoContent {
			t.Fatalf("a retry whose list update failed answered done: %s", res.Body)
		}
		if v := e.view(t, id); len(v.Retries) != 1 {
			t.Fatalf("the item was not kept: %+v", v.Retries)
		}
		s.failRemove.Store(false)
		if res := e.believing(http.MethodPost, retryPath(id, it), "", "open"); res.Code != http.StatusNoContent {
			t.Fatalf("second press = %d %s", res.Code, res.Body)
		}
		if got := sourcesOf(t, e, "/api/cases/"+id+"/sources"); len(got) != 1 {
			t.Fatalf("the tag went in %d times", len(got))
		}
		if v := e.view(t, id); len(v.Retries) != 0 {
			t.Fatalf("the item is still listed: %+v", v.Retries)
		}
	})
}

func joinQuoted(names []string) string {
	out := ""
	for i, n := range names {
		if i > 0 {
			out += `","`
		}
		out += n
	}
	return out
}
