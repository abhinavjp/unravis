package evidence

import "testing"

// The mongo-profile policy lets usedDisk out as a boolean and nothing else.
func TestCaseExportAllowsUsedDisk(t *testing.T) {
	p, ok := exportPolicy["mongo-profile"]["usedDisk"]
	if !ok {
		t.Fatal("mongo-profile has no usedDisk policy")
	}
	for _, v := range []string{"true", "false"} {
		if got := exportAttr([]byte("usedDisk"), p, v); got != v || !attrValueOK(p, got) {
			t.Errorf("usedDisk %q exported as %q", v, got)
		}
	}
	for _, v := range []string{"maybe", "1", "a free text sentence", ""} {
		if got := exportAttr([]byte("usedDisk"), p, v); got != removedText {
			t.Errorf("usedDisk %q exported as %q, want removed", v, got)
		}
	}
}
