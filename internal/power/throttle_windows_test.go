//go:build windows

package power

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

type infoCall struct {
	class, first, control, state uint32
}

func TestFallbackOrderRetriesInvalidParameter(t *testing.T) {
	var calls []infoCall
	var priorities []uint32
	throttler := &windowsThrottler{
		build: func() uint32 { return 19045 },
		setInfo: func(class, first, control, state uint32) error {
			calls = append(calls, infoCall{class, first, control, state})
			if class == processPowerThrottling && len(calls) == 1 {
				return windows.ERROR_INVALID_PARAMETER
			}
			return nil
		},
		setPriority: func(class uint32) error {
			priorities = append(priorities, class)
			return nil
		},
	}
	if err := throttler.apply(Idle); err != nil {
		t.Fatal(err)
	}
	want := []infoCall{
		{processPowerThrottling, powerThrottlingVersion, executionSpeed | ignoreTimerResolution, executionSpeed | ignoreTimerResolution},
		{processPowerThrottling, powerThrottlingVersion, executionSpeed, executionSpeed},
		{processMemoryPriority, memoryPriorityVeryLow, 0, 0},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
	if len(priorities) != 0 {
		t.Fatalf("unexpected priority fallback: %v", priorities)
	}
}

func TestFallbackOrderUsesBelowNormalForOtherErrors(t *testing.T) {
	var priorities []uint32
	throttler := &windowsThrottler{
		build: func() uint32 { return 19045 },
		setInfo: func(class, first, control, state uint32) error {
			if class == processPowerThrottling && state != 0 {
				return windows.ERROR_ACCESS_DENIED
			}
			return nil
		},
		setPriority: func(class uint32) error {
			priorities = append(priorities, class)
			return nil
		},
	}
	if err := throttler.apply(Idle); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(priorities, []uint32{windows.BELOW_NORMAL_PRIORITY_CLASS}) {
		t.Fatalf("priority calls = %v", priorities)
	}
	if err := throttler.apply(Normal); err != nil {
		t.Fatal(err)
	}
	if got := priorities[len(priorities)-1]; got != windows.NORMAL_PRIORITY_CLASS {
		t.Fatalf("normal priority = %#x", got)
	}
}

func TestServer2016SkipsEcoQoS(t *testing.T) {
	var calls []infoCall
	var priorities []uint32
	throttler := &windowsThrottler{
		build: func() uint32 { return 14393 },
		setInfo: func(class, first, control, state uint32) error {
			calls = append(calls, infoCall{class, first, control, state})
			return nil
		},
		setPriority: func(class uint32) error {
			priorities = append(priorities, class)
			return nil
		},
	}
	if err := throttler.apply(Idle); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].class != processMemoryPriority {
		t.Fatalf("Server 2016 calls = %#v", calls)
	}
	if !reflect.DeepEqual(priorities, []uint32{windows.BELOW_NORMAL_PRIORITY_CLASS}) {
		t.Fatalf("priority calls = %v", priorities)
	}
}

func TestNormalRestoreReportsPowerFailure(t *testing.T) {
	failure := errors.New("power clear failed")
	var priorities []uint32
	throttler := &windowsThrottler{
		build: func() uint32 { return 19045 },
		setInfo: func(class, first, control, state uint32) error {
			if class == processPowerThrottling {
				return failure
			}
			return nil
		},
		setPriority: func(class uint32) error {
			priorities = append(priorities, class)
			return nil
		},
		fallback: true,
	}
	if err := throttler.apply(Normal); !errors.Is(err, failure) {
		t.Fatalf("normal restore error = %v, want power clear failure", err)
	}
	if !reflect.DeepEqual(priorities, []uint32{windows.NORMAL_PRIORITY_CLASS}) {
		t.Fatalf("priority calls = %v", priorities)
	}
	if throttler.fallback {
		t.Fatal("priority fallback remained active after it was restored")
	}
}

func TestRestoreReportsMemoryFailure(t *testing.T) {
	failure := errors.New("memory priority failed")
	throttler := &windowsThrottler{
		build: func() uint32 { return 19045 },
		setInfo: func(class, first, control, state uint32) error {
			if class == processMemoryPriority {
				return failure
			}
			return nil
		},
		setPriority: func(uint32) error { return nil },
	}
	if err := throttler.apply(Normal); !errors.Is(err, failure) {
		t.Fatalf("normal restore error = %v, want memory failure", err)
	}
}

func TestIdleRollsBackPowerAfterMemoryFailure(t *testing.T) {
	failure := errors.New("memory priority failed")
	var calls []infoCall
	throttler := &windowsThrottler{
		build: func() uint32 { return 19045 },
		setInfo: func(class, first, control, state uint32) error {
			calls = append(calls, infoCall{class, first, control, state})
			if class == processMemoryPriority && first == memoryPriorityVeryLow {
				return failure
			}
			return nil
		},
		setPriority: func(uint32) error { return nil },
	}
	if err := throttler.apply(Idle); !errors.Is(err, failure) {
		t.Fatalf("idle error = %v, want memory failure", err)
	}
	want := []infoCall{
		{processPowerThrottling, powerThrottlingVersion, executionSpeed | ignoreTimerResolution, executionSpeed | ignoreTimerResolution},
		{processMemoryPriority, memoryPriorityVeryLow, 0, 0},
		{processPowerThrottling, powerThrottlingVersion, executionSpeed | ignoreTimerResolution, 0},
		{processMemoryPriority, memoryPriorityNormal, 0, 0},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %#v, want %#v", calls, want)
	}
}

func TestIdleRollsBackFallbackAfterMemoryFailure(t *testing.T) {
	failure := errors.New("memory priority failed")
	var priorities []uint32
	throttler := &windowsThrottler{
		build: func() uint32 { return 14393 },
		setInfo: func(class, first, control, state uint32) error {
			if class == processMemoryPriority && first == memoryPriorityVeryLow {
				return failure
			}
			return nil
		},
		setPriority: func(class uint32) error {
			priorities = append(priorities, class)
			return nil
		},
	}
	if err := throttler.apply(Idle); !errors.Is(err, failure) {
		t.Fatalf("idle error = %v, want memory failure", err)
	}
	if !reflect.DeepEqual(priorities, []uint32{windows.BELOW_NORMAL_PRIORITY_CLASS, windows.NORMAL_PRIORITY_CLASS}) {
		t.Fatalf("priority calls = %v", priorities)
	}
	if throttler.fallback {
		t.Fatal("priority fallback remained active after rollback")
	}
}

func TestSetProcessInformationReal(t *testing.T) {
	if windows.RtlGetVersion().BuildNumber < ecoQoSMinimumBuild {
		t.Skip("EcoQoS is not available on this Windows version")
	}
	if err := Apply(Idle); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := Apply(Normal); err != nil {
			t.Errorf("restore normal power mode: %v", err)
		}
	}()
}

func TestPowerOnlyCurrentProcess(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, err := os.ReadFile(name) //nolint:gosec // name comes from the package directory listing
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), "OpenProcess") {
			t.Fatalf("%s can open another process", name)
		}
	}
}

func TestPowerStructureLayout(t *testing.T) {
	state := struct{ version, control, mask uint32 }{}
	if unsafe.Sizeof(state) != 12 {
		t.Fatalf("power throttling state size = %d", unsafe.Sizeof(state))
	}
}
