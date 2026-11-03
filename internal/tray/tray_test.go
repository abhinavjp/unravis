package tray

import (
	"strings"
	"testing"
)

func goodMenu() []Item {
	return []Item{
		{ID: 1, Text: "Open Unravis"},
		{Separator: true},
		{ID: 2, Text: "Start with Windows", Checked: true},
		{ID: 3, Text: "Quit", Disabled: true},
	}
}

func goodOptions() Options {
	return Options{
		Tooltip:  "Unravis",
		Menu:     goodMenu,
		OnSelect: func(uint32) {},
		OnOpen:   func() {},
	}
}

func TestValidateItemsAcceptsAGoodMenu(t *testing.T) {
	if err := ValidateItems(goodMenu()); err != nil {
		t.Fatalf("good menu rejected: %v", err)
	}
	if err := ValidateItems(nil); err != nil {
		t.Fatalf("empty menu rejected: %v", err)
	}
	full := make([]Item, MaxItems)
	for i := range full {
		full[i] = Item{ID: uint32(i + 1), Text: "Item"}
	}
	if err := ValidateItems(full); err != nil {
		t.Fatalf("a full menu was rejected: %v", err)
	}
}

func TestValidateItemsRejectsDuplicateAndZeroIDs(t *testing.T) {
	if err := ValidateItems([]Item{{ID: 1, Text: "A"}, {ID: 1, Text: "B"}}); err == nil {
		t.Error("duplicate IDs accepted")
	}
	if err := ValidateItems([]Item{{ID: 0, Text: "A"}}); err == nil {
		t.Error("zero ID accepted")
	}
}

func TestValidateItemsRejectsTooManyItems(t *testing.T) {
	items := make([]Item, MaxItems+1)
	for i := range items {
		items[i] = Item{ID: uint32(i + 1), Text: "Item"}
	}
	if err := ValidateItems(items); err == nil {
		t.Errorf("%d items accepted", len(items))
	}
	seps := make([]Item, MaxItems+1)
	for i := range seps {
		seps[i] = Item{Separator: true}
	}
	if err := ValidateItems(seps); err == nil {
		t.Error("too many separators accepted")
	}
}

func TestValidateItemsRejectsLongOrEmptyText(t *testing.T) {
	if err := ValidateItems([]Item{{ID: 1, Text: ""}}); err == nil {
		t.Error("empty text accepted")
	}
	if err := ValidateItems([]Item{{ID: 1, Text: strings.Repeat("a", MaxTextRunes)}}); err != nil {
		t.Errorf("text of %d runes rejected: %v", MaxTextRunes, err)
	}
	if err := ValidateItems([]Item{{ID: 1, Text: strings.Repeat("a", MaxTextRunes+1)}}); err == nil {
		t.Error("overlong text accepted")
	}
	// Runes, not bytes: 64 two-byte runes fit.
	if err := ValidateItems([]Item{{ID: 1, Text: strings.Repeat("é", MaxTextRunes)}}); err != nil {
		t.Errorf("64 multi-byte runes rejected: %v", err)
	}
	for _, bad := range []string{"two\nlines", "tab\there", "nul\x00", "del\x7f"} {
		if err := ValidateItems([]Item{{ID: 1, Text: bad}}); err == nil {
			t.Errorf("control character accepted in %q", bad)
		}
	}
}

func TestValidateItemsSeparatorMustBeBare(t *testing.T) {
	if err := ValidateItems([]Item{{Separator: true}}); err != nil {
		t.Errorf("bare separator rejected: %v", err)
	}
	if err := ValidateItems([]Item{{Separator: true, Text: "x"}}); err == nil {
		t.Error("separator with text accepted")
	}
	if err := ValidateItems([]Item{{Separator: true, ID: 4}}); err == nil {
		t.Error("separator with an ID accepted")
	}
}

func TestOptionsValidateRequiresCallbacks(t *testing.T) {
	if err := goodOptions().validate(); err != nil {
		t.Fatalf("good options rejected: %v", err)
	}
	o := goodOptions()
	o.Menu = nil
	if o.validate() == nil {
		t.Error("nil Menu accepted")
	}
	o = goodOptions()
	o.OnSelect = nil
	if o.validate() == nil {
		t.Error("nil OnSelect accepted")
	}
	o = goodOptions()
	o.OnOpen = nil
	if o.validate() == nil {
		t.Error("nil OnOpen accepted")
	}
	// OnEndSession and Logf are optional, and a nil Logf is a no-op.
	o = goodOptions()
	o.OnEndSession, o.Logf = nil, nil
	if err := o.validate(); err != nil {
		t.Errorf("optional fields required: %v", err)
	}
	o.logf("ignored %d", 1)
}

func TestOptionsValidateLimitsTheTooltip(t *testing.T) {
	o := goodOptions()
	o.Tooltip = strings.Repeat("a", MaxTooltipRunes)
	if err := o.validate(); err != nil {
		t.Errorf("127-rune tooltip rejected: %v", err)
	}
	o.Tooltip = strings.Repeat("a", MaxTooltipRunes+1)
	if o.validate() == nil {
		t.Error("128-rune tooltip accepted")
	}
	// Runes, not bytes: 127 three-byte runes fit, 128 do not.
	o.Tooltip = strings.Repeat("€", MaxTooltipRunes)
	if err := o.validate(); err != nil {
		t.Errorf("127 multi-byte runes rejected: %v", err)
	}
	o.Tooltip = strings.Repeat("€", MaxTooltipRunes+1)
	if o.validate() == nil {
		t.Error("128 multi-byte runes accepted")
	}
}
