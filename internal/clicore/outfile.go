package clicore

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// WriteNewFile writes data to path for --output-file: it makes a temporary file in the same folder and puts it in place, so a reader
// never sees half a file. An existing file is refused unless overwrite is set, and a path that is a folder or a link is refused
// always. The file is private to the account (0600). Without overwrite the file is put in place with a hard link, which fails when the
// name exists, so a file that another process made in the meantime is never replaced; on a file system with no hard links the check
// is made twice and then renamed (a narrow window remains there). With overwrite, the replaced file's mode and owner are not kept.
// A link in a parent folder is followed. A problem with the arguments is INVALID_ARGUMENT (exit 2); a failure of the disk after that
// is INTERNAL (exit 1).
func WriteNewFile(path string, data []byte, overwrite bool) *Error {
	bad := func(msg string) *Error { return NewError(CodeInvalidArgument, msg, "") }
	failed := func() *Error { return NewError(CodeInternal, "The output file could not be written.", "") }
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
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return failed()
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return failed()
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return failed()
	}
	if !overwrite {
		err := os.Link(name, path)
		switch {
		case err == nil:
			_ = os.Remove(name)
			return nil
		case errors.Is(err, fs.ErrExist):
			_ = os.Remove(name)
			return bad("The output file exists. Add --overwrite to replace it.")
		}
		// no hard links here: check again and rename
	}
	if e := check(); e != nil {
		_ = os.Remove(name)
		return e
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return failed()
	}
	return nil
}
