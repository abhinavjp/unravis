//go:build windows

package setup

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// The PATH of this PC, read and written in the registry. NOT RUN: needs a clean VM (installer-and-releases.md section 8 lists the
// proofs).

// osEnv reads and writes the PATH in the registry. where is the one seam for tests: nil means the real hive and key; a test points it at a
// scratch key, so the code is run for real without touching the PATH of the PC.
type osEnv struct {
	where func(root Root) (registry.Key, string)
}

func envRootKey(root Root) registry.Key {
	if root == HKLM {
		return registry.LOCAL_MACHINE
	}
	return registry.CURRENT_USER
}

// readPath returns the PATH text and its type. A missing value reads as empty and expandable. Only REG_SZ and REG_EXPAND_SZ are
// accepted, so a value of another type is never overwritten. The text is not expanded: %SystemRoot% stays as written.
func (osEnv) readPath(k registry.Key) (string, uint32, error) {
	v, typ, err := k.GetStringValue(EnvPathName)
	if errors.Is(err, registry.ErrNotExist) {
		return "", registry.EXPAND_SZ, nil
	}
	if err != nil {
		if errors.Is(err, registry.ErrUnexpectedType) {
			return "", 0, fmt.Errorf("the PATH value is not text: %w", ErrWrongType)
		}
		return "", 0, err
	}
	if typ != registry.SZ && typ != registry.EXPAND_SZ {
		return "", 0, fmt.Errorf("the PATH value is not text: %w", ErrWrongType)
	}
	return v, typ, nil
}

func (e osEnv) open(root Root, access uint32, create bool) (registry.Key, error) {
	hive, path := envRootKey(root), envKeyFor(root)
	if e.where != nil {
		hive, path = e.where(root)
	}
	if create {
		k, _, err := registry.CreateKey(hive, path, access)
		return k, err
	}
	return registry.OpenKey(hive, path, access)
}

func (e osEnv) HasPathEntry(root Root, entry string) (bool, error) {
	k, err := e.open(root, registry.QUERY_VALUE, false)
	if errors.Is(err, registry.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer k.Close()
	v, _, err := e.readPath(k)
	if err != nil {
		return false, err
	}
	_, removed := removeFromPathValue(v, entry)
	return removed, nil
}

func (e osEnv) write(k registry.Key, value string, typ uint32) error {
	if typ == registry.SZ {
		return k.SetStringValue(EnvPathName, value)
	}
	return k.SetExpandStringValue(EnvPathName, value)
}

func (e osEnv) AddPathEntry(root Root, entry string) (bool, error) {
	if err := checkPathEntry(entry); err != nil {
		return false, err
	}
	k, err := e.open(root, registry.QUERY_VALUE|registry.SET_VALUE, true)
	if err != nil {
		return false, err
	}
	defer k.Close()
	v, typ, err := e.readPath(k)
	if err != nil {
		return false, err
	}
	out, added, changed, err := addToPathValue(v, entry)
	if err != nil || !changed {
		return false, err
	}
	if err := e.write(k, out, typ); err != nil {
		return false, err
	}
	return added, nil
}

func (e osEnv) RemovePathEntry(root Root, entry string) (bool, error) {
	k, err := e.open(root, registry.QUERY_VALUE|registry.SET_VALUE, false)
	if errors.Is(err, registry.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer k.Close()
	v, typ, err := e.readPath(k)
	if err != nil {
		return false, err
	}
	out, removed := removeFromPathValue(v, entry)
	if !removed {
		return false, nil
	}
	if err := e.write(k, out, typ); err != nil {
		return false, err
	}
	return true, nil
}

// BroadcastEnvChange tells running programs (Explorer, new consoles) to read the PATH again. A hung window times out after 5 seconds.
func (osEnv) BroadcastEnvChange() error {
	const (
		hwndBroadcast   = 0xffff
		wmSettingChange = 0x001A
		smtoAbortIfHung = 0x0002
	)
	proc := windows.NewLazySystemDLL("user32.dll").NewProc("SendMessageTimeoutW")
	arg, err := windows.UTF16PtrFromString("Environment")
	if err != nil {
		return err
	}
	var result uintptr
	r, _, callErr := proc.Call(hwndBroadcast, wmSettingChange, 0, uintptr(unsafe.Pointer(arg)), smtoAbortIfHung, 5000, uintptr(unsafe.Pointer(&result)))
	if r == 0 {
		if callErr != nil && callErr != windows.ERROR_SUCCESS {
			return fmt.Errorf("broadcasting the environment change: %w", callErr)
		}
		return errors.New("broadcasting the environment change timed out")
	}
	return nil
}
