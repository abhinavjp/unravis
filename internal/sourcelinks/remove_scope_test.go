package sourcelinks

import "testing"

// Two saved connections can point at the same database and collection. A source that has no selection key is named by database and
// collection alone, so removing it must not take the other connection's link with it.
func TestRemoveScopedLeavesTheLinkOfAnotherSavedConnectionWithTheSameDatabase(t *testing.T) {
	s, _ := newStore(t)
	one, two := link("saved-1", "shared"), link("saved-2", "shared")
	one.SelectionKey, two.SelectionKey = "", ""
	other := link("saved-2", "other")
	for _, l := range []Link{one, two, other} {
		if err := s.Add(l); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RemoveScoped("saved-1", "", "shared", "audit"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.List()
	if len(got) != 2 || got[0].SavedConnectionID != "saved-2" || got[0].Database != "shared" || got[1].Database != "other" {
		t.Fatalf("after removing one connection's link: %+v", got)
	}
}

func TestRemoveScopedByKeyOnlyTouchesItsOwnSavedConnection(t *testing.T) {
	s, _ := newStore(t)
	one, two := link("saved-1", "a"), link("saved-2", "b")
	one.SelectionKey, two.SelectionKey = "shared-key", "shared-key"
	_ = s.Add(one)
	_ = s.Add(two)
	if err := s.RemoveScoped("saved-2", "shared-key", "", ""); err != nil {
		t.Fatal(err)
	}
	got, _ := s.List()
	if len(got) != 1 || got[0].SavedConnectionID != "saved-1" {
		t.Fatalf("after removing by key: %+v", got)
	}
	if err := s.RemoveScoped("saved-9", "shared-key", "", ""); err != nil {
		t.Fatalf("removing nothing is not an error: %v", err)
	}
	if got, _ := s.List(); len(got) != 1 {
		t.Fatalf("a connection that holds no such link changed the file: %+v", got)
	}
}

func TestRemoveScopedNeedsASavedConnection(t *testing.T) {
	s, _ := newStore(t)
	_ = s.Add(link("saved-1", "a"))
	if err := s.RemoveScoped("", "key-a", "a", "audit"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.List(); len(got) != 1 {
		t.Fatalf("an empty saved connection must remove nothing: %+v", got)
	}
}
