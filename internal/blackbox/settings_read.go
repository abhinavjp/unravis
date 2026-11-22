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
	// fallbackOK is decided once, from the word the reader itself gave (FallbackAllowed), never from a word that was clamped for the log. The
	// zero value is STOP, so a refusal built any other way stops the start.
	fallbackOK bool
}

func (r *SettingsRefusal) Error() string {
	if r.err == nil {
		return "blackbox: settings refused (" + r.Word + ")"
	}
	return r.err.Error()
}
func (r *SettingsRefusal) Unwrap() error { return r.err }

// refuse tags err with its fixed word.
func refuse(word string, err error) error {
	return &SettingsRefusal{Word: word, err: err, fallbackOK: FallbackAllowed(word)}
}

// NewSettingsRefusal is refuse for callers outside the package (tests): err is the cause, which is kept for the reader's own callers and
// is never logged.
func NewSettingsRefusal(word string, err error) error { return refuse(word, err) }

// allWords is the one list of the words above. knownWords and Words are made from it, so a new word has one place to go.
var allWords = []string{WordNoReader, WordPath, WordChain, WordNotFound, WordSharing, WordAccess, WordNotDisk, WordFolder, WordLink, WordNames,
	WordTooLarge, WordMoved, WordOwner, WordList, WordUnreadable}

var knownWords = func() map[string]bool {
	m := make(map[string]bool, len(allWords))
	for _, w := range allWords {
		m[w] = true
	}
	return m
}()

// Words is a copy of the list of fixed words (for tests).
func Words() []string { return append([]string(nil), allWords...) }

// RefusalWord is the fixed word for an error from the settings reader, for the LOG only: its own word, or WordUnreadable for anything
// untagged or any word that is not in the list, so free text can never reach a log line. It must not be used to decide the fallback: the
// clamp turns an unknown word into a fallback word. Use FallbackAllowedFor.
func RefusalWord(err error) string {
	var r *SettingsRefusal
	if errors.As(err, &r) && knownWords[r.Word] {
		return r.Word
	}
	return WordUnreadable
}

// FallbackAllowed says whether a known refusal word may, for now, fall back to the by-path proof and read. Only WordAccess and
// WordUnreadable (this account cannot open or read the file: the live unknowns of probe P1) are allowed. Every other word, a word that is not
// in the list, and the word "sharing" stop the start: they are verdicts on the file, or (sharing) a signal an attacker can time with an
// oplock, and the by-path proof is weaker than the handle proof (no link count, no final path, no share-deny-write during the read, an
// unbounded read) and re-opens the proof-to-read window. TEMPORARY: removed, with FallbackAllowedFor, once P1 passes.
func FallbackAllowed(word string) bool {
	return knownWords[word] && (word == WordAccess || word == WordUnreadable)
}

// FallbackAllowedFor is the decision for an error from ParseVerifiedSettings (or from a reader): true only for a refusal that carries a word
// that FallbackAllowed lets through. An untagged error, an unknown word, a nil error and anything built by hand stop the start (fail closed).
func FallbackAllowedFor(err error) bool {
	var r *SettingsRefusal
	return errors.As(err, &r) && r.fallbackOK
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
		// The fallback decision is taken from the reader's own error, before the word is clamped for the log.
		return Config{}, &SettingsRefusal{Word: word, err: ErrSettingsNotProven, fallbackOK: FallbackAllowedFor(err)}
	}
	return ParseConfig(data)
}
