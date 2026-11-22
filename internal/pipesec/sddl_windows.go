//go:build windows

package pipesec

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// pipeMask is read data, write data, create pipe instance, read attributes and synchronize (0x100087). Never FILE_GENERIC_WRITE.
const pipeMask = 0x100087

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

// pipeSDDL builds the one DACL both pipes use: a single allow entry for the current logon SID. No Everyone, no network, no null DACL.
func pipeSDDL() (string, error) {
	sid, err := logonSID(windows.GetCurrentProcessToken())
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("D:P(A;;0x%x;;;%s)", pipeMask, sid.String()), nil
}
