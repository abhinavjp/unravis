package casestore

import (
	"context"
	"sort"
	"time"
)

// memPlan is the in-memory saved views and checklist of one case (see PlanStore).
// It is guarded by memData.mu.
type memPlan struct {
	views []SavedView
	items []ChecklistItem
}

var _ PlanStore = (*memCase)(nil)

// planWrite is the shared start of every plan write: handle check, then the
// closed-case and erased-case refusals under the data lock, which it leaves held.
// The caller unlocks.
func (c *memCase) planWrite(ctx context.Context) error {
	if err := c.tagWrite(ctx); err != nil {
		return err
	}
	c.d.mu.Lock()
	if c.d.erased {
		c.d.mu.Unlock()
		return ErrNotFound
	}
	if c.d.info.Closed != nil {
		c.d.mu.Unlock()
		return ErrCaseClosed
	}
	if c.d.plan == nil {
		c.d.plan = &memPlan{}
	}
	return nil
}

func (c *memCase) SavedViews(ctx context.Context) ([]SavedView, error) {
	if err := c.check(ctx); err != nil {
		return nil, err
	}
	c.d.mu.RLock()
	defer c.d.mu.RUnlock()
	out := []SavedView{}
	if c.d.plan != nil {
		for _, v := range c.d.plan.views {
			v.Settings = append([]byte(nil), v.Settings...)
			out = append(out, v)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Saved.After(out[j].Saved) })
	return out, nil
}

func (c *memCase) SaveView(ctx context.Context, v SavedView) (SavedView, error) {
	clean, err := CleanSavedView(v)
	if err != nil {
		return SavedView{}, err
	}
	if clean.ID != "" && !ValidPlanID(clean.ID) {
		return SavedView{}, ErrBadPlan
	}
	if err := c.planWrite(ctx); err != nil {
		return SavedView{}, err
	}
	defer c.d.mu.Unlock()
	p := c.d.plan
	clean.Saved = time.Now().UTC()
	if clean.ID != "" {
		for i := range p.views {
			if p.views[i].ID == clean.ID {
				p.views[i] = clean
				return cloneView(clean), nil
			}
		}
		return SavedView{}, ErrNotFound
	}
	if len(p.views) >= MaxSavedViews {
		return SavedView{}, ErrCapExceeded
	}
	id, err := NewPlanID()
	if err != nil {
		return SavedView{}, err
	}
	clean.ID = id
	p.views = append(p.views, clean)
	return cloneView(clean), nil
}

func cloneView(v SavedView) SavedView {
	v.Settings = append([]byte(nil), v.Settings...)
	return v
}

func (c *memCase) DeleteView(ctx context.Context, id string) error {
	if !ValidPlanID(id) {
		return ErrNotFound
	}
	if err := c.planWrite(ctx); err != nil {
		return err
	}
	defer c.d.mu.Unlock()
	p := c.d.plan
	for i := range p.views {
		if p.views[i].ID == id {
			p.views = append(p.views[:i], p.views[i+1:]...)
			return nil
		}
	}
	return ErrNotFound
}

func (c *memCase) ChecklistItems(ctx context.Context) ([]ChecklistItem, error) {
	if err := c.check(ctx); err != nil {
		return nil, err
	}
	c.d.mu.RLock()
	defer c.d.mu.RUnlock()
	out := []ChecklistItem{}
	if c.d.plan != nil {
		out = append(out, c.d.plan.items...)
	}
	rank := map[string]int{}
	for i, l := range ChecklistLists {
		rank[l] = i
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if rank[a.List] != rank[b.List] {
			return rank[a.List] < rank[b.List]
		}
		if a.Order != b.Order {
			return a.Order < b.Order
		}
		return a.Created.Before(b.Created)
	})
	return out, nil
}

func (c *memCase) PutChecklistItem(ctx context.Context, it ChecklistItem) (ChecklistItem, error) {
	clean, err := CleanChecklistItem(it)
	if err != nil {
		return ChecklistItem{}, err
	}
	if clean.ID != "" && !ValidPlanID(clean.ID) {
		return ChecklistItem{}, ErrBadPlan
	}
	if err := c.planWrite(ctx); err != nil {
		return ChecklistItem{}, err
	}
	defer c.d.mu.Unlock()
	p := c.d.plan
	now := time.Now().UTC()
	if clean.ID != "" {
		for i := range p.items {
			if p.items[i].ID == clean.ID {
				clean.Created = p.items[i].Created
				clean.Updated = now
				p.items[i] = clean
				return clean, nil
			}
		}
		return ChecklistItem{}, ErrNotFound
	}
	if len(p.items) >= MaxChecklistItems {
		return ChecklistItem{}, ErrCapExceeded
	}
	id, err := NewPlanID()
	if err != nil {
		return ChecklistItem{}, err
	}
	clean.ID, clean.Created, clean.Updated = id, now, now
	p.items = append(p.items, clean)
	return clean, nil
}

func (c *memCase) DeleteChecklistItem(ctx context.Context, id string) error {
	if !ValidPlanID(id) {
		return ErrNotFound
	}
	if err := c.planWrite(ctx); err != nil {
		return err
	}
	defer c.d.mu.Unlock()
	p := c.d.plan
	for i := range p.items {
		if p.items[i].ID == id {
			p.items = append(p.items[:i], p.items[i+1:]...)
			return nil
		}
	}
	return ErrNotFound
}
