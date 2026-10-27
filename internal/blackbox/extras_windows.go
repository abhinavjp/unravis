//go:build windows

package blackbox

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"

	"api-audit-explorer/internal/sampler"
)

// OSCollector is the collector of the real machine: the sampler's Windows
// Source plus disk queue (PDH) and network totals (GetIfTable2Ex).
func OSCollector(processes bool) (*HostCollector, error) {
	src, err := sampler.OSSource()
	if err != nil {
		return nil, err
	}
	return NewHostCollector(src, newWinExtras(), processes), nil
}

var (
	pdh                         = windows.NewLazySystemDLL("pdh.dll")
	procPdhOpenQuery            = pdh.NewProc("PdhOpenQueryW")
	procPdhAddEnglishCounter    = pdh.NewProc("PdhAddEnglishCounterW")
	procPdhCollectQueryData     = pdh.NewProc("PdhCollectQueryData")
	procPdhGetFormattedCtrValue = pdh.NewProc("PdhGetFormattedCounterValue")
	procPdhCloseQuery           = pdh.NewProc("PdhCloseQuery")
)

const (
	pdhFmtDouble      = 0x00000200
	diskQueuePath     = `\PhysicalDisk(_Total)\Current Disk Queue Length`
	ifTypeLoopback    = 24
	ifOperStatusUp    = 1
	ifFlagHardwareBit = 0x01
)

// pdhFmtCounterValue is PDH_FMT_COUNTERVALUE with the double member.
type pdhFmtCounterValue struct {
	CStatus uint32
	_       uint32
	Double  float64
}

type winExtras struct {
	query, counter uintptr
	opened         bool
	err            error
}

func newWinExtras() *winExtras {
	w := &winExtras{}
	if err := pdh.Load(); err != nil {
		w.err = err
		return w
	}
	if r, _, _ := procPdhOpenQuery.Call(0, 0, uintptr(unsafe.Pointer(&w.query))); r != 0 {
		w.err = fmt.Errorf("PdhOpenQuery: 0x%x", r)
		return w
	}
	p, _ := windows.UTF16PtrFromString(diskQueuePath)
	if r, _, _ := procPdhAddEnglishCounter.Call(w.query, uintptr(unsafe.Pointer(p)), 0, uintptr(unsafe.Pointer(&w.counter))); r != 0 {
		procPdhCloseQuery.Call(w.query)
		w.err = fmt.Errorf("PdhAddEnglishCounter: 0x%x", r)
		return w
	}
	w.opened = true
	procPdhCollectQueryData.Call(w.query)
	return w
}

func (w *winExtras) DiskQueue() (float64, error) {
	if !w.opened {
		return 0, w.err
	}
	if r, _, _ := procPdhCollectQueryData.Call(w.query); r != 0 {
		return 0, fmt.Errorf("PdhCollectQueryData: 0x%x", r)
	}
	var v pdhFmtCounterValue
	if r, _, _ := procPdhGetFormattedCtrValue.Call(w.counter, pdhFmtDouble, 0, uintptr(unsafe.Pointer(&v))); r != 0 {
		return 0, fmt.Errorf("PdhGetFormattedCounterValue: 0x%x", r)
	}
	if v.CStatus != 0 {
		return 0, fmt.Errorf("disk queue counter status 0x%x", v.CStatus)
	}
	return v.Double, nil
}

// Network sums the byte counters of the physical interfaces that are up. When
// the machine reports none as physical, every interface that is up and not the
// loopback counts.
func (w *winExtras) Network() (uint64, uint64, error) {
	var tbl *windows.MibIfTable2
	if err := windows.GetIfTable2Ex(windows.MibIfTableNormal, &tbl); err != nil {
		return 0, 0, err
	}
	if tbl == nil {
		return 0, 0, errors.New("no interface table")
	}
	defer windows.FreeMibTable(unsafe.Pointer(tbl))
	rows := unsafe.Slice(&tbl.Table[0], tbl.NumEntries)
	var hwIn, hwOut, allIn, allOut uint64
	hw := false
	for i := range rows {
		r := &rows[i]
		if r.OperStatus != ifOperStatusUp || r.Type == ifTypeLoopback {
			continue
		}
		allIn += r.InOctets
		allOut += r.OutOctets
		if r.InterfaceAndOperStatusFlags&ifFlagHardwareBit != 0 {
			hw = true
			hwIn += r.InOctets
			hwOut += r.OutOctets
		}
	}
	if hw {
		return hwIn, hwOut, nil
	}
	return allIn, allOut, nil
}

func (w *winExtras) Close() error {
	if w.opened {
		procPdhCloseQuery.Call(w.query)
		w.opened = false
	}
	return nil
}
