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

func (r *SettingsRefusal) Error() string {
	if r.err == nil {
		return "blackbox: settings refused (" + r.Word + ")"
	}
	return r.err.Error()
}
func (r *SettingsRefusal) Unwrap() error { return r.err }

// refuse tags err with its fixed word.
func refuse(word string, err error) error { return &SettingsRefusal{Word: word, err: err} }

// NewSettingsRefusal is refuse for callers outside the package (tests): err is the cause, which is kept for the reader's own callers and
// is never logged.
func NewSettingsRefusal(word string, err error) error { return refuse(word, err) }

var knownWords = map[string]bool{
	WordNoReader: true, WordPath: true, WordChain: true, WordNotFound: true, WordSharing: true, WordAccess: true, WordNotDisk: true,
	WordFolder: true, WordLink: true, WordNames: true, WordTooLarge: true, WordMoved: true, WordOwner: true, WordList: true, WordUnreadable: true,
}

// RefusalWord is the fixed word for an error from the settings reader: its own word, or WordUnreadable for anything untagged or any word
// that is not in the list above, so free text can never reach a log line.
func RefusalWord(err error) string {
	var r *SettingsRefusal
	if errors.As(err, &r) && knownWords[r.Word] {
		return r.Word
	}
	return WordUnreadable
}

// FallbackAllowed says whether the service may, for now, fall back to the by-path proof and read after the verified read refused with this
// word. Only the three words that mean "this account or a share mode cannot do the read" (WordAccess, WordUnreadable, WordSharing) are
// allowed: they are the live unknowns of probe P1. Every other word is a verdict on the file (or no reader), and the by-path proof is weaker
// than the handle proof (it never checks the link count or the final path) and re-opens the proof-to-read window, so it stops the start.
// TEMPORARY: the fallback is removed, and this function with it, once P1 passes.
func FallbackAllowed(word string) bool {
	return word == WordAccess || word == WordUnreadable || word == WordSharing
}

// ParseVerifiedSettings is how the SQL service gets its settings: read is the only thing that touches the disk (OSEnv.ReadVerifiedSettings,
// which opens the file once and proves it on the handle it reads from), and the bytes it returns are parsed here. There is no second open
// of path, so the file that was proven is the file that is used.
//
//   - a nil read, or a read that returns an error, is ErrSettingsNotProven (fail closed; the cause is dropped, so no text leaks; the error is a SettingsRefusal that wraps it, so RefusalWord(err) gives the fixed word), and note,
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
		return Config{}, refuse(WordNoReader, ErrSettingsNotProven)
	}
	data, err := read(path)
	if err != nil {
		word := RefusalWord(err)
		if note != nil {
			note(word)
		}
		return Config{}, refuse(word, ErrSettingsNotProven)
	}
	return ParseConfig(data)
}
