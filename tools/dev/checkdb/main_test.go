package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/casestore/sqlite"
	"api-audit-explorer/internal/casestore/storetest"
	"api-audit-explorer/internal/sealedfile"
)

func TestCheckdb(t *testing.T) {
	data := t.TempDir()
	prot := storetest.RandomProtector()
	seal := func() (sealedfile.Protector, error) { return prot, nil }
	st, err := sqlite.New(sqlite.Options{Root: filepath.Join(data, "cases"), Protector: prot, MinFreeBytes: -1})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	info, err := st.Create(ctx, "t")
	if err != nil {
		t.Fatal(err)
	}
	c, err := st.Open(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.(interface {
		PutChecklistItem(context.Context, casestore.ChecklistItem) (casestore.ChecklistItem, error)
	}).PutChecklistItem(ctx, casestore.ChecklistItem{List: casestore.ListToDo, Text: "alpha", Order: casestore.OrderAuto}); err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	_ = st.Close()

	texts := filepath.Join(t.TempDir(), "texts.txt")
	write := func(s string) {
		if err := os.WriteFile(texts, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runWith := func(args ...string) (string, error) {
		var b bytes.Buffer
		err := run(append([]string{"-data", data}, args...), &b, seal)
		return b.String(), err
	}

	if out, err := runWith("-list"); err != nil || !strings.Contains(out, string(info.ID)+" ok") {
		t.Fatalf("list: %q %v", out, err)
	}
	write("alpha\n")
	if out, err := runWith("-case", string(info.ID), "-texts", texts, "-no-wal"); err != nil {
		t.Fatalf("good run: %q %v", out, err)
	}
	write("alpha\nbeta\n")
	if _, err := runWith("-case", string(info.ID), "-texts", texts); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("a missing line must fail, got %v", err)
	}
	write("")
	if _, err := runWith("-case", string(info.ID), "-texts", texts); err == nil {
		t.Fatal("an empty texts file must fail")
	}
	if err := os.WriteFile(filepath.Join(data, "cases", string(info.ID), "case.db-wal"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runWith("-no-wal"); err == nil || !strings.Contains(err.Error(), "left behind") {
		t.Fatalf("a left-over -wal must fail, got %v", err)
	}
	if _, err := runWith("-case", "nosuchcase0123456789abcdef012345"); err == nil {
		t.Fatal("an unknown case must fail")
	}
}
