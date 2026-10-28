//go:build windows

package sealedfile

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
	"unsafe"
)

var (
	crypt32             = syscall.NewLazyDLL("crypt32.dll")
	kernel32            = syscall.NewLazyDLL("kernel32.dll")
	cryptProtectData    = crypt32.NewProc("CryptProtectData")
	cryptUnprotectData  = crypt32.NewProc("CryptUnprotectData")
	localFree           = kernel32.NewProc("LocalFree")
	createMutex         = kernel32.NewProc("CreateMutexW")
	waitForSingleObject = kernel32.NewProc("WaitForSingleObject")
	releaseMutex        = kernel32.NewProc("ReleaseMutex")
	closeHandle         = kernel32.NewProc("CloseHandle")
	moveFileEx          = kernel32.NewProc("MoveFileExW")
)

type dataBlob struct {
	Size uint32
	Data *byte
}

// dpapiProtector seals with DPAPI. flags is CRYPTPROTECT_UI_FORBIDDEN (1), plus CRYPTPROTECT_LOCAL_MACHINE (4) for the machine scope.
type dpapiProtector struct{ flags uintptr }

const (
	cryptprotectUIForbidden  = 0x1
	cryptprotectLocalMachine = 0x4
)

// CurrentUser returns a Protector that seals data with Windows DPAPI for the
// current user (never the machine scope).
func CurrentUser() (Protector, error) { return dpapiProtector{flags: cryptprotectUIForbidden}, nil }

// LocalMachine returns a Protector that seals data with Windows DPAPI for this PC: any account on this PC can unseal it, no other PC can.
// It is used only for the shared data folder (cases, hosts, ledger), whose folder ACL decides who may read the files. Saved passwords and
// read keys are never sealed this way: they live in the key keeper, sealed for its own service account.
func LocalMachine() (Protector, error) {
	return dpapiProtector{flags: cryptprotectUIForbidden | cryptprotectLocalMachine}, nil
}

func (p dpapiProtector) Protect(plain []byte) ([]byte, error) {
	return dpapi(true, plain, p.flags)
}

func (p dpapiProtector) Unprotect(ciphertext []byte) ([]byte, error) {
	return dpapi(false, ciphertext, p.flags)
}

func dpapi(protect bool, input []byte, flags uintptr) ([]byte, error) {
	if len(input) == 0 {
		return nil, errors.New("empty DPAPI input")
	}
	in := dataBlob{Size: uint32(len(input)), Data: &input[0]}
	var out dataBlob
	var ok uintptr
	var callErr error
	if protect {
		ok, _, callErr = cryptProtectData.Call(uintptr(unsafe.Pointer(&in)), 0, 0, 0, 0, flags, uintptr(unsafe.Pointer(&out)))
	} else {
		ok, _, callErr = cryptUnprotectData.Call(uintptr(unsafe.Pointer(&in)), 0, 0, 0, 0, flags&cryptprotectUIForbidden, uintptr(unsafe.Pointer(&out)))
	}
	runtime.KeepAlive(input)
	if out.Data != nil {
		defer localFree.Call(uintptr(unsafe.Pointer(out.Data)))
	}
	if ok == 0 {
		return nil, fmt.Errorf("Windows DPAPI operation failed: %w", callErr)
	}
	if out.Data == nil || out.Size == 0 || out.Size > maxBlob {
		return nil, errors.New("Windows DPAPI returned invalid data")
	}
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}

// Lock takes a named, per-path mutex that also serialises separate processes.
// The caller must call the returned function to release it.
func Lock(path string) (func(), error) {
	canonical, err := filepath.Abs(path)
	if err != nil {
		return nil, ErrUnavailable
	}
	digest := sha256.Sum256([]byte(canonical))
	name, err := syscall.UTF16PtrFromString(`Local\APIAuditExplorer-` + fmtHex(digest[:16]))
	if err != nil {
		return nil, ErrUnavailable
	}
	handle, _, callErr := createMutex.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if handle == 0 {
		return nil, callErr
	}
	runtime.LockOSThread()
	result, _, _ := waitForSingleObject.Call(handle, uintptr((5*time.Second)/time.Millisecond))
	if result != 0 && result != 0x80 {
		closeHandle.Call(handle)
		runtime.UnlockOSThread()
		return nil, ErrUnavailable
	}
	return func() {
		releaseMutex.Call(handle)
		closeHandle.Call(handle)
		runtime.UnlockOSThread()
	}, nil
}

// Replace moves from over to, replacing it, and flushes the move to disk.
func Replace(from, to string) error {
	source, err := syscall.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	destination, err := syscall.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	ok, _, callErr := moveFileEx.Call(uintptr(unsafe.Pointer(source)), uintptr(unsafe.Pointer(destination)), 0x1|0x8)
	if ok == 0 {
		return callErr
	}
	return nil
}

func fmtHex(value []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(value)*2)
	for i, b := range value {
		out[2*i], out[2*i+1] = digits[b>>4], digits[b&15]
	}
	return string(out)
}
