package server

import "net/http"

// bbDriveInfo is the safe description of one drive on this PC. It carries no
// root path or operating-system error text. Nil byte counts mean the space
// could not be read; they are null in JSON, never zero.
type bbDriveInfo struct {
	Letter     string  `json:"letter"`
	Label      string  `json:"label"`
	Type       string  `json:"type"`
	System     bool    `json:"system"`
	FreeBytes  *uint64 `json:"freeBytes"`
	TotalBytes *uint64 `json:"totalBytes"`
}

type bbDrivesAnswer struct {
	Drives []bbDriveInfo `json:"drives"`
}

// bbDrivesRead is replaceable by tests so a request never needs to inspect the
// real machine. The production value is the platform implementation.
var bbDrivesRead = listDrives

// bbDrives lists this PC's drives without changing anything. A platform that
// cannot list drives answers with an empty list.
func (h *handler) bbDrives(w http.ResponseWriter, _ *http.Request) {
	if !h.bbReady(w) {
		return
	}
	drives := bbDrivesRead()
	if drives == nil {
		drives = []bbDriveInfo{}
	}
	writeJSON(w, http.StatusOK, bbDrivesAnswer{Drives: drives}, 64<<10)
}
