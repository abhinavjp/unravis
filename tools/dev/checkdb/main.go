// Command checkdb checks the case databases in a data folder after Unravis was stopped (spec 01 BL-303). It opens each case the way the
// app does (sealed for the current Windows user), so run it as the person who owns the data, on a COPY of a real data folder, never on
// the live one: opening a case can touch it.
//
//	checkdb -data <folder> [-case <id>] [-list] [-no-wal] [-texts <file>]
//
// The cases are read from <folder>\cases. -list prints "<id> ok|locked|failed" for each case and stops. Otherwise each selected case
// must open and pass PRAGMA integrity_check; with -no-wal no -wal, -shm or -journal file may be left under the cases folder; with
// -texts every line of the file must be the text of a to-do item of the (one) selected case. It exits 1 when any check fails.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/casestore/sqlite"
	"api-audit-explorer/internal/sealedfile"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, sealedfile.CurrentUser); err != nil {
		fmt.Fprintln(os.Stderr, "checkdb:", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer, seal func() (sealedfile.Protector, error)) error {
	fs0 := flag.NewFlagSet("checkdb", flag.ContinueOnError)
	data := fs0.String("data", "", "data folder (cases are in <data>\\cases)")
	only := fs0.String("case", "", "check this case only")
	list := fs0.Bool("list", false, "list the cases and whether each opens")
	noWAL := fs0.Bool("no-wal", false, "fail when a -wal, -shm or -journal file is left")
	texts := fs0.String("texts", "", "file with one to-do text per line; each must be in the selected case")
	if err := fs0.Parse(args); err != nil {
		return err
	}
	if *data == "" {
		return errors.New("-data is required")
	}
	root := filepath.Join(*data, "cases")
	// Looked at first: opening the store or a case may tidy such files itself.
	var left []string
	if *noWAL && !*list {
		var err error
		if left, err = walFiles(root); err != nil {
			return err
		}
	}
	p, err := seal()
	if err != nil {
		return fmt.Errorf("sealing is unavailable here: %w", err)
	}
	store, err := sqlite.New(sqlite.Options{Root: root, Protector: p})
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()
	ctx := context.Background()
	infos, err := store.List(ctx)
	if err != nil {
		return err
	}
	var ids []casestore.CaseID
	for _, in := range infos {
		if *only == "" || string(in.ID) == *only {
			ids = append(ids, in.ID)
		}
	}
	if *only != "" && len(ids) == 0 {
		return fmt.Errorf("case %s not found under %s", *only, root)
	}
	if *list {
		for _, id := range ids {
			fmt.Fprintln(out, id, openStatus(ctx, store, id))
		}
		return nil
	}
	if *texts != "" && len(ids) != 1 {
		return errors.New("-texts needs exactly one case (use -case)")
	}
	var bad []string
	for _, id := range ids {
		if msg := checkCase(ctx, store, id, *texts, out); msg != "" {
			bad = append(bad, fmt.Sprintf("%s: %s", id, msg))
		}
	}
	if *noWAL {
		if len(left) > 0 {
			bad = append(bad, "files left behind: "+strings.Join(left, ", "))
		}
		fmt.Fprintf(out, "left-over -wal/-shm/-journal files: %d\n", len(left))
	}
	if len(bad) > 0 {
		return errors.New(strings.Join(bad, "; "))
	}
	fmt.Fprintf(out, "OK: %d case(s) checked\n", len(ids))
	return nil
}

func openStatus(ctx context.Context, s *sqlite.Store, id casestore.CaseID) string {
	c, err := s.Open(ctx, id)
	switch {
	case err == nil:
		_ = c.Close()
		return "ok"
	case errors.Is(err, casestore.ErrLocked):
		return "locked"
	default:
		return "failed: " + err.Error()
	}
}

// checkCase returns "" when the case passes, else why not.
func checkCase(ctx context.Context, s *sqlite.Store, id casestore.CaseID, textsFile string, out io.Writer) string {
	c, err := s.Open(ctx, id)
	if err != nil {
		return "open: " + err.Error()
	}
	defer func() { _ = c.Close() }()
	chk, ok := c.(interface{ IntegrityCheck(context.Context) error })
	if !ok {
		return "the case has no integrity check"
	}
	if err := chk.IntegrityCheck(ctx); err != nil {
		return "integrity_check: " + err.Error()
	}
	fmt.Fprintf(out, "%s integrity_check ok\n", id)
	if textsFile == "" {
		return ""
	}
	lister, ok := c.(interface {
		ChecklistItems(context.Context) ([]casestore.ChecklistItem, error)
	})
	if !ok {
		return "the case cannot list its checklist"
	}
	items, err := lister.ChecklistItems(ctx)
	if err != nil {
		return "checklist: " + err.Error()
	}
	have := map[string]int{}
	for _, it := range items {
		if it.List == casestore.ListToDo {
			have[it.Text]++
		}
	}
	f, err := os.Open(textsFile)
	if err != nil {
		return err.Error()
	}
	defer func() { _ = f.Close() }()
	var want, missing int
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		t := strings.TrimSpace(sc.Text())
		if t == "" {
			continue
		}
		want++
		if have[t] == 0 {
			missing++
		}
	}
	if err := sc.Err(); err != nil {
		return err.Error()
	}
	fmt.Fprintf(out, "%s saved lines: %d expected, %d missing\n", id, want, missing)
	if want == 0 {
		return "the texts file has no lines (the load never ran)"
	}
	if missing > 0 {
		return fmt.Sprintf("%d of %d acknowledged lines are missing", missing, want)
	}
	return ""
}

func walFiles(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		for _, suffix := range []string{"-wal", "-shm", "-journal"} {
			if strings.HasSuffix(p, suffix) {
				out = append(out, p)
			}
		}
		return nil
	})
	return out, err
}
