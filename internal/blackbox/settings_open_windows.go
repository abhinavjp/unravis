//go:build windows

package blackbox

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

// settingsHandleProblem names the attribute bits as literals so it is tested on every platform; the build stops if one drifts.
var (
	_ [int(windows.FILE_ATTRIBUTE_DIRECTORY) - 0x10]struct{}
	_ [0x10 - int(windows.FILE_ATTRIBUTE_DIRECTORY)]struct{}
	_ [int(windows.FILE_ATTRIBUTE_REPARSE_POINT) - 0x400]struct{}
	_ [0x400 - int(windows.FILE_ATTRIBUTE_REPARSE_POINT)]struct{}
)

// Test hooks. afterSettingsOpen runs after the file is proven and before its bytes are read, so a test can try to change the file in that
// gap (nil in production). settingsSleep is the pause between opens that hit a sharing violation.
var (
	afterSettingsOpen func()
	settingsSleep     = time.Sleep
)

const (
	// settingsOpenRetries is how many times an open is repeated after a sharing violation (an installer or SaveConfig holds the file for a
	// moment), settingsOpenPause apart. The retry never loosens the open: it asks for the same share mode every time.
	settingsOpenRetries = 3
	settingsOpenPause   = 100 * time.Millisecond
)

// readVerifiedSettings is the settings reader the SQL service uses at every start: the checks that need no handle (a plain local drive
// path, no link and no reparse point on the way, by attributes only; the service holds just traverse on the program folder, so it cannot
// read the folders' access lists, which install-sql and enable-sql proved), then readOpenedSettings.
func readVerifiedSettings(path string) ([]byte, error) {
	if err := settingsPathProblem(path); err != nil {
		return nil, refuse(WordPath, err)
	}
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) {
		return nil, refuse(WordPath, fmt.Errorf("blackbox: the settings file path must be absolute"))
	}
	if err := localDriveProblem(clean[:2]); err != nil {
		return nil, refuse(WordPath, err)
	}
	if err := refuseSettingsFileChain(clean); err != nil {
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
			return nil, refuse(WordNotFound, err)
		}
		return nil, refuse(WordChain, err)
	}
	return readOpenedSettings(clean)
}

// readOpenedSettings opens path once, without following a link and with a share mode that lets nobody write, rename or delete the file while
// it is open, proves the file on that handle and reads at most maxConfigBytes from the same handle. The path is never opened again.
//
//  1. CreateFile(GENERIC_READ, FILE_SHARE_READ, OPEN_EXISTING, OPEN_REPARSE_POINT). GENERIC_READ includes READ_CONTROL, which the owner and
//     list need; the service holds FILE_GENERIC_READ on the file (maskSQLSettings).
//  2. On the handle: a disk file, not a folder, not a reparse point, one name, within maxConfigBytes (settingsHandleProblem).
//  3. The handle's real path equals the path asked for, so a folder above that was swapped for a link after the chain check is caught.
//  4. Owner SYSTEM or Administrators (trustedFileOwner) and a list nobody else can change (adminOnlyDACL), both read from the handle.
//  5. The bytes come from the same handle.
func readOpenedSettings(path string) ([]byte, error) {
	clean := filepath.Clean(path)
	p, err := windows.UTF16PtrFromString(clean)
	if err != nil {
		return nil, err
	}
	var h windows.Handle
	for try := 0; ; try++ {
		h, err = windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING,
			windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
		if err == nil {
			break
		}
		if !errors.Is(err, windows.ERROR_SHARING_VIOLATION) || try >= settingsOpenRetries {
			return nil, refuse(openWord(err), err)
		}
		settingsSleep(settingsOpenPause)
	}
	// The handle is closed on every refusal; once step 5 starts, f owns it.
	owned := false
	defer func() {
		if !owned {
			windows.CloseHandle(h)
		}
	}()

	t, err := windows.GetFileType(h)
	if err != nil {
		return nil, refuse(WordUnreadable, err)
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return nil, refuse(WordUnreadable, err)
	}
	size := uint64(info.FileSizeHigh)<<32 | uint64(info.FileSizeLow)
	if err := settingsHandleProblem(t == windows.FILE_TYPE_DISK, info.FileAttributes, info.NumberOfLinks, size); err != nil {
		return nil, err
	}
	final, err := finalPath(h)
	if err != nil {
		return nil, refuse(WordUnreadable, err)
	}
	if !strings.EqualFold(filepath.Clean(final), clean) {
		return nil, refuse(WordMoved, errors.New("blackbox: the settings file is reached through a link or an alias"))
	}
	sd, err := windows.GetSecurityInfo(h, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return nil, refuse(WordAccess, err)
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return nil, refuse(WordAccess, err)
	}
	if owner == nil || !trustedFileOwner(owner.String()) {
		return nil, refuse(WordOwner, errors.New("blackbox: the settings file has an untrusted owner"))
	}
	list, err := describeSD(sd, "the settings file")
	if err != nil {
		return nil, refuse(WordAccess, err)
	}
	aces := make([]fileACE, 0, len(list.entries))
	for _, e := range list.entries {
		aces = append(aces, e.ace)
	}
	if err := adminOnlyDACL(list.present, aces); err != nil {
		return nil, refuse(WordList, err)
	}
	if afterSettingsOpen != nil {
		afterSettingsOpen()
	}
	f := os.NewFile(uintptr(h), clean)
	owned = true // f.Close closes the handle, once
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxConfigBytes+1))
	if err != nil {
		return nil, refuse(WordUnreadable, err)
	}
	if len(data) > maxConfigBytes {
		return nil, refuse(WordTooLarge, errors.New("blackbox: the settings file is too large"))
	}
	return data, nil
}

// openWord is the fixed word for a failed open.
func openWord(err error) string {
	switch {
	case errors.Is(err, windows.ERROR_FILE_NOT_FOUND), errors.Is(err, windows.ERROR_PATH_NOT_FOUND):
		return WordNotFound
	case errors.Is(err, windows.ERROR_SHARING_VIOLATION):
		return WordSharing
	case errors.Is(err, windows.ERROR_ACCESS_DENIED):
		return WordAccess
	}
	return WordUnreadable
}
