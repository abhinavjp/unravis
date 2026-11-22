package blackbox

import (
	"errors"
	"fmt"
)

// errPDHAbsent marks a PDH failure that means "this host has no such object or
// counter" (for example no VMware Tools performance library). It is not a
// failure of the reader: a group may treat it as "not applicable on this host".
var errPDHAbsent = errors.New("PDH object or counter is not present on this host")

// pdhAbsentStatus reports the PDH statuses that mean "no such object or
// counter": 0xC0000BB8 PDH_CSTATUS_NO_OBJECT, 0xC0000BB9 PDH_CSTATUS_NO_COUNTER,
// 0xC0000BBF PDH_CSTATUS_NO_COUNTERNAME, 0xC0000BC0 PDH_CSTATUS_BAD_COUNTERNAME.
// The values are from memory of pdhmsg.h and are confirmed on a real Windows
// PDH by the hosted-runner test in the vm group PR; the set is deliberately
// small. Any other status stays a real failure.
func pdhAbsentStatus(code uint32) bool {
	switch code {
	case 0xC0000BB8, 0xC0000BB9, 0xC0000BBF, 0xC0000BC0:
		return true
	}
	return false
}

// pdhStatusErr is a PDH call that returned a non-zero status. Error() keeps the
// text the code used before this type existed ("PdhAddEnglishCounter %q: 0x%x"),
// and the status stays readable through errors.Is(err, errPDHAbsent).
type pdhStatusErr struct {
	call string
	path string
	code uint32
}

func (e *pdhStatusErr) Error() string {
	return fmt.Sprintf("%s %q: 0x%x", e.call, e.path, e.code)
}

// Is makes errors.Is(err, errPDHAbsent) true when the status is an absent one.
func (e *pdhStatusErr) Is(target error) bool {
	return target == errPDHAbsent && pdhAbsentStatus(e.code)
}
