//go:build windows

package blackbox

import (
	"errors"
	"reflect"
	"testing"
)

// TestEnumW3WPInstancesRefreshesObjectCache guards the MSDN contract:
// PdhEnumObjectItemsW returns a stale cached instance list until
// PdhEnumObjectsW(bRefresh=TRUE) refreshes it, so the refresh must run FIRST
// — a recycled pool's stale <PID>_<Pool> instance would mis-target a reused
// PID. The PDH seams are faked; no live counters are needed.
func TestEnumW3WPInstancesRefreshesObjectCache(t *testing.T) {
	var calls []string
	origRefresh, origItems := refreshW3WPObjects, enumW3WPItems
	t.Cleanup(func() { refreshW3WPObjects, enumW3WPItems = origRefresh, origItems })
	refreshW3WPObjects = func() error { calls = append(calls, "refresh"); return nil }
	enumW3WPItems = func() ([]string, error) {
		calls = append(calls, "items")
		return []string{"4321_Payroll API"}, nil
	}

	got, err := enumW3WPInstances()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(calls, []string{"refresh", "items"}) {
		t.Fatalf("PDH call order = %v, want [refresh items] (cache refreshed before enumerating)", calls)
	}
	if !reflect.DeepEqual(got, []string{"4321_Payroll API"}) {
		t.Fatalf("instances = %v, want the enumerated list", got)
	}
}

// TestEnumW3WPInstancesRefreshFailureSkipsItems guards: a failed refresh
// aborts the enumeration (never a stale list) and the items call is not made.
func TestEnumW3WPInstancesRefreshFailureSkipsItems(t *testing.T) {
	origRefresh, origItems := refreshW3WPObjects, enumW3WPItems
	t.Cleanup(func() { refreshW3WPObjects, enumW3WPItems = origRefresh, origItems })
	refreshW3WPObjects = func() error { return errors.New("PdhEnumObjectsW: 0xc0000bb8") }
	itemsCalled := false
	enumW3WPItems = func() ([]string, error) { itemsCalled = true; return nil, nil }

	if _, err := enumW3WPInstances(); err == nil {
		t.Fatal("a failed refresh must fail the enumeration")
	}
	if itemsCalled {
		t.Fatal("items must not be enumerated after a failed refresh (stale cache)")
	}
}
