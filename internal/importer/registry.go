package importer

import "sort"

// TieWindow is how close two Detect scores must be to count as a tie.
const TieWindow = 10

// Registry holds the known importers and picks one for a file.
type Registry struct {
	imps   []Importer
	byName map[string]Importer
}

// NewRegistry builds a registry. The first importer registered under a name
// wins; later ones with the same name are ignored.
func NewRegistry(imps ...Importer) *Registry {
	r := &Registry{byName: map[string]Importer{}}
	for _, imp := range imps {
		if imp == nil {
			continue
		}
		if _, dup := r.byName[imp.Name()]; dup {
			continue
		}
		r.byName[imp.Name()] = imp
		r.imps = append(r.imps, imp)
	}
	return r
}

// ByName finds an importer by its stable name.
func (r *Registry) ByName(name string) (Importer, bool) {
	imp, ok := r.byName[name]
	return imp, ok
}

// Names lists the registered importer names, sorted.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.imps))
	for _, imp := range r.imps {
		names = append(names, imp.Name())
	}
	sort.Strings(names)
	return names
}

// score runs Detect on the first 64 KiB, clamps it to 0..100, and treats a
// panic in an importer as "not mine".
func score(imp Importer, head []byte, fileName string) (s int) {
	defer func() {
		if recover() != nil {
			s = 0
		}
	}()
	if len(head) > HeadSize {
		head = head[:HeadSize]
	}
	s = imp.Detect(head, fileName)
	if s < 0 {
		return 0
	}
	if s > 100 {
		return 100
	}
	return s
}

// Detect scores every importer. best is the highest scorer (earliest
// registered wins an exact tie) and score its value; best is nil when no
// importer scores above zero. tied is non-nil only when more than one
// importer scored within TieWindow points of the best; it then lists the
// names of all of them, best included, sorted, and the user picks.
func (r *Registry) Detect(head []byte, fileName string) (best Importer, bestScore int, tied []string) {
	scores := make([]int, len(r.imps))
	for i, imp := range r.imps {
		scores[i] = score(imp, head, fileName)
		if scores[i] > bestScore {
			best, bestScore = imp, scores[i]
		}
	}
	if best == nil {
		return nil, 0, nil
	}
	var near []string
	for i, imp := range r.imps {
		if scores[i] > 0 && bestScore-scores[i] <= TieWindow {
			near = append(near, imp.Name())
		}
	}
	if len(near) > 1 {
		sort.Strings(near)
		tied = near
	}
	return best, bestScore, tied
}
