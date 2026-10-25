package discover

import (
	"context"
	"strings"

	"api-audit-explorer/internal/importer"
)

// The verify pass proves that a finding the scan offers is one Unravis can read.
// A detector says "this folder looks like IIS logs"; the pass opens a few of the
// files, hands the leading bytes to the importer the finding names and keeps the
// finding ready only when that importer recognises them. A finding that fails is
// not dropped: it stays in the list with its importer cleared and a plain reason
// in Unreadable, so the client can show it as "found, but not readable" and never
// as a row the user can tick.

// Reason keys for a finding that did not pass. They are fixed sentences in
// wording.go and join the same table as the "shows" sentences.
const (
	unreadableOpen    = "discover.cannotopen.shows"
	unreadableEmpty   = "discover.emptyfile.shows"
	unreadableFormat  = "discover.wrongformat.shows"
	unreadableTimeout = "discover.verifytimeout.shows"
)

const (
	// verifyMaxPaths is how many of a finding's files are opened.
	verifyMaxPaths = 3
	// verifyWorkers bounds how many findings are checked at once.
	verifyWorkers = 4
)

// verifiable reports whether a finding goes through the pass. Live series,
// export hints and remote-only rows are never selectable files, so they are left
// exactly as the detector made them.
func verifiable(f Finding) bool {
	if f.Importer == "" {
		return false
	}
	switch f.Kind {
	case KindLogFile, KindLogSet, KindEventChannel:
		return true
	}
	return false
}

// samplePaths picks up to verifyMaxPaths paths spread over the list (first,
// middle, last), so a rotated set whose current file is still empty is judged by
// the others too.
func samplePaths(paths []string) []string {
	n := len(paths)
	if n <= verifyMaxPaths {
		return paths
	}
	return []string{paths[0], paths[n/2], paths[n-1]}
}

// verifyFinding reports whether the importer a finding names can read it. When it
// cannot, reasonKey is the wording key that says why. It reads at most one head
// (importer.HeadSize bytes, the same sample the detectors and the import look at)
// from each of up to three files, through env.FS, and never keeps the bytes. An
// event channel is checked by asking the system whether the channel opens, not by
// reading content.
func verifyFinding(f Finding, env Env) (ok bool, reasonKey string) {
	if env.Importers == nil {
		return false, unreadableOpen
	}
	imp, found := env.Importers.ByName(f.Importer)
	if !found {
		return false, unreadableFormat
	}
	if f.Kind == KindEventChannel {
		return verifyChannel(f, env)
	}
	if env.FS == nil || len(f.paths) == 0 {
		return false, unreadableOpen
	}

	var opened, nonEmpty bool
	for _, p := range samplePaths(f.paths) {
		head, err := env.FS.Head(p, importer.HeadSize)
		if err != nil {
			continue
		}
		opened = true
		if len(head) == 0 {
			continue
		}
		nonEmpty = true
		name := WinBase(p)
		if importer.SniffNamed(head, name) != nil {
			continue
		}
		if imp.Detect(head, name) > 0 {
			return true, ""
		}
	}
	switch {
	case !opened:
		return false, unreadableOpen
	case !nonEmpty:
		return false, unreadableEmpty
	default:
		return false, unreadableFormat
	}
}

// verifyChannel confirms an event channel exists and the account may read it.
func verifyChannel(f Finding, env Env) (bool, string) {
	if env.Channels == nil {
		return false, unreadableOpen
	}
	names, err := env.Channels.List()
	if err != nil {
		return false, unreadableOpen
	}
	for _, n := range names {
		if strings.EqualFold(n, f.Display) {
			if env.Channels.CanRead(n) != nil {
				return false, unreadableOpen
			}
			return true, ""
		}
	}
	return false, unreadableOpen
}

// safeVerify runs verifyFinding and turns a panic (an importer's Detect is
// third-party-shaped code) into "could not open".
func safeVerify(f Finding, env Env) (ok bool, reasonKey string) {
	defer func() {
		if recover() != nil {
			ok, reasonKey = false, unreadableOpen
		}
	}()
	return verifyFinding(f, env)
}

// demote marks a finding as found but not readable: no importer, so no client
// can select it, and the reason in plain words.
func demote(f *Finding, reasonKey string) {
	f.Verified = false
	f.Importer = ""
	f.Unreadable = reasonKey
}

// VerifyPaths runs the same check the scan applies to a finding, for a set of
// files the user pointed at: it reports whether the named importer recognises a
// sample of them, and the wording key that says why not. It is a thin wrapper
// over the scan's own check and changes nothing about it.
func VerifyPaths(env Env, importerName string, paths []string) (ok bool, reasonKey string) {
	f := Finding{Kind: KindLogSet, Importer: importerName}
	f.SetPaths(paths...)
	return safeVerify(f, env)
}

// LogFileOK reports whether a file name is one the scan would offer as a log: a
// log extension (with an optional rolling suffix) and not the name of a key,
// certificate or credential file. A folder the user names is filtered with it.
func LogFileOK(name string) bool { return logExtOK(name) && !sensitiveLogName(name) }

// SuggestNames derives the Portal and API hints from the folders of paths, as the
// scan does for every finding. Nil when nothing usable is left.
func SuggestNames(paths []string) *Suggestion { return suggestNames(paths) }

type verifyJob struct {
	i int
	f Finding // a copy: the workers never touch the caller's slice
}

type verifyResult struct {
	i      int
	ok     bool
	reason string
}

// verifyAll checks every verifiable finding in place. It runs at most
// verifyWorkers checks at once and stops when ctx ends: a finding that has no
// answer by then is demoted with the time-out reason, never left ready. A check
// stuck inside the file system is abandoned and its late answer dropped. It
// returns true when ctx cut the pass short.
func verifyAll(ctx context.Context, env Env, fs []Finding) (cutOff bool) {
	var todo []int
	for i := range fs {
		if verifiable(fs[i]) {
			todo = append(todo, i)
		}
	}
	if len(todo) == 0 {
		return false
	}
	// Copy up front: the caller's slice is written to while workers still run.
	list := make([]verifyJob, len(todo))
	for k, i := range todo {
		list[k] = verifyJob{i: i, f: fs[i]}
	}
	jobs := make(chan verifyJob)
	out := make(chan verifyResult, len(todo)) // room for every answer, so a late worker never blocks
	workers := min(verifyWorkers, len(todo))
	for w := 0; w < workers; w++ {
		go func() {
			for j := range jobs {
				if ctx.Err() != nil {
					out <- verifyResult{i: j.i, reason: unreadableTimeout}
					continue
				}
				ok, reason := safeVerify(j.f, env)
				out <- verifyResult{i: j.i, ok: ok, reason: reason}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, j := range list {
			select {
			case jobs <- j:
			case <-ctx.Done():
				return
			}
		}
	}()

	answered := make(map[int]bool, len(todo))
	apply := func(r verifyResult) {
		answered[r.i] = true
		if r.ok {
			fs[r.i].Verified = true
			return
		}
		demote(&fs[r.i], r.reason)
	}
collect:
	for len(answered) < len(todo) {
		select {
		case r := <-out:
			apply(r)
		case <-ctx.Done():
			// Take what is already there, then give up on the rest.
			for {
				select {
				case r := <-out:
					apply(r)
				default:
					break collect
				}
			}
		}
	}
	for _, i := range todo {
		if !answered[i] {
			demote(&fs[i], unreadableTimeout)
		}
		if fs[i].Unreadable == unreadableTimeout {
			cutOff = true
		}
	}
	return cutOff
}
