package ledger

import "errors"

// Open returns a Store backed by the JSON file at path ("" = in memory). STUB: Task B replaces this file.
func Open(path string) (Store, error) { return nil, errors.New("ledger: not implemented") }

// Guards lists the reasons "strong correlation" is blocked for h. STUB: Task B replaces this file.
func Guards(f Facts, evidenceIDs []string) []string { return nil }
