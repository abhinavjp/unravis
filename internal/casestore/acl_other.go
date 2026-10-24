//go:build !windows

package casestore

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// ErrReparsePoint means a case path component is a symlink. Case folders are
// never followed through one (§4 rule 7).
var ErrReparsePoint = errors.New("case path contains a reparse point")

// SecureDir restricts dir to the current user. There is no Windows ACL off
// Windows; mode 0700 is the closest equivalent.
func SecureDir(dir string) error { return os.Chmod(dir, 0700) }

// RejectReparse refuses root and every folder from root down to path if any of
// them is a symlink. A component that does not exist yet is skipped.
func RejectReparse(root, path string) error {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ErrReparsePoint
	}
	parts := []string{root}
	if rel != "." {
		cur := root
		for _, p := range strings.Split(rel, string(filepath.Separator)) {
			cur = filepath.Join(cur, p)
			parts = append(parts, cur)
		}
	}
	for _, p := range parts {
		info, err := os.Lstat(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return ErrReparsePoint
		}
	}
	return nil
}
