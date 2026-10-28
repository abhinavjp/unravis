package server

import (
	"context"

	"api-audit-explorer/internal/casestore"
)

// resetImpact is what a master key reset would do to the cases on this PC, said
// before the person agrees to it (spec 7.6, M12). It is counts only.
//
// A reset sets the damaged key file aside, so nothing sealed with the old master
// key can be opened again. A case with its own key is not sealed with it and is
// left alone, so it is not in MasterKeyCases and not in any warning.
type resetImpact struct {
	// MasterKeyCases are cases whose working copy is sealed under the old master
	// key. They stay unreadable after the reset.
	MasterKeyCases int `json:"masterKeyCases"`
	// OwnKeyCases are cases with their own key. The reset does not touch them.
	OwnKeyCases int `json:"ownKeyCases"`
	// ArchiveOnly is M12: archived cases whose working copy is freed and whose case
	// file was sealed under the old master key, so it can only be opened with the old
	// key. It counts none of the own-key cases and none whose working copy is kept.
	ArchiveOnly int `json:"archiveOnly"`
}

// masterKeyResetImpact counts the cases a reset would leave unreadable and the
// ones it leaves alone. It reads only the kind of each case's key file, so it
// works while the master key is damaged and never opens a case.
func (h *handler) masterKeyResetImpact(ctx context.Context) *resetImpact {
	out := &resetImpact{}
	// M12's N is the one the archive feature keeps beside the key file (a damaged
	// key file cannot open the sealed case records, so it is not counted live). A
	// case whose case file was made with its own key is not in it.
	if h.mk != nil {
		out.ArchiveOnly = readFreedNote(h.mk.dir)
	}
	if h.cases == nil {
		return out
	}
	rs, ok := h.cases.(rewrapStore)
	if !ok {
		return out
	}
	list, err := h.cases.List(ctx)
	if err != nil {
		return out
	}
	for _, info := range list {
		kind, err := rs.CaseKind(info.ID)
		if err != nil {
			continue
		}
		switch kind {
		case casestore.KindMasterKey:
			out.MasterKeyCases++
		case casestore.KindCaseKey:
			out.OwnKeyCases++
		}
	}
	return out
}
