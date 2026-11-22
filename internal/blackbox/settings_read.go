package blackbox

import "errors"

// ErrSettingsNotProven says the settings file could not be proven locked and read in one go: there was no reader, or the reader refused or
// failed. It carries no path and no system text, so nothing leaks through it; the service maps it to its one fixed "not locked" sentence.
var ErrSettingsNotProven = errors.New("blackbox: the settings file was not proven locked")

// ParseVerifiedSettings is how the SQL service gets its settings: read is the only thing that touches the disk (OSEnv.ReadVerifiedSettings,
// which opens the file once and proves it on the handle it reads from), and the bytes it returns are parsed here. There is no second open
// of path, so the file that was proven is the file that is used.
//
//   - a nil read, or a read that returns an error, is ErrSettingsNotProven (fail closed; the cause is dropped, so no text leaks);
//   - bytes that were read are parsed by ParseConfig, and its error is returned as it is: a proven file with bad JSON or bad values is a
//     settings error, not a file that is "not locked".
//
// It lives apart from cmd/unravis-blackbox so the rule can be tested on every platform.
func ParseVerifiedSettings(read func(path string) ([]byte, error), path string) (Config, error) {
	if read == nil {
		return Config{}, ErrSettingsNotProven
	}
	data, err := read(path)
	if err != nil {
		return Config{}, ErrSettingsNotProven
	}
	return ParseConfig(data)
}
