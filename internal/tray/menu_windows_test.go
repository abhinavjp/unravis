//go:build windows

package tray

import "testing"

func TestMenuFlags(t *testing.T) {
	cases := []struct {
		name string
		it   Item
		want uint32
	}{
		{"plain", Item{ID: 1, Text: "a"}, mfString},
		{"checked", Item{ID: 1, Text: "a", Checked: true}, mfString | mfChecked},
		{"disabled", Item{ID: 1, Text: "a", Disabled: true}, mfString | mfGrayed | mfDisabled},
		{"separator", Item{Separator: true}, mfSeparator},
		{"checked+disabled", Item{ID: 1, Text: "a", Checked: true, Disabled: true}, mfString | mfChecked | mfGrayed | mfDisabled},
	}
	for _, c := range cases {
		if got := menuFlags(c.it); got != c.want {
			t.Errorf("%s: menuFlags = %#x, want %#x", c.name, got, c.want)
		}
	}
}

func TestBuildMenuHasOneEntryPerItem(t *testing.T) {
	items := []Item{{ID: 1, Text: "Open"}, {Separator: true}, {ID: 2, Text: "Quit", Checked: true}}
	menu, err := buildMenu(items)
	if err != nil {
		t.Fatal(err)
	}
	defer destroyMenu(menu)
	if n := menuItemCount(menu); n != 3 {
		t.Fatalf("menuItemCount = %d, want 3", n)
	}
}

func TestBuildMenuAcceptsAnEmptyList(t *testing.T) {
	menu, err := buildMenu(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer destroyMenu(menu)
	if menu == 0 {
		t.Fatal("menu handle is 0")
	}
	if n := menuItemCount(menu); n != 0 {
		t.Fatalf("menuItemCount = %d, want 0", n)
	}
}

func TestShowMenuRejectsInvalidItems(t *testing.T) {
	id, ok := showMenu(0, []Item{{ID: 1, Text: "a"}, {ID: 1, Text: "b"}})
	if ok || id != 0 {
		t.Fatalf("showMenu = (%d, %v), want (0, false)", id, ok)
	}
}
