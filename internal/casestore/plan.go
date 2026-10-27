package casestore

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

// What a case keeps of its guided analysis, apart from the evidence: the views a
// person saved in the Manual tools, and the four-list checklist of what to look
// at next. Both are small, typed by the person, and travel with the case file.
// Neither holds a connection, a password or any audit payload: the settings of a
// saved view are filters (a window, a route, a threshold), and the API layer
// refuses a setting that names a credential or an address.

const (
	// MaxSavedViews and MaxChecklistItems bound what one case keeps. A write past
	// the bound is refused with ErrCapExceeded; nothing already saved is dropped.
	MaxSavedViews     = 200
	MaxChecklistItems = 500
	// MaxViewName and MaxChecklistText are in characters.
	MaxViewName      = 60
	MaxChecklistText = 300
	// MaxViewSettings is the size of one saved view's settings object, in bytes.
	MaxViewSettings = 8 << 10
	maxToolName     = 40
	maxSuspectID    = 16
)

// ErrBadPlan is returned for a saved view or a checklist item that breaks a rule
// above or has a list, tool or id of the wrong shape.
var ErrBadPlan = errors.New("casestore: invalid saved view or checklist item")

// The four lists of the checklist. An item is on exactly one.
const (
	ListToDo     = "todo"
	ListDoing    = "doing"
	ListLearned  = "learned"
	ListRuledOut = "ruled_out"
)

// ChecklistLists is the fixed order of the four lists.
var ChecklistLists = []string{ListToDo, ListDoing, ListLearned, ListRuledOut}

// ValidChecklistList reports whether s names one of the four lists.
func ValidChecklistList(s string) bool {
	for _, l := range ChecklistLists {
		if s == l {
			return true
		}
	}
	return false
}

// SavedView is one remembered setting of a Manual tool.
type SavedView struct {
	ID    string    `json:"id"`
	Name  string    `json:"name"`
	Tool  string    `json:"tool"`
	Saved time.Time `json:"saved"`
	// Settings is one JSON object: what the tool needs to show the same picture
	// again. The store keeps it as sent.
	Settings json.RawMessage `json:"settings"`
}

// OrderAuto is the Order that lets the store pick the place (see ChecklistItem).
const OrderAuto = -1

// maxChecklistOrder is the highest Order a store keeps.
const maxChecklistOrder = 1_000_000

// ChecklistItem is one line on one of the four lists.
type ChecklistItem struct {
	ID   string `json:"id"`
	List string `json:"list"`
	Text string `json:"text"`
	// Suspect is the catalogue id (N1, H2, ...) the line is about, or empty.
	Suspect string `json:"suspect,omitempty"`
	// Order sorts items inside a list; lower comes first. OrderAuto asks the
	// store to choose: a new item, or one moved to another list, goes last in its
	// list; an item that stays in its list keeps its place. The store chooses
	// inside its own write, so two adds at once never share a place.
	Order   int       `json:"order"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
}

// PlanStore is implemented by a Case that keeps saved views and a checklist. It
// is a separate interface, like WorkspaceBlobStore, so it breaks no Case. Reads
// work on a closed case; writes are refused there with ErrCaseClosed.
type PlanStore interface {
	// SavedViews lists the views, newest first. A case without any returns none.
	SavedViews(ctx context.Context) ([]SavedView, error)
	// SaveView stores one view. An empty ID is a new view (the store makes the id
	// and the time); an ID that exists replaces it; an ID that does not is
	// ErrNotFound. ErrBadPlan for a view that breaks a rule; ErrCapExceeded when a
	// new view would pass MaxSavedViews.
	SaveView(ctx context.Context, v SavedView) (SavedView, error)
	// DeleteView removes one view; ErrNotFound when there is none with that id.
	DeleteView(ctx context.Context, id string) error

	// ChecklistItems lists the items by list, then Order, then creation.
	ChecklistItems(ctx context.Context) ([]ChecklistItem, error)
	// PutChecklistItem stores one item, with the same ID rules as SaveView.
	PutChecklistItem(ctx context.Context, it ChecklistItem) (ChecklistItem, error)
	// DeleteChecklistItem removes one item; ErrNotFound when there is none.
	DeleteChecklistItem(ctx context.Context, id string) error
}

// CleanSavedView checks a view and returns it with its text trimmed and its
// settings compacted. The ID and the time are left to the store.
func CleanSavedView(v SavedView) (SavedView, error) {
	v.Name = strings.TrimSpace(v.Name)
	v.Tool = strings.TrimSpace(v.Tool)
	n := utf8.RuneCountInString(v.Name)
	if n < 1 || n > MaxViewName || !printable(v.Name) {
		return v, ErrBadPlan
	}
	if t := utf8.RuneCountInString(v.Tool); t < 1 || t > maxToolName || !slug(v.Tool) {
		return v, ErrBadPlan
	}
	if len(v.Settings) == 0 {
		v.Settings = json.RawMessage("{}")
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(v.Settings, &obj); err != nil || obj == nil {
		return v, ErrBadPlan
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, v.Settings); err != nil {
		return v, ErrBadPlan
	}
	if compact.Len() > MaxViewSettings {
		return v, ErrCapExceeded
	}
	v.Settings = json.RawMessage(compact.Bytes())
	return v, nil
}

// CleanChecklistItem checks an item and returns it with its text trimmed.
func CleanChecklistItem(it ChecklistItem) (ChecklistItem, error) {
	it.Text = strings.TrimSpace(it.Text)
	it.Suspect = strings.TrimSpace(it.Suspect)
	n := utf8.RuneCountInString(it.Text)
	if n < 1 || n > MaxChecklistText || !printable(it.Text) {
		return it, ErrBadPlan
	}
	if !ValidChecklistList(it.List) {
		return it, ErrBadPlan
	}
	if it.Suspect != "" && (utf8.RuneCountInString(it.Suspect) > maxSuspectID || !slug(it.Suspect)) {
		return it, ErrBadPlan
	}
	if it.Order < OrderAuto || it.Order > maxChecklistOrder {
		return it, ErrBadPlan
	}
	return it, nil
}

// ValidPlanID reports whether s has the shape NewPlanID makes (32 lower-case hex
// characters), so an id in a path or a body is checked before the store sees it.
func ValidPlanID(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func printable(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func slug(s string) bool {
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_'
		if !ok {
			return false
		}
	}
	return s != ""
}

// NewPlanID makes the id of a saved view or checklist item: 128 random bits as
// 32 lower-case hex characters.
func NewPlanID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
