//go:build windows

package pipesec

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// pipeMask is FILE_GENERIC_READ | FILE_GENERIC_WRITE (0x12019f). CreateNamedPipe with PIPE_ACCESS_DUPLEX asks for generic read and write,
// and Windows checks that request against the first instance's DACL when it creates instance 2 and up; a smaller mask (we tried
// 0x100087) fails with Access is denied (found on the first Windows CI run). It contains no WRITE_DAC and no WRITE_OWNER. Because the pool
// is full (all 16 instances are created at start), a same-user process cannot add a squatting instance even though it holds create-instance.
const pipeMask = 0x12019f

// clientAccess is what a client asks for when it opens the pipe: pipeMask without FILE_CREATE_PIPE_INSTANCE. Generic rights would
// map to bits the DACL does not grant, so the open would be denied.
const clientAccess = 0x100083

// logonSID returns the logon SID (the token group with SE_GROUP_LOGON_ID) of tok.
func logonSID(tok windows.Token) (*windows.SID, error) {
	g, err := tok.GetTokenGroups()
	if err != nil {
		return nil, err
	}
	for _, a := range g.AllGroups() {
		if a.Attributes&windows.SE_GROUP_LOGON_ID == windows.SE_GROUP_LOGON_ID {
			return a.Sid, nil
		}
	}
	return nil, fmt.Errorf("pipesec: token has no logon SID")
}

// ownerMask is READ_CONTROL. An OWNER RIGHTS (OW) entry stops Windows from giving the owner implicit READ_CONTROL and WRITE_DAC; without
// it the same user in another logon session could open an idle instance with WRITE_DAC and rewrite the DACL. READ_CONTROL stays so the
// DACL can be read back; WRITE_DAC and WRITE_OWNER are never granted.
const ownerMask = 0x20000

// pipeSDDL builds the one DACL both pipes use: an allow entry for the current logon SID, and an OWNER RIGHTS entry that grants only
// READ_CONTROL. No Everyone, no network, no null DACL.
func pipeSDDL() (string, error) {
	sid, err := logonSID(windows.GetCurrentProcessToken())
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("D:P(A;;0x%x;;;%s)(A;;0x%x;;;OW)", pipeMask, sid.String(), ownerMask), nil
}
