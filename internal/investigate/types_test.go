package investigate

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestInputNewFieldsOmitted(t *testing.T) {
	b, err := json.Marshal(Input{})
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"sqlPresent", "sqlEvents", "sqlKinds", "httpErrPresent", "httpErr", "iisSubStatus"} {
		if strings.Contains(string(b), k) {
			t.Errorf("Input{} JSON contains %q: %s", k, b)
		}
	}
	b, err = json.Marshal(IISRow{})
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"status", "subStatus"} {
		if strings.Contains(string(b), k) {
			t.Errorf("IISRow{} JSON contains %q: %s", k, b)
		}
	}
}
