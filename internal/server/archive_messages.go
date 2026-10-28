package server

import "api-audit-explorer/internal/casestore"

// The fixed messages of archive, verify, free and restore (spec v7, appendix M).
// Each one is a whole sentence the page shows as it is; none holds a path, a file
// name, a key or any case content.
const (
	importRunningText    = "An import is still running. Wait for it or stop it first."                          // M1
	caseInUseText        = "The case is in use. Close its other tabs, then try again."                          // M4
	caseArchivedText     = "Archived cases are in the Archived list on the start screen."                       // M6
	archiveFolderText    = "The folder cannot be used. Choose a folder on this computer that you can write to." // M8
	archiveNoSpaceText   = "There is not enough free space for the archive."                                    // M9
	archiveCheckText     = "The archive file could not be checked. Nothing was changed. Try again."             // M10
	caseFileFailedText   = "The case file did not pass the check. The working copy was kept."                   // M11
	oldKeyOnlyText       = "This file can only be opened with the key it was made with."                        // M13
	archiveStoppedText   = "Archiving was stopped. The case is back as it was. Try again."                      // M14
	archiveTooLargeText  = "This case is too large to archive in one file."                                     // M16
	verifyStoppedText    = "The check was stopped. Nothing was changed. Try again."                             // M17
	caseFileBusyText     = "Another case file is being saved or restored."                                      // A18
	archiveNotKeptText   = "This case is not archived, or its working copy is already gone."
	checkFirstText       = "Check the case file first."
	archiveTypedPassText = "Type a passphrase of at least 12 characters for this file."
	archiveKeyedPassText = "This case has its own key, which seals the file. Leave the passphrase empty."
	archiveKeyLockedText = "The master key is locked. Unlock it and try again."
	restoreNeedsFileText = "The case file for this case is not in the folder it was saved to."
)

// archiveMaxDB is the 8 GiB limit of one case database (M16). A case over it cannot
// be archived in one file. A variable so a test can lower it.
var archiveMaxDB int64 = casestore.MaxCaseFileDB
