package blackbox

import "errors"

// ErrSettingsNotProven says the settings file could not be proven locked and read in one go: there was no reader, or the reader refused or
// failed. It carries no path and no system text, so nothing leaks through it; the service maps it to its one fixed "not locked" sentence.
var ErrSettingsNotProven = errors.New("blackbox: the settings file was not proven locked")

// The fixed words that say why the settings reader refused. They are the only cause that is logged: a word from this list, never a path,
// never a Windows message and never anything read from the file, so a refusal on a real PC can be told apart without leaking.
const (
	WordNoReader   = "no-reader"  // the service was started without a reader
	WordPath       = "path"       // not a plain local drive path
	WordChain      = "chain"      // a link, a reparse point or a non-folder on the way to the file, or the file itself, by attributes
	WordNotFound   = "not-found"  // no such file
	WordSharing    = "sharing"    // another program holds the file without sharing it, after the retries
	WordAccess     = "access"     // Windows refused the open or a read of the owner or list
	WordNotDisk    = "not-a-file" // not a file on a disk
	WordFolder     = "folder"     // the handle is a folder
	WordLink       = "link"       // the handle is a reparse point
	WordNames      = "names"      // more than one name (a hard link)
	WordTooLarge   = "too-large"  // over maxConfigBytes
	WordMoved      = "moved"      // the real path of the handle is not the path asked for
	WordOwner      = "owner"      // not owned by SYSTEM or Administrators
	WordList       = "list"       // somebody else may change the file
	WordUnreadable = "unreadable" // any other failure
)

// SettingsRefusal is a refusal with its fixed word. Its text is for tests and for the reader's own callers; ParseVerifiedSettings drops it
// and logs only the word.
type SettingsRefusal struct {
	Word string
	err  error
}

func (r *SettingsRefusal) Error() string { return r.err.Error() }
func (r *SettingsRefusal) Unwrap() error { return r.err }

// refuse tags err with its fixed word.
func refuse(word string, err error) error { return &SettingsRefusal{Word: word, err: err} }

// RefusalWord is the fixed word for an error from the settings reader: its own word, or WordUnreadable for anything untagged.
func RefusalWord(err error) string {
	var r *SettingsRefusal
	if errors.As(err, &r) {
		return r.Word
	}
	return WordUnreadable
}

// ParseVerifiedSettings is how the SQL service gets its settings: read is the only thing that touches the disk (OSEnv.ReadVerifiedSettings,
// which opens the file once and proves it on the handle it reads from), and the bytes it returns are parsed here. There is no second open
// of path, so the file that was proven is the file that is used.
//
//   - a nil read, or a read that returns an error, is ErrSettingsNotProven (fail closed; the cause is dropped, so no text leaks), and note,
//     when it is not nil, is told the one fixed word for it (RefusalWord) so the service can log why;
//   - bytes that were read are parsed by ParseConfig, and its error is returned as it is: a proven file with bad JSON or bad values is a
//     settings error, not a file that is "not locked".
//
// It lives apart from cmd/unravis-blackbox so the rule can be tested on every platform.
func ParseVerifiedSettings(read func(path string) ([]byte, error), path string, note func(word string)) (Config, error) {
	if read == nil {
		if note != nil {
			note(WordNoReader)
		}
		return Config{}, ErrSettingsNotProven
	}
	data, err := read(path)
	if err != nil {
		if note != nil {
			note(RefusalWord(err))
		}
		return Config{}, ErrSettingsNotProven
	}
	return ParseConfig(data)
}
