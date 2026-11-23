// Package clioffline holds the commands of the command line that work without the app: no pipe, no profile, no network (spec
// 05-cli, BL-391). Nothing here writes. It lives apart from internal/clicore so that the framework-free core imports no recorder code;
// where the offline reader sits in the thin exe is decided by the framework spike (BL-381).
package clioffline

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"api-audit-explorer/internal/blackbox"
	"api-audit-explorer/internal/clicore"
)

// Limits of one inspection.
const (
	DefaultMaxBytes = 256 << 20 // the files of one folder, added up
	MaxSamples      = 1000      // --samples
	maxEntries      = 100000    // directory entries looked at
)

// Options of Inspect.
type Options struct {
	// MaxBytes caps the size of the recording files read; 0 means DefaultMaxBytes.
	MaxBytes int64
	// Samples is how many of the first parsed samples to return as well (0 to MaxSamples).
	Samples int
}

// Manifest is what an inspection says about manifest.json (counts and times, no process or file names).
type Manifest struct {
	Version     int        `json:"version"`
	Host        string     `json:"host"`
	State       string     `json:"state"`
	FirstSample *time.Time `json:"firstSample"`
	LastSample  *time.Time `json:"lastSample"`
	Files       int        `json:"files"`
	Gaps        int        `json:"gaps"`
	Events      int        `json:"events"`
	Samples     int        `json:"samples"` // what the manifest says the files hold
}

// SampleInfo is what the reader found in the files.
type SampleInfo struct {
	Count int               `json:"count"`
	From  *time.Time        `json:"from"`
	To    *time.Time        `json:"to"`
	First []blackbox.Sample `json:"first,omitempty"` // only with Options.Samples
}

// Result is the data of `blackbox.inspect`.
type Result struct {
	Input    string     `json:"input"` // as given
	Kind     string     `json:"kind"`  // folder or file
	Manifest *Manifest  `json:"manifest"`
	Samples  SampleInfo `json:"samples"`
	Warnings []string   `json:"warnings"`
}

// liveCaseName says whether a directory entry marks a case store (a live case, a sealed case file or a case key). The offline reader
// never opens a folder that holds one.
func liveCaseName(name string) bool {
	l := strings.ToLower(name)
	switch l {
	case "case.db", "case.key", "cases":
		return true
	}
	for _, ext := range []string{".sqlite", ".sqlite3", ".db", ".unravis-case"} {
		if strings.HasSuffix(l, ext) {
			return true
		}
	}
	return strings.HasPrefix(l, "case.key.")
}

// Inspect reads a recordings folder, or one hourly file (bb-YYYYMMDD-HH.ndjson), and says what is in it. It opens files read-only
// through the same reader the app uses, which refuses links and any name the recorder does not write. It refuses a folder that holds
// case data (LIVE_CASE_REFUSED) and input over the size cap. The path is never echoed in an error.
func Inspect(ctx context.Context, input string, opt Options) (*Result, *clicore.Error) {
	if opt.MaxBytes <= 0 {
		opt.MaxBytes = DefaultMaxBytes
	}
	if opt.Samples < 0 || opt.Samples > MaxSamples {
		return nil, clicore.NewError(clicore.CodeInvalidArgument, "--samples must be from 0 to 1000.", "")
	}
	if input == "" || strings.ContainsRune(input, 0) {
		return nil, clicore.NewError(clicore.CodeInvalidArgument, "Give the folder of a Blackbox recording, or one of its hourly files.", "")
	}
	// The input itself must not be a link (Lstat does not follow it); the reader checks every name inside.
	st, err := os.Lstat(input)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, clicore.NewError(clicore.CodeNotFound, "That path does not exist.", "")
		}
		return nil, clicore.NewError(clicore.CodeInvalidArgument, "That path cannot be read.", "")
	}
	if st.Mode()&os.ModeSymlink != 0 || !(st.IsDir() || st.Mode().IsRegular()) {
		return nil, clicore.NewError(clicore.CodeInvalidArgument, "That path is a link or not a plain folder or file.", "")
	}
	res := &Result{Input: input, Warnings: []string{}}
	if st.IsDir() {
		return inspectFolder(ctx, input, opt, res)
	}
	return inspectFile(ctx, input, st, opt, res)
}

func inspectFolder(ctx context.Context, dir string, opt Options, res *Result) (*Result, *clicore.Error) {
	res.Kind = "folder"
	f, err := os.Open(dir)
	if err != nil {
		return nil, clicore.NewError(clicore.CodeInvalidArgument, "That folder cannot be read.", "")
	}
	entries, err := f.ReadDir(maxEntries + 1)
	_ = f.Close()
	if err != nil && !errors.Is(err, io.EOF) { // io.EOF: an empty folder
		return nil, clicore.NewError(clicore.CodeInvalidArgument, "That folder cannot be read.", "")
	}
	if len(entries) > maxEntries {
		return nil, clicore.NewError(clicore.CodeInvalidArgument, "That folder holds too many entries to be a recording.", "")
	}
	var total int64
	for _, e := range entries {
		if liveCaseName(e.Name()) {
			return nil, clicore.NewError(clicore.CodeLiveCaseRefused, "That folder holds case data. The offline reader reads Blackbox recordings only.", "Copy the recording to a folder of its own and read that.")
		}
		if blackbox.ValidFileName(e.Name()) {
			if info, err := e.Info(); err == nil && info.Mode().IsRegular() {
				total += info.Size()
			}
		}
	}
	if total > opt.MaxBytes {
		return nil, clicore.NewError(clicore.CodeRequestTooLarge, "The recording files are larger than the limit.", "Copy fewer hourly files to a folder, or raise --max-bytes.")
	}
	src := blackbox.NewFolderSource(blackbox.Location{Kind: blackbox.LocFolder, Path: dir})
	mr, rerr := src.Manifest(ctx)
	if rerr != nil {
		return nil, readError(rerr)
	}
	m := mr.Manifest
	res.Manifest = &Manifest{Version: m.Format, Host: m.Host, State: m.State, FirstSample: m.FirstSample, LastSample: m.LastSample,
		Files: len(m.Files), Gaps: len(m.Gaps), Events: len(m.Events), Samples: m.SampleCount()}
	var from, to time.Time
	for i, fe := range m.Files {
		if i == 0 || fe.Hour.Before(from) {
			from = fe.Hour
		}
		if end := fe.Hour.Add(time.Hour); end.After(to) {
			to = end
		}
	}
	return readInto(ctx, src, m, from, to, opt, res)
}

func inspectFile(ctx context.Context, path string, st os.FileInfo, opt Options, res *Result) (*Result, *clicore.Error) {
	res.Kind = "file"
	name := filepath.Base(path)
	hour, ok := blackbox.HourOfName(name)
	if !ok {
		return nil, clicore.NewError(clicore.CodeInvalidArgument, "That file is not a Blackbox hourly file (bb-YYYYMMDD-HH.ndjson).", "")
	}
	if st.Size() > opt.MaxBytes {
		return nil, clicore.NewError(clicore.CodeRequestTooLarge, "The recording file is larger than the limit.", "Raise --max-bytes.")
	}
	src := blackbox.NewFolderSource(blackbox.Location{Kind: blackbox.LocFolder, Path: filepath.Dir(path)})
	m := blackbox.Manifest{Files: []blackbox.FileEntry{{Name: name, Hour: hour}}}
	return readInto(ctx, src, m, hour, hour.Add(time.Hour), opt, res)
}

func readInto(ctx context.Context, src blackbox.Source, m blackbox.Manifest, from, to time.Time, opt Options, res *Result) (*Result, *clicore.Error) {
	samples, rep, err := blackbox.ReadSamples(ctx, src, m, from, to)
	if err != nil && len(samples) == 0 {
		return nil, readError(err)
	}
	res.Samples.Count = len(samples)
	if len(samples) > 0 {
		first, last := samples[0].T, samples[len(samples)-1].T
		res.Samples.From, res.Samples.To = &first, &last
	}
	if opt.Samples > 0 {
		n := opt.Samples
		if n > len(samples) {
			n = len(samples)
		}
		res.Samples.First = samples[:n]
	}
	res.Warnings = append(res.Warnings, warnings(rep, err)...)
	return res, nil
}

// warnings turns what the reader reports into fixed sentences with counts. File names are not listed.
func warnings(rep blackbox.ReadReport, err error) []string {
	var w []string
	add := func(cond bool, s string) {
		if cond {
			w = append(w, s)
		}
	}
	add(err != nil, "The read stopped early; the result is partial.")
	add(len(rep.Torn) > 0, "The last line of a file was cut (a recorder writing, or killed).")
	add(len(rep.Dropped) > 0, "Some files could not be read to the end.")
	add(len(rep.Missing) > 0, "Some files named in the manifest are not there.")
	add(len(rep.Corrupt) > 0, "Some files hold a damaged line in the middle.")
	add(rep.Newer, "Some samples were written by a newer version and were skipped.")
	add(rep.Capped, "Only the newest files were read (file cap).")
	add(rep.Limited, "A size or sample limit stopped the read; the result is partial.")
	sort.Strings(w)
	return w
}

// readError maps a reader error to a command line error with a fixed sentence (never the path).
func readError(err error) *clicore.Error {
	switch blackbox.KindOf(err) {
	case blackbox.ReadNotFound:
		return clicore.NewError(clicore.CodeNotFound, "No Blackbox recording is in that folder (manifest.json is missing).", "")
	case blackbox.ReadBadData:
		return clicore.NewError(clicore.CodeInvalidArgument, "That is not a readable Blackbox recording.", "")
	case blackbox.ReadNewerFormat:
		return clicore.NewError(clicore.CodeVersionIncompatible, "The recording was written by a newer Unravis.", "Update Unravis.")
	}
	return clicore.NewError(clicore.CodeInvalidArgument, "The recording could not be read.", "")
}
