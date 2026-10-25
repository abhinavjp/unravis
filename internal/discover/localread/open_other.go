//go:build !windows

package localread

type systemOpener struct{}

// OpenSource implements Opener: the hardened open exists on Windows only.
func (systemOpener) OpenSource(string, FileID) (Source, Info, error) {
	return nil, Info{}, ErrNotSupportedOS
}

// EnterBackground is a no-op off Windows; call the returned function when done.
func EnterBackground() (leave func()) { return func() {} }
