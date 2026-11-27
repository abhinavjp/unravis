package blackbox

import (
	"context"
	"encoding/json"
)

// maxSourcesBytes bounds one read of the sources manifest (it holds at most maxSourceEntries short entries).
const maxSourcesBytes = 512 << 10

// SourcesReader is implemented by a Source that can say where the logs of its server are. A Blackbox older than the
// manifest answers ReadNotFound, which a caller treats as "unknown, use the settings you have".
type SourcesReader interface {
	Sources(ctx context.Context) (SourcesDoc, error)
}

// ParseSources reads a sources manifest and refuses one of a newer schema than this build understands.
func ParseSources(b []byte) (SourcesDoc, *ReadError) {
	var d SourcesDoc
	if err := json.Unmarshal(b, &d); err != nil || d.Schema == 0 {
		return SourcesDoc{}, readErr(ReadNotFound, "That Blackbox did not give a list of where its logs are.", err)
	}
	if d.Schema > SourcesSchema {
		return SourcesDoc{}, readErr(ReadNewerFormat, "This Blackbox lists its logs in a newer way than this Unravis understands. Update Unravis to read it.", ErrNewerFormat)
	}
	if len(d.Sources) > maxSourceEntries {
		d.Sources = d.Sources[:maxSourceEntries]
	}
	return d, nil
}

// Sources reads sources.json from the recordings folder.
func (s *FolderSource) Sources(ctx context.Context) (SourcesDoc, error) {
	b, err := readFileCtx(ctx, s.Access, SourcesName, s.sourceSlots())
	if err != nil {
		return SourcesDoc{}, s.classifyFile(err, SourcesName)
	}
	d, rerr := ParseSources(b)
	if rerr != nil {
		return SourcesDoc{}, rerr
	}
	return d, nil
}

// Sources reads /sources.
func (s *HTTPSource) Sources(ctx context.Context) (SourcesDoc, error) {
	b, _, rerr := s.get(ctx, "/sources", maxSourcesBytes)
	if rerr != nil {
		return SourcesDoc{}, rerr
	}
	d, perr := ParseSources(b)
	if perr != nil {
		return SourcesDoc{}, perr
	}
	return d, nil
}
