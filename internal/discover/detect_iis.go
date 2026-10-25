package discover

import (
	"bytes"
	"context"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"api-audit-explorer/internal/discover/configparse"
	"api-audit-explorer/internal/importer"
)

// IISRoots are the folders that the environment variables %SystemDrive%,
// %windir%, %ProgramData% and %ProgramFiles% stand for. Env has no access to
// environment variables, so the caller passes them in. Those four are the only
// variables a config value may use (spec 2.2); any other gives a config-unparsed
// gap. %SystemRoot% is read as %windir%, which is what it always equals.
type IISRoots struct {
	SystemDrive  string // "C:"
	WinDir       string // `C:\Windows`
	ProgramData  string // `C:\ProgramData`
	ProgramFiles string // `C:\Program Files`
}

// DefaultIISRoots are the stock locations on drive C.
func DefaultIISRoots() IISRoots {
	return IISRoots{SystemDrive: "C:", WinDir: `C:\Windows`, ProgramData: `C:\ProgramData`, ProgramFiles: `C:\Program Files`}
}

func (r IISRoots) filled() IISRoots {
	d := DefaultIISRoots()
	if r.SystemDrive == "" {
		r.SystemDrive = d.SystemDrive
	}
	if r.WinDir == "" {
		r.WinDir = d.WinDir
	}
	if r.ProgramData == "" {
		r.ProgramData = d.ProgramData
	}
	if r.ProgramFiles == "" {
		r.ProgramFiles = d.ProgramFiles
	}
	return r
}

var envToken = regexp.MustCompile(`(?i)%(systemdrive|windir|systemroot|programdata|programfiles)%`)

// expand replaces the allowed variables. Any other %name% stays, and ValidLogPath
// then refuses the value as config-unparsed.
func (r IISRoots) expand(raw string) string {
	return envToken.ReplaceAllStringFunc(raw, func(tok string) string {
		switch strings.ToLower(tok) {
		case "%systemdrive%":
			return r.SystemDrive
		case "%windir%", "%systemroot%":
			return r.WinDir
		case "%programdata%":
			return r.ProgramData
		default:
			return r.ProgramFiles
		}
	})
}

const (
	iisDetectorName     = "iis"
	httpErrDetectorName = "httperr"

	hostLabelLocal = "local"

	// frebKeep is how many of the newest trace files one finding lists (spec 5).
	frebKeep = 500

	httpErrRegKey   = `HKLM\SYSTEM\CurrentControlSet\Services\HTTP\Parameters`
	httpErrRegValue = "ErrorLoggingDir"
)

var (
	reW3CName    = regexp.MustCompile(`(?i)^u_ex[^\\/]*\.log$`)
	reFrebName   = regexp.MustCompile(`(?i)^fr[0-9]+\.xml$`)
	reHTTPErrLog = regexp.MustCompile(`(?i)^httperr[^\\/]*\.log$`)
	reSiteDir    = regexp.MustCompile(`(?i)^W3SVC([0-9]{1,10})$`)
	reANCMTail   = regexp.MustCompile(`^[0-9]{14}_[0-9]+\.log$`)
)

func matchName(re *regexp.Regexp) func(FileInfo) bool {
	return func(fi FileInfo) bool { return re.MatchString(fi.Name) }
}

// NewIISDetector returns the detector for IIS site logs (iis-w3c), failed
// request traces (iis-freb) and ASP.NET Core Module console logs (ancm-stdout).
// It reads applicationHost.config for site IDs and folders, and each site's
// web.config for the console log setting, keeping only those values (see
// package configparse). With no readable config it falls back to the default
// W3SVC folders. Pass DefaultIISRoots, or the real folders of the host.
func NewIISDetector(roots IISRoots) Detector { return iisDetector{roots: roots.filled()} }

// NewHTTPErrDetector returns the detector for HTTP.sys error logs (iis-httperr).
func NewHTTPErrDetector(roots IISRoots) Detector { return httpErrDetector{roots: roots.filled()} }

type iisDetector struct{ roots IISRoots }

func (iisDetector) Name() string { return iisDetectorName }

type httpErrDetector struct{ roots IISRoots }

func (httpErrDetector) Name() string { return httpErrDetectorName }

// run holds the state of one Detect call.
type iisRun struct {
	ctx      context.Context
	env      Env
	roots    IISRoots
	detector string
	findings []Finding
	gaps     []Gap
	gapSeen  map[Gap]bool
	index    map[string]int // dedupeKey to position in findings
}

func newIISRun(ctx context.Context, env Env, roots IISRoots, detector string) *iisRun {
	return &iisRun{ctx: ctx, env: env, roots: roots, detector: detector, gapSeen: map[Gap]bool{}, index: map[string]int{}}
}

func (r *iisRun) gap(kind Kind, display string, reason Reason) {
	g := Gap{Detector: r.detector, Kind: kind, Display: display, Reason: reason}
	if !r.gapSeen[g] {
		r.gapSeen[g] = true
		r.gaps = append(r.gaps, g)
	}
}

func (r *iisRun) gapErr(kind Kind, display string, err error) {
	r.gap(kind, display, GapFromError(r.detector, kind, display, err).Reason)
}

// add keeps a finding, merging it into an earlier one with the same files so two
// sites that log to one folder give one finding listing both site IDs.
func (r *iisRun) add(f Finding) {
	k := dedupeKey(f)
	if i, ok := r.index[k]; ok {
		r.findings[i] = merge(r.findings[i], f)
		return
	}
	r.index[k] = len(r.findings)
	r.findings = append(r.findings, f)
}

// dirFiles lists the matching files of one folder. The folder is first resolved
// through FS.Final: a reparse point, or a name that resolves to somewhere else,
// is refused with an unsafe-path error.
func (r *iisRun) dirFiles(dir string, depth int, keep func(FileInfo) bool) (WalkResult, error) {
	canon, reparse, err := r.env.FS.Final(dir)
	if err != nil {
		return WalkResult{}, err
	}
	if reparse || !strings.EqualFold(strings.TrimRight(canon, `\`), strings.TrimRight(dir, `\`)) {
		return WalkResult{}, unsafeErr("reparse")
	}
	return Walk(r.ctx, r.env.FS, dir, depth, keep)
}

// setSpec describes the finding to build from a list of files.
type setSpec struct {
	gapName  string // what a gap calls this source: a site ID, never a path
	importer string
	shows    string
	sites    []string
	declared bool // the folder came from a config file, not a default location
}

// emit turns files into one finding. Size, count, dates and cost come from the
// directory listing alone; the only read is the 64 KiB head of the newest file,
// which confirms the importer. It reports false when nothing was added.
func (r *iisRun) emit(spec setSpec, files []Walked) bool {
	if len(files) == 0 {
		return false
	}
	SortOldestFirst(files)
	var size int64
	oldest, newest := files[0].Info.ModTime, files[0].Info.ModTime
	paths := make([]string, len(files))
	for i, f := range files {
		size += f.Info.Size
		paths[i] = f.Path
		if f.Info.ModTime.Before(oldest) {
			oldest = f.Info.ModTime
		}
		if f.Info.ModTime.After(newest) {
			newest = f.Info.ModTime
		}
	}
	agree, err := r.sniff(spec.importer, files)
	if err != nil && IsAccessDenied(err) {
		r.gapErr(KindLogSet, spec.gapName, err)
		return false
	}
	conf := 40
	switch {
	case agree && spec.declared:
		conf = 90
	case agree:
		conf = 70
	}
	last := files[len(files)-1].Info.Name
	detail := last
	kind := KindLogFile
	if len(files) > 1 {
		kind = KindLogSet
		detail = last + " and " + strconv.Itoa(len(files)-1) + " more"
	}
	f := Finding{
		Detector:   r.detector,
		Kind:       kind,
		Importer:   spec.importer,
		HostLabel:  hostLabelLocal,
		Confidence: conf,
		Shows:      spec.shows,
		SizeBytes:  size,
		Files:      len(files),
		Oldest:     oldest,
		Newest:     newest,
		Cost:       CostForSize(size),
	}
	f.SetPaths(paths...)
	f.SetSites(spec.sites, detail)
	r.add(f)
	return true
}

// sniff reads the head of the newest non-empty file and asks the importer
// registry which importer it looks like. It reports whether that is imp. A read
// error is returned; the caller treats a denied one as a gap and any other as
// "name only".
func (r *iisRun) sniff(imp string, files []Walked) (bool, error) {
	if r.env.Importers == nil {
		return false, nil
	}
	pick := files[len(files)-1]
	for i := len(files) - 1; i >= 0; i-- {
		if files[i].Info.Size > 0 {
			pick = files[i]
			break
		}
	}
	head, err := r.env.FS.Head(pick.Path, importer.HeadSize)
	if err != nil {
		return false, err
	}
	best, _, _ := r.env.Importers.Detect(head, pick.Info.Name)
	return best != nil && best.Name() == imp, nil
}

// Detect finds IIS site logs, failed request traces and ANCM console logs.
func (d iisDetector) Detect(ctx context.Context, env Env) ([]Finding, []Gap) {
	r := newIISRun(ctx, env, d.roots, iisDetectorName)
	if env.FS == nil {
		return nil, []Gap{{Detector: iisDetectorName, Kind: KindLogSet, Display: "IIS", Reason: ReasonUnreadable}}
	}
	cfgPath := WinJoin(r.roots.WinDir, `System32\inetsrv\config\applicationHost.config`)
	ah := r.readAppHost(cfgPath)
	if ah != nil && len(ah.Sites) > 0 {
		cfgDir := WinDir(cfgPath)
		for _, s := range ah.Sites {
			if ctx.Err() != nil {
				break
			}
			r.site(ah, s, cfgDir)
		}
	} else {
		r.defaultSites()
	}
	return r.findings, r.gaps
}

// readAppHost reads and parses applicationHost.config. It returns nil when the
// file is missing or unusable; a missing file is not a gap on its own, because
// the default folders are checked next and report if they are missing too.
func (r *iisRun) readAppHost(path string) *configparse.AppHost {
	const name = "IIS settings"
	fi, err := r.env.FS.Stat(path)
	if err != nil {
		if !IsNotFound(err) {
			r.gapErr(KindLogSet, name, err)
		}
		return nil
	}
	if fi.IsDir || fi.Reparse {
		r.gap(KindLogSet, name, ReasonUnsafePath)
		return nil
	}
	if fi.Size > MaxConfigBytes {
		r.gap(KindLogSet, name, ReasonTooLarge)
		return nil
	}
	b, err := r.env.FS.Head(path, MaxConfigBytes)
	if err != nil {
		r.gapErr(KindLogSet, name, err)
		return nil
	}
	ah, err := configparse.ParseAppHost(bytes.NewReader(b))
	if err != nil {
		r.gap(KindLogSet, name, ReasonConfigUnparsed)
		return nil
	}
	return ah
}

// site reports everything one configured site writes.
func (r *iisRun) site(ah *configparse.AppHost, s configparse.Site, cfgDir string) {
	id := "W3SVC" + s.ID
	r.siteW3C(ah, s, id, cfgDir)
	r.siteFreb(ah, s, id, cfgDir)
	r.siteANCM(s, id)
}

func (r *iisRun) siteW3C(ah *configparse.AppHost, s configparse.Site, id, cfgDir string) {
	format := s.LogFormat
	if format == "" {
		format = ah.DefaultLogFormat
	}
	if format != "" && !strings.EqualFold(format, "W3C") {
		r.gap(KindLogSet, id, ReasonConfigUnparsed)
		return
	}
	raw := s.LogDir
	if raw == "" {
		raw = ah.DefaultLogDir
	}
	declared := raw != ""
	if !declared {
		raw = `%SystemDrive%\inetpub\logs\LogFiles`
	}
	base, err := ValidLogPath(r.roots.expand(raw), cfgDir)
	if err != nil {
		r.gapErr(KindLogSet, id, err)
		return
	}
	spec := setSpec{gapName: id, importer: "iis-w3c", shows: wordingIISShows, sites: []string{id}, declared: declared}
	// IIS writes to <directory>\W3SVC<id>. A folder that holds the log files
	// directly is also accepted, which is how two sites can share one folder.
	for _, dir := range []string{WinJoin(base, id), base} {
		res, err := r.dirFiles(dir, 0, matchName(reW3CName))
		if err != nil {
			if IsNotFound(err) {
				continue
			}
			r.gapErr(KindLogSet, id, err)
			return
		}
		if r.emit(spec, res.Files) {
			return
		}
	}
	r.gap(KindLogSet, id, ReasonNotFound)
}

func (r *iisRun) siteFreb(ah *configparse.AppHost, s configparse.Site, id, cfgDir string) {
	raw := s.FrebDir
	if raw == "" {
		raw = ah.DefaultFrebDir
	}
	declared := raw != ""
	if !declared {
		raw = `%SystemDrive%\inetpub\logs\FailedReqLogFiles`
	}
	name := id + " failed request traces"
	base, err := ValidLogPath(r.roots.expand(raw), cfgDir)
	if err != nil {
		r.gapErr(KindLogSet, name, err)
		return
	}
	res, err := r.dirFiles(WinJoin(base, id), 0, matchName(reFrebName))
	if err != nil {
		// Tracing is off on most hosts, so a missing folder is not worth a row.
		if !IsNotFound(err) {
			r.gapErr(KindLogSet, name, err)
		}
		return
	}
	r.emit(frebSpec(name, id, declared), newestFiles(res.Files, frebKeep))
}

func frebSpec(name, id string, declared bool) setSpec {
	return setSpec{gapName: name, importer: "iis-freb", shows: wordingFrebShows, sites: []string{id}, declared: declared}
}

// newestFiles keeps the n files with the latest times.
func newestFiles(files []Walked, n int) []Walked {
	if len(files) <= n {
		return files
	}
	SortOldestFirst(files)
	return files[len(files)-n:]
}

func (r *iisRun) siteANCM(s configparse.Site, id string) {
	name := id + " console log"
	for _, app := range s.AppPaths {
		if r.ctx.Err() != nil {
			return
		}
		// The physical path is used only to find web.config. One that cannot be
		// used (a share, a variable we do not expand) is skipped quietly.
		phys, err := ValidLogPath(r.roots.expand(app), "")
		if err != nil {
			continue
		}
		cfg := WinJoin(phys, "web.config")
		fi, err := r.env.FS.Stat(cfg)
		if err != nil {
			if IsAccessDenied(err) {
				r.gapErr(KindLogSet, name, err)
			}
			continue
		}
		if fi.IsDir || fi.Reparse || fi.Size > MaxConfigBytes {
			continue
		}
		b, err := r.env.FS.Head(cfg, MaxConfigBytes)
		if err != nil {
			if IsAccessDenied(err) {
				r.gapErr(KindLogSet, name, err)
			}
			continue
		}
		outs, err := configparse.ParseWebConfig(bytes.NewReader(b))
		if err != nil {
			r.gap(KindLogSet, name, ReasonConfigUnparsed)
			continue
		}
		for _, o := range outs {
			r.ancmSet(id, name, phys, o)
		}
	}
}

func (r *iisRun) ancmSet(id, name, physDir string, o configparse.Stdout) {
	prefix, err := ValidLogPath(r.roots.expand(o.File), physDir)
	if err != nil {
		r.gapErr(KindLogSet, name, err)
		return
	}
	dir, stem := WinDir(prefix), strings.ToLower(WinBase(prefix))+"_"
	keep := func(fi FileInfo) bool {
		n := strings.ToLower(fi.Name)
		return strings.HasPrefix(n, stem) && reANCMTail.MatchString(n[len(stem):])
	}
	res, err := r.dirFiles(dir, 0, keep)
	if err != nil {
		if IsNotFound(err) {
			if o.Enabled {
				r.gap(KindLogSet, name, ReasonNotFound)
			}
			return
		}
		r.gapErr(KindLogSet, name, err)
		return
	}
	spec := setSpec{gapName: name, importer: "ancm-stdout", shows: wordingANCMShows, sites: []string{id}, declared: true}
	if !r.emit(spec, res.Files) && o.Enabled {
		r.gap(KindLogSet, name, ReasonNotFound)
	}
}

// defaultSites is the fallback when applicationHost.config gave no sites: look
// for W3SVC<id> folders under the default log and trace folders.
func (r *iisRun) defaultSites() {
	const name = "IIS logs"
	base := WinJoin(r.roots.SystemDrive, `inetpub\logs\LogFiles`)
	res, err := r.dirFiles(base, 1, matchName(reW3CName))
	if err != nil {
		r.gapErr(KindLogSet, name, err)
		return
	}
	r.reparseGaps(res.Reparse)
	groups := groupBySite(res.Files)
	if len(groups) == 0 {
		if len(res.Reparse) == 0 {
			r.gap(KindLogSet, name, ReasonNotFound)
		}
	}
	for _, id := range sortedSiteIDs(groups) {
		r.emit(setSpec{gapName: id, importer: "iis-w3c", shows: wordingIISShows, sites: []string{id}}, groups[id])
	}

	tbase := WinJoin(r.roots.SystemDrive, `inetpub\logs\FailedReqLogFiles`)
	const tname = "IIS failed request traces"
	tres, err := r.dirFiles(tbase, 1, matchName(reFrebName))
	if err != nil {
		// Tracing is off on most hosts, so only a refusal is worth a row.
		if !IsNotFound(err) {
			r.gapErr(KindLogSet, tname, err)
		}
		return
	}
	r.reparseGaps(tres.Reparse)
	tgroups := groupBySite(tres.Files)
	for _, id := range sortedSiteIDs(tgroups) {
		r.emit(frebSpec(id+" failed request traces", id, false), newestFiles(tgroups[id], frebKeep))
	}
}

// reparseGaps adds an unsafe-path gap for each skipped W3SVC<id> folder.
func (r *iisRun) reparseGaps(paths []string) {
	for _, p := range paths {
		if m := reSiteDir.FindStringSubmatch(WinBase(p)); m != nil {
			r.gap(KindLogSet, "W3SVC"+m[1], ReasonUnsafePath)
		}
	}
}

// groupBySite groups files by the W3SVC<id> folder they sit in. Files in other
// folders are dropped.
func groupBySite(files []Walked) map[string][]Walked {
	out := map[string][]Walked{}
	for _, f := range files {
		if m := reSiteDir.FindStringSubmatch(WinBase(f.Dir)); m != nil {
			id := "W3SVC" + m[1]
			out[id] = append(out[id], f)
		}
	}
	return out
}

func sortedSiteIDs(m map[string][]Walked) []string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return naturalLess(ids[i], ids[j]) })
	return ids
}

// Detect finds the HTTP.sys error log set.
func (d httpErrDetector) Detect(ctx context.Context, env Env) ([]Finding, []Gap) {
	const name = "HTTP error log"
	r := newIISRun(ctx, env, d.roots, httpErrDetectorName)
	if env.FS == nil {
		return nil, []Gap{{Detector: httpErrDetectorName, Kind: KindLogSet, Display: name, Reason: ReasonUnreadable}}
	}
	var cands []string
	declared := false
	if env.Registry != nil {
		// A denied or missing value means "not declared"; the default folder is
		// then probed, and reports its own denial.
		if raw, err := env.Registry.String(httpErrRegKey, httpErrRegValue); err == nil && strings.TrimSpace(raw) != "" {
			p, perr := ValidLogPath(r.roots.expand(raw), "")
			if perr != nil {
				r.gapErr(KindLogSet, name, perr)
			} else {
				declared = true
				if strings.EqualFold(WinBase(p), "HTTPERR") {
					cands = []string{p}
				} else {
					// HTTP.sys adds the HTTPERR folder under the configured one.
					cands = []string{WinJoin(p, "HTTPERR"), p}
				}
			}
		}
	}
	if !declared {
		cands = []string{WinJoin(r.roots.WinDir, `System32\LogFiles\HTTPERR`)}
	}
	spec := setSpec{gapName: name, importer: "iis-httperr", shows: wordingHTTPErrShows, declared: declared}
	for _, dir := range cands {
		if ctx.Err() != nil {
			break
		}
		res, err := r.dirFiles(dir, 0, matchName(reHTTPErrLog))
		if err != nil {
			if IsNotFound(err) {
				continue
			}
			r.gapErr(KindLogSet, name, err)
			return r.findings, r.gaps
		}
		if r.emit(spec, res.Files) {
			return r.findings, r.gaps
		}
	}
	if len(cands) > 0 {
		r.gap(KindLogSet, name, ReasonNotFound)
	}
	return r.findings, r.gaps
}
