package ledger

import "testing"

// A control character or a bidi override in any free-text field is refused: it
// would reach the exported ledger unseen and could reorder what a reader sees.
func TestTextRefusesControlAndBidiCharacters(t *testing.T) {
	s, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{
		"nul":          "bad\x00text",
		"bell":         "bad\x07text",
		"escape":       "bad\x1b[31mtext",
		"rtl override": "bad‮text",
		"isolate":      "bad⁦text",
	} {
		if _, err := s.Create(text); err == nil {
			t.Errorf("%s: create accepted the text", name)
		}
	}
	if _, err := s.Create("line one\nline two\twith a tab"); err != nil {
		t.Errorf("line break and tab must stay allowed: %v", err)
	}
}
