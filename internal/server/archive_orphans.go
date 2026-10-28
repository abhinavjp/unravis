package server

import (
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Orphan files (P3, spec v7 section 7.7): finished case files an archive left
// behind without committing. They are listed in the health panel and never
// deleted or adopted by Unravis; a person decides. Left-over temporary stores of
// Verify are reported as "cleanup pending".
//
//	GET /api/cases/orphan-files

type orphanFileDTO struct {
	Name   string    `json:"name"`
	Folder string    `json:"folder"`
	CaseID string    `json:"caseId,omitempty"`
	Code   string    `json:"code,omitempty"`
	At     time.Time `json:"at"`
	Bytes  int64     `json:"bytes"`
}

type orphanFilesDTO struct {
	Files []orphanFileDTO `json:"files"`
	// VerifyCleanupPending is true while temporary stores of Verify are still on
	// disk and no Verify is running.
	VerifyCleanupPending bool `json:"verifyCleanupPending"`
}

func (h *handler) orphanFiles(w http.ResponseWriter, r *http.Request) {
	root, ar, ok := h.archivePrecheck(w)
	if !ok {
		return
	}
	list, err := ar.Unresolved(r.Context())
	if err != nil {
		writeStoreError(w, err)
		return
	}
	out := orphanFilesDTO{Files: []orphanFileDTO{}}
	for _, u := range list {
		fi, err := os.Lstat(filepath.Join(u.Folder, u.Name))
		if err != nil {
			if os.IsNotExist(err) {
				// The person removed it: stop listing it.
				_ = ar.DropUnresolved(r.Context(), u.Name, u.Folder)
			}
			continue
		}
		out.Files = append(out.Files, orphanFileDTO{
			Name: u.Name, Folder: u.Folder, CaseID: string(u.CaseID), Code: shortCode(u.CaseID), At: u.At.UTC(), Bytes: fi.Size(),
		})
	}
	out.VerifyCleanupPending = verifyLeftovers(root) && !h.caseFiles().jobs.anyRunning(jobVerify)
	writeJSON(w, http.StatusOK, out, maxResponseBytes)
}
