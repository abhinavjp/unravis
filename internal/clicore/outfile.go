package clicore

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// WriteNewFile writes data to path for --output-file: it makes a temporary file in the same folder and renames it into place, so a
// reader never sees half a file. An existing file is refused unless overwrite is set, and a path that is a folder or a link is
// refused always. The file is private to the account (0600). The check for an existing file and the rename are two steps, so another
// process that creates the same name in between can lose its file only when overwrite is set; without it the rename of a name that
// appeared in that moment is refused as well (checked again after the temporary file is ready).
func WriteNewFile(path string, data []byte, overwrite bool) *Error {
	bad := func(msg string) *Error { return NewError(CodeInvalidArgument, msg, "") }
	if path == "" {
		return bad("--output-file needs a file name.")
	}
	dir := filepath.Dir(path)
	check := func() *Error {
		fi, err := os.Lstat(path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return nil
		case err != nil:
			return bad("The output file cannot be checked.")
		case fi.Mode()&os.ModeSymlink != 0 || fi.IsDir() || !fi.Mode().IsRegular():
			return bad("The output file is a link or a folder, or not a plain file.")
		case !overwrite:
			return bad("The output file exists. Add --overwrite to replace it.")
		}
		return nil
	}
	if e := check(); e != nil {
		return e
	}
	tmp, err := os.CreateTemp(dir, ".unravis-out-*")
	if err != nil {
		return bad("The output file cannot be created in that folder.")
	}
	name := tmp.Name()
	cleanup := func() { _ = tmp.Close(); _ = os.Remove(name) }
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return bad("The output file could not be written.")
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return bad("The output file could not be written.")
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return bad("The output file could not be written.")
	}
	if e := check(); e != nil {
		_ = os.Remove(name)
		return e
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return bad("The output file could not be put in place.")
	}
	return nil
}
