//go:build !windows

package bbsql

import "errors"

// errNoFolderACL: a DACL is a Windows object, and the Blackbox SQL service is a
// Windows service. Off Windows there is nothing to read, so there is nothing to
// prove, and CheckFolderAccess fails closed instead of pretending an absence of
// evidence is evidence of safety.
var errNoFolderACL = errors.New("bbsql: no folder access-control model on this platform")

func readFolderACL(string) (folderACL, error) { return folderACL{}, errNoFolderACL }

func folderSelfSID() string { return "" }
