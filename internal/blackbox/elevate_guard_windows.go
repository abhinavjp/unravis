//go:build windows

package blackbox

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// GuardHelper is the ThisPC.Guard used by the app (SEC-01). Before a permission
// prompt is shown it opens the program with writes and deletes denied, so the
// file cannot be swapped between the check and the launch, hashes that exact
// file, and asks Windows whether its signature is valid. The returned release
// function closes the lock; keep it until the program has ended.
func GuardHelper(program string) (func(), error) {
	p, err := windows.UTF16PtrFromString(program)
	if err != nil {
		return nil, err
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(h), program)
	release := func() { f.Close() }
	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		release()
		return nil, err
	}
	// f owns the handle (closing f closes it), so release is the only close.
	// WinVerifyTrust reads from the handle's position, so go back to the start.
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		release()
		return nil, err
	}
	trust := verifyTrust(program, h)
	if err := helperDecision(PinnedHelperSHA256, hex.EncodeToString(sum.Sum(nil)), trust); err != nil {
		release()
		return nil, err
	}
	return release, nil
}

// verifyTrust runs WinVerifyTrust on the open file and returns its result code.
// SEC-FIX-02: Windows documents the file path as required, so it is given along
// with the handle of the locked file; the handle makes it check that same file.
func verifyTrust(path string, h windows.Handle) uint32 {
	fp, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0xFFFFFFFF
	}
	fileInfo := windows.WinTrustFileInfo{FilePath: fp, File: h}
	fileInfo.Size = uint32(unsafe.Sizeof(fileInfo))
	data := windows.WinTrustData{
		UIChoice:                        windows.WTD_UI_NONE,
		RevocationChecks:                windows.WTD_REVOKE_NONE,
		UnionChoice:                     windows.WTD_CHOICE_FILE,
		FileOrCatalogOrBlobOrSgnrOrCert: unsafe.Pointer(&fileInfo),
		StateAction:                     windows.WTD_STATEACTION_VERIFY,
	}
	data.Size = uint32(unsafe.Sizeof(data))
	action := windows.WINTRUST_ACTION_GENERIC_VERIFY_V2
	err = windows.WinVerifyTrustEx(windows.InvalidHWND, &action, &data)
	code := uint32(0)
	if err != nil {
		if en, ok := err.(windows.Errno); ok {
			code = uint32(en)
		} else {
			code = 0xFFFFFFFF
		}
	}
	data.StateAction = windows.WTD_STATEACTION_CLOSE
	_ = windows.WinVerifyTrustEx(windows.InvalidHWND, &action, &data)
	return code
}
