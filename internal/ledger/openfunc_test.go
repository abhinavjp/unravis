package ledger

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// memBlob is a caller-supplied place for the ledger bytes.
type memBlob struct {
	data     []byte
	loads    int
	saves    int
	failSave error
}

func (m *memBlob) load() ([]byte, error) { m.loads++; return m.data, nil }
func (m *memBlob) save(b []byte) error {
	if m.failSave != nil {
		return m.failSave
	}
	m.saves++
	m.data = append([]byte(nil), b...)
	return nil
}

func openBlob(t *testing.T, m *memBlob) Store {
	t.Helper()
	s, err := OpenFunc(m.load, m.save)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestOpenFuncStartsEmptyFromNilOrBlankBytes(t *testing.T) {
	for name, data := range map[string][]byte{"nil": nil, "empty": {}, "blank": []byte("  \n\t")} {
		t.Run(name, func(t *testing.T) {
			m := &memBlob{data: data}
			s := openBlob(t, m)
			items, err := s.List()
			if err != nil || len(items) != 0 {
				t.Fatalf("List = %v %v", items, err)
			}
			if m.saves != 0 {
				t.Fatal("opening wrote something")
			}
		})
	}
}

func TestOpenFuncPersistsEveryChangeAndReloads(t *testing.T) {
	m := &memBlob{}
	s := openBlob(t, m)
	h, err := s.Create(demoText)
	if err != nil {
		t.Fatal(err)
	}
	if m.saves != 1 || !bytes.Contains(m.data, []byte(`"version"`)) || !bytes.Contains(m.data, []byte(demoText)) {
		t.Fatalf("after create: saves=%d data=%s", m.saves, m.data)
	}
	if _, err := s.Update(h.ID, Input{Text: demoText, State: StateSupported, EvidenceIDs: []string{"ev:abcd1234:f1"}, Notes: "n", SettlingObservation: "look", Facts: Facts{PresentK: 1, TotalN: 2}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reuse(h.ID); err != nil {
		t.Fatal(err)
	}
	if m.saves != 3 {
		t.Fatalf("saves = %d, want 3", m.saves)
	}

	again := openBlob(t, m)
	want, _ := s.List()
	got, _ := again.List()
	if len(got) != 2 || len(want) != 2 {
		t.Fatalf("reloaded %d, want 2", len(got))
	}
	for i := range want {
		if got[i].ID != want[i].ID || got[i].Text != want[i].Text || got[i].State != want[i].State ||
			got[i].Notes != want[i].Notes || got[i].SettlingObservation != want[i].SettlingObservation ||
			len(got[i].History) != len(want[i].History) || !got[i].CreatedAt.Equal(want[i].CreatedAt) {
			t.Errorf("entry %d changed across reload:\n got %+v\nwant %+v", i, got[i], want[i])
		}
	}
}

func TestOpenFuncSameBytesAsOpen(t *testing.T) {
	// The blob is the same JSON a file ledger holds, so one reads the other.
	path := t.TempDir() + "/ledger.json"
	file, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Create(demoText); err != nil {
		t.Fatal(err)
	}
	m := &memBlob{}
	fromFile, _ := file.List()
	blob := openBlob(t, m)
	imp, ok := blob.(Importer)
	if !ok {
		t.Fatal("OpenFunc store is not an Importer")
	}
	if _, err := imp.Import(fromFile); err != nil {
		t.Fatal(err)
	}
	if got, _ := openBlob(t, m).List(); len(got) != 1 || got[0].Text != demoText {
		t.Fatalf("round trip = %+v", got)
	}
}

func TestOpenFuncInvalidBytesAreRefusedAndNeverOverwritten(t *testing.T) {
	for name, data := range map[string]string{
		"not json":      `{"version":1,"hypotheses":[`,
		"unknown field": `{"version":1,"hypotheses":[],"extra":1}`,
		"bad version":   `{"version":99,"hypotheses":[]}`,
		"bad state":     `{"version":1,"hypotheses":[{"id":"a","text":"t","state":"proven","evidenceIds":[]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			m := &memBlob{data: []byte(data)}
			s, err := OpenFunc(m.load, m.save)
			if err == nil || s != nil {
				t.Fatalf("OpenFunc accepted %q", data)
			}
			if !errors.Is(err, ErrUnreadable) {
				t.Errorf("error %v does not wrap ErrUnreadable", err)
			}
			if m.saves != 0 || string(m.data) != data {
				t.Fatalf("stored bytes were touched: saves=%d", m.saves)
			}
		})
	}
}

func TestOpenFuncNeedsBothFunctionsAndReportsLoadErrors(t *testing.T) {
	m := &memBlob{}
	if _, err := OpenFunc(nil, m.save); err == nil {
		t.Error("nil load accepted")
	}
	if _, err := OpenFunc(m.load, nil); err == nil {
		t.Error("nil save accepted")
	}
	boom := errors.New("disk gone")
	_, err := OpenFunc(func() ([]byte, error) { return nil, boom }, m.save)
	if !errors.Is(err, boom) || errors.Is(err, ErrUnreadable) {
		t.Errorf("load error = %v", err)
	}
}

func TestOpenFuncFailedSaveKeepsTheLedgerAsItWas(t *testing.T) {
	m := &memBlob{}
	s := openBlob(t, m)
	h, _ := s.Create(demoText)
	before := string(m.data)

	boom := errors.New("closed case")
	m.failSave = boom
	if _, err := s.Create("another"); err == nil {
		t.Fatal("Create succeeded though save failed")
	} else if !errors.Is(err, boom) {
		t.Errorf("save error is not wrapped: %v", err)
	}
	if err := s.Delete(h.ID); err == nil {
		t.Fatal("Delete succeeded though save failed")
	}
	if items, _ := s.List(); len(items) != 1 {
		t.Fatalf("memory changed though the save failed: %d entries", len(items))
	}
	if string(m.data) != before {
		t.Fatal("stored bytes changed")
	}
}

func TestOpenFuncKeepsGuardsLimitsAndBudget(t *testing.T) {
	m := &memBlob{}
	s := openBlob(t, m)
	h, _ := s.Create(demoText)

	var guard *GuardError
	_, err := s.Update(h.ID, Input{Text: demoText, State: StateStrong, EvidenceIDs: []string{}})
	if !errors.As(err, &guard) || len(guard.Reasons) == 0 {
		t.Fatalf("strong without facts: %v", err)
	}
	if _, err := s.Create("   "); !errors.Is(err, ErrInvalid) {
		t.Errorf("empty text: %v", err)
	}
	if _, err := s.Update("nope", Input{Text: "x", State: StateOpen}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id: %v", err)
	}
	if got, err := s.Update(h.ID, Input{Text: demoText, State: StateStrong, EvidenceIDs: strongEvidence, Facts: strongFacts()}); err != nil || got.State != StateStrong {
		t.Errorf("strong with facts: %+v %v", got, err)
	}

	// The count limit.
	full := openBlob(t, &memBlob{})
	for i := 0; i < MaxHypotheses; i++ {
		if _, err := full.Create("t"); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	if _, err := full.Create("one too many"); err == nil {
		t.Error("created past MaxHypotheses")
	}
}

func TestImportCopiesEverythingWithNewIDs(t *testing.T) {
	src := openBlob(t, &memBlob{})
	a, _ := src.Create("first")
	if _, err := src.Update(a.ID, Input{
		Text: "first", State: StateSupported, EvidenceIDs: []string{"ev:abcd1234:f1"}, Notes: "note", SettlingObservation: "settle",
		Facts: Facts{PresentK: 2, TotalN: 3, Counterexamples: []string{"ep 3"}, Alternatives: []Alternative{{Text: "gc", Status: AltOpen}}, TimezoneRecorded: true},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := src.Reuse(a.ID); err != nil {
		t.Fatal(err)
	}
	items, _ := src.List()

	dst := openBlob(t, &memBlob{})
	got, err := dst.(Importer).Import(items)
	if err != nil || len(got) != len(items) {
		t.Fatalf("Import = %d %v", len(got), err)
	}
	listed, _ := dst.List()
	for i := range items {
		g, w := listed[i], items[i]
		if g.ID == w.ID || got[i].ID != g.ID {
			t.Errorf("entry %d id: copy %q source %q returned %q", i, g.ID, w.ID, got[i].ID)
		}
		if g.Text != w.Text || g.State != w.State || g.Notes != w.Notes || g.SettlingObservation != w.SettlingObservation ||
			strings.Join(g.EvidenceIDs, ",") != strings.Join(w.EvidenceIDs, ",") || g.Facts.PresentK != w.Facts.PresentK ||
			len(g.Facts.Counterexamples) != len(w.Facts.Counterexamples) || len(g.Facts.Alternatives) != len(w.Facts.Alternatives) || g.Facts.TimezoneRecorded != w.Facts.TimezoneRecorded ||
			len(g.History) != len(w.History) || !g.CreatedAt.Equal(w.CreatedAt) || !g.UpdatedAt.Equal(w.UpdatedAt) {
			t.Errorf("entry %d differs:\n got %+v\nwant %+v", i, g, w)
		}
	}
	// The copies do not share memory with the source.
	listed[0].EvidenceIDs[0] = "changed"
	if again, _ := dst.List(); again[0].EvidenceIDs[0] != "ev:abcd1234:f1" {
		t.Error("List returned the stored slice")
	}
	if src2, _ := src.List(); src2[0].EvidenceIDs[0] != "ev:abcd1234:f1" {
		t.Error("Import aliased the source")
	}

	if got, err := dst.(Importer).Import(nil); err != nil || len(got) != 0 || got == nil {
		t.Errorf("Import(nil) = %v %v", got, err)
	}
}

func TestImportIsAllOrNothing(t *testing.T) {
	m := &memBlob{}
	dst := openBlob(t, m)
	var items []Hypothesis
	for i := 0; i < MaxHypotheses+1; i++ {
		items = append(items, Hypothesis{Text: "t", State: StateOpen})
	}
	if _, err := dst.(Importer).Import(items); err == nil {
		t.Fatal("Import past MaxHypotheses succeeded")
	}
	if got, _ := dst.List(); len(got) != 0 || m.saves != 0 {
		t.Fatalf("a refused import left %d entries, %d saves", len(got), m.saves)
	}
	m.failSave = errors.New("closed")
	if _, err := dst.(Importer).Import(items[:2]); err == nil {
		t.Fatal("Import succeeded though save failed")
	}
	if got, _ := dst.List(); len(got) != 0 {
		t.Fatalf("a failed save left %d entries in memory", len(got))
	}
}
