//go:build windows

package power

import (
	"errors"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	processMemoryPriority  = 0
	processPowerThrottling = 4
	powerThrottlingVersion = 1
	executionSpeed         = 0x1
	ignoreTimerResolution  = 0x4
	memoryPriorityVeryLow  = 1
	memoryPriorityNormal   = 5
	ecoQoSMinimumBuild     = 16299
)

var procSetProcessInformation = windows.NewLazySystemDLL("kernel32.dll").NewProc("SetProcessInformation")

type infoSetter func(class, first, control, state uint32) error

type windowsThrottler struct {
	build       func() uint32
	setInfo     infoSetter
	setPriority func(uint32) error

	mu       sync.Mutex
	fallback bool
}

var defaultThrottler = &windowsThrottler{
	build:       func() uint32 { return windows.RtlGetVersion().BuildNumber },
	setInfo:     setProcessInformation,
	setPriority: func(class uint32) error { return windows.SetPriorityClass(windows.CurrentProcess(), class) },
}

func Apply(mode Mode) error { return defaultThrottler.apply(mode) }

func (t *windowsThrottler) apply(mode Mode) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	idle := mode == Idle
	eco := t.build() >= ecoQoSMinimumBuild
	var powerErr, priorityErr, memoryErr error
	if !idle {
		if eco {
			powerErr = t.setInfo(processPowerThrottling, powerThrottlingVersion, executionSpeed|ignoreTimerResolution, 0)
		}
		if t.fallback {
			priorityErr = t.setPriority(windows.NORMAL_PRIORITY_CLASS)
			if priorityErr == nil {
				t.fallback = false
			}
		}
		memoryErr = t.setInfo(processMemoryPriority, memoryPriorityNormal, 0, 0)
		return errors.Join(powerErr, priorityErr, memoryErr)
	}

	powerApplied := false
	if eco {
		powerErr = t.setInfo(processPowerThrottling, powerThrottlingVersion, executionSpeed|ignoreTimerResolution, executionSpeed|ignoreTimerResolution)
		if errors.Is(powerErr, windows.ERROR_INVALID_PARAMETER) {
			powerErr = t.setInfo(processPowerThrottling, powerThrottlingVersion, executionSpeed, executionSpeed)
		}
		powerApplied = powerErr == nil
		if powerApplied && t.fallback {
			priorityErr = t.setPriority(windows.NORMAL_PRIORITY_CLASS)
			if priorityErr == nil {
				t.fallback = false
			}
		}
	}
	usedFallback := !eco || powerErr != nil
	if usedFallback {
		t.fallback = true
		priorityErr = t.setPriority(windows.BELOW_NORMAL_PRIORITY_CLASS)
	}
	memoryErr = t.setInfo(processMemoryPriority, memoryPriorityVeryLow, 0, 0)
	if (powerErr == nil || usedFallback) && priorityErr == nil && memoryErr == nil {
		return nil
	}

	var rollbackErr error
	if eco && powerApplied {
		rollbackErr = t.setInfo(processPowerThrottling, powerThrottlingVersion, executionSpeed|ignoreTimerResolution, 0)
	}
	if t.fallback {
		if err := t.setPriority(windows.NORMAL_PRIORITY_CLASS); err != nil {
			rollbackErr = errors.Join(rollbackErr, err)
		} else {
			t.fallback = false
		}
	}
	rollbackErr = errors.Join(rollbackErr, t.setInfo(processMemoryPriority, memoryPriorityNormal, 0, 0))
	return errors.Join(powerErr, priorityErr, memoryErr, rollbackErr)
}

func setProcessInformation(class, first, control, state uint32) error {
	var info unsafe.Pointer
	var size uintptr
	if class == processMemoryPriority {
		value := first
		info, size = unsafe.Pointer(&value), unsafe.Sizeof(value)
	} else {
		value := struct {
			version, control, state uint32
		}{first, control, state}
		info, size = unsafe.Pointer(&value), unsafe.Sizeof(value)
	}
	result, _, callErr := procSetProcessInformation.Call(uintptr(windows.CurrentProcess()), uintptr(class), uintptr(info), size)
	if result != 0 {
		return nil
	}
	if callErr != nil {
		return callErr
	}
	return windows.ERROR_SUCCESS
}
