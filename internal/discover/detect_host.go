package discover

import (
	"context"
	"errors"
	"sort"
	"strings"

	"api-audit-explorer/internal/importer"
)

// Host sources (spec section 5): the live sampler series, Perfmon counter logs
// and .NET counters and nettrace files in site folders.
//
// Perfmon: CSV files under %SystemDrive%\PerfLogs\Admin, read to 3 folders below
// it, become log sets for win-counters. A .blg file is binary and is never
// parsed; its folder becomes an export-needed finding. .NET counters: CSV and
// JSON files that the dotnet-counters importer recognises, read from the folders
// SiteDirs names (no recursion). A .nettrace file in those folders is
// export-needed. The live sampler finding is emitted only when win-live is
// registered, so a build without the sampler does not offer a source it cannot
// read.

const hostDetectorName = "host"

const (
	importerWinLive        = "win-live"
	importerWinCounters    = "win-counters"
	importerDotnetCounters = "dotnet-counters"

	perfmonMaxDepth = 3 // folders below PerfLogs\Admin that are read
	perfmonName     = "Performance counter logs"
	siteName        = "App site folder"
	liveName        = "Unravis live sampler"
)

// SiteDirs lists the site folders the .NET counter and nettrace checks read.
// Each entry is an absolute local path. The detector does not walk down from
// them. A nil SiteDirs means no site folders are read.
type SiteDirs func(ctx context.Context) []string

// HostOptions configures the host detector. The zero value reads the system
// drive as C: and reads no site folders.
type HostOptions struct {
	SystemDrive string   // for example "C:"; empty means "C:"
	SiteDirs    SiteDirs // nil means no site folders
}

// hostDetector finds the host sources.
type hostDetector struct{ opts HostOptions }

// NewHost returns the host detector.
func NewHost(opts HostOptions) Detector { return hostDetector{opts: opts} }

func (hostDetector) Name() string { return hostDetectorName }

func (d hostDetector) Detect(ctx context.Context, env Env) ([]Finding, []Gap) {
	var findings []Finding
	var gaps []Gap
	add := func(fs []Finding, gs []Gap) {
		findings = append(findings, fs...)
		gaps = append(gaps, gs...)
	}
	add(liveSeriesFinding(env))
	if ctx.Err() == nil {
		add(d.perfmon(ctx, env))
	}
	if ctx.Err() == nil {
		add(d.siteFolders(ctx, env))
	}
	return findings, gaps
}

// liveSeriesFinding offers the live sampler's series. It is emitted only when
// the registry has win-live, or when no registry is given.
func liveSeriesFinding(env Env) ([]Finding, []Gap) {
	if env.Importers != nil {
		if _, ok := env.Importers.ByName(importerWinLive); !ok {
			return nil, nil
		}
	}
	f := Finding{
		Detector: hostDetectorName, Kind: KindLiveSeries, Importer: importerWinLive,
		Display: liveName, Confidence: 90, Shows: "discover.live.shows", Cost: CostSmall,
	}
	return []Finding{f}, nil
}

// systemRoot is the system drive's root, for example C:\.
func (d hostDetector) systemRoot() (string, error) {
	drive := d.opts.SystemDrive
	if drive == "" {
		drive = "C:"
	}
	return ValidateAbsPath(strings.TrimRight(drive, `\`) + `\`)
}

// perfmon reads %SystemDrive%\PerfLogs\Admin.
func (d hostDetector) perfmon(ctx context.Context, env Env) ([]Finding, []Gap) {
	drive, err := d.systemRoot()
	if err != nil {
		return nil, []Gap{{Detector: hostDetectorName, Kind: KindLogSet, Display: perfmonName, Reason: ReasonUnsafePath}}
	}
	root := WinJoin(drive, "PerfLogs", "Admin")
	info, err := statChecked(env.FS, root)
	switch {
	case IsNotFound(err):
		return nil, nil
	case err != nil:
		return nil, []Gap{GapFromError(hostDetectorName, KindLogSet, perfmonName, err)}
	case !info.IsDir:
		return nil, nil
	}

	tree, gaps := walkHostFiles(ctx, env.FS, root, perfmonMaxDepth, perfmonName)
	var findings []Finding
	for _, folder := range sortedFolders(tree) {
		var csvs, blgs []hostEntry
		for _, e := range tree[folder] {
			switch {
			case hasSuffixFold(e.info.Name, ".csv"):
				csvs = append(csvs, e)
			case hasSuffixFold(e.info.Name, ".blg"):
				blgs = append(blgs, e)
			}
		}
		if len(blgs) > 0 {
			findings = append(findings, hostGroupFinding(KindExportNeeded, "", "discover.perfmon.shows", "discover.perfmon.how", 70, folder, blgs))
		}
		if len(csvs) > 0 {
			f, gs, ok := perfmonLogSet(env, folder, csvs)
			gaps = append(gaps, gs...)
			if ok {
				findings = append(findings, f)
			}
		}
	}
	return findings, gaps
}

// perfmonLogSet builds the win-counters log set for one folder. Confidence is 70
// when the newest file's head confirms the importer, 40 when it does not. It
// returns ok false, with a gap, when the head cannot be read.
func perfmonLogSet(env Env, folder string, files []hostEntry) (Finding, []Gap, bool) {
	conf := 70
	if env.Importers != nil {
		if imp, ok := env.Importers.ByName(importerWinCounters); ok {
			newest := files[0]
			for _, e := range files[1:] {
				if e.info.ModTime.After(newest.info.ModTime) {
					newest = e
				}
			}
			head, err := env.FS.Head(newest.path, importer.HeadSize)
			if err != nil {
				return Finding{}, []Gap{GapFromError(hostDetectorName, KindLogSet, perfmonName, err)}, false
			}
			if imp.Detect(head, newest.info.Name) <= 0 {
				conf = 40
			}
		}
	}
	return hostGroupFinding(KindLogSet, importerWinCounters, "discover.perfmon.shows", "", conf, folder, files), nil, true
}

// siteFolders reads the .NET counter and nettrace files in each site folder.
func (d hostDetector) siteFolders(ctx context.Context, env Env) ([]Finding, []Gap) {
	if d.opts.SiteDirs == nil {
		return nil, nil
	}
	var imp importer.Importer
	if env.Importers != nil {
		if got, ok := env.Importers.ByName(importerDotnetCounters); ok {
			imp = got
		}
	}
	var findings []Finding
	var gaps []Gap
	for _, raw := range d.opts.SiteDirs(ctx) {
		if ctx.Err() != nil {
			break
		}
		dir, err := ValidateAbsPath(raw)
		if err != nil {
			gaps = append(gaps, Gap{Detector: hostDetectorName, Kind: KindLogFile, Display: siteName, Reason: ReasonUnsafePath})
			continue
		}
		info, err := statChecked(env.FS, dir)
		switch {
		case IsNotFound(err):
			continue
		case err != nil:
			gaps = append(gaps, GapFromError(hostDetectorName, KindLogFile, siteName, err))
			continue
		case !info.IsDir:
			continue
		}
		tree, gs := walkHostFiles(ctx, env.FS, dir, 0, siteName)
		gaps = append(gaps, gs...)
		for _, e := range tree[dir] {
			lower := strings.ToLower(e.info.Name)
			switch {
			case strings.HasSuffix(lower, ".nettrace"):
				findings = append(findings, hostGroupFinding(KindExportNeeded, "", "discover.nettrace.shows", "discover.nettrace.how", 90, dir, []hostEntry{e}))
			case (strings.HasSuffix(lower, ".csv") || strings.HasSuffix(lower, ".json")) && imp != nil:
				head, err := env.FS.Head(e.path, importer.HeadSize)
				if err != nil {
					gaps = append(gaps, GapFromError(hostDetectorName, KindLogFile, siteName, err))
					continue
				}
				if imp.Detect(head, e.info.Name) > 0 {
					f := hostGroupFinding(KindLogFile, importerDotnetCounters, "discover.dotnetcounters.shows", "", 90, dir, []hostEntry{e})
					f.Display = e.info.Name
					findings = append(findings, f)
				}
			}
		}
	}
	return findings, gaps
}

// hostEntry is one file found by walkHostFiles.
type hostEntry struct {
	path string
	info FileInfo
}

// walkHostFiles lists the files under root, down to maxDepth folders below it
// (root is depth 0). It never follows a reparse point. A folder that cannot be
// listed becomes a gap; a folder over MaxDirEntries keeps its partial listing and
// adds a too-large gap. The result is keyed by folder path.
func walkHostFiles(ctx context.Context, fsys FS, root string, maxDepth int, display string) (map[string][]hostEntry, []Gap) {
	out := map[string][]hostEntry{}
	var gaps []Gap
	var visit func(dir string, depth int)
	visit = func(dir string, depth int) {
		if ctx.Err() != nil {
			return
		}
		infos, err := fsys.ReadDir(dir)
		switch {
		case errors.Is(err, ErrTooManyEntries):
			gaps = append(gaps, Gap{Detector: hostDetectorName, Kind: KindLogSet, Display: display, Reason: ReasonTooLarge})
		case err != nil:
			gaps = append(gaps, GapFromError(hostDetectorName, KindLogSet, display, err))
			return
		}
		for _, fi := range infos {
			if fi.Reparse {
				continue
			}
			p := WinJoin(dir, fi.Name)
			if fi.IsDir {
				if depth < maxDepth {
					visit(p, depth+1)
				}
				continue
			}
			out[dir] = append(out[dir], hostEntry{path: p, info: fi})
		}
	}
	visit(root, 0)
	return out, gaps
}

// hostGroupFinding builds one finding for a group of files in one folder. Only
// findings that are not export-needed carry paths; an export-needed finding names
// its folder and counts, and never a path a client could fetch.
func hostGroupFinding(kind Kind, imp, shows, how string, conf int, folder string, files []hostEntry) Finding {
	f := Finding{
		Detector: hostDetectorName, Kind: kind, Importer: imp, Display: WinBase(folder),
		Confidence: conf, Shows: shows, How: how, Files: len(files),
	}
	var size int64
	paths := make([]string, 0, len(files))
	for _, e := range files {
		size += e.info.Size
		paths = append(paths, e.path)
		if f.Oldest.IsZero() || e.info.ModTime.Before(f.Oldest) {
			f.Oldest = e.info.ModTime
		}
		if e.info.ModTime.After(f.Newest) {
			f.Newest = e.info.ModTime
		}
	}
	f.SizeBytes = size
	f.Cost = CostForSize(size)
	if kind != KindExportNeeded {
		sort.Strings(paths)
		f.SetPaths(paths...)
	}
	return f
}

// sortedFolders returns the folder keys of a walk, sorted.
func sortedFolders(tree map[string][]hostEntry) []string {
	keys := make([]string, 0, len(tree))
	for k := range tree {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// hasSuffixFold reports whether name ends with suffix, ignoring case.
func hasSuffixFold(name, suffix string) bool {
	return strings.HasSuffix(strings.ToLower(name), suffix)
}
