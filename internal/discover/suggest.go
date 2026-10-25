package discover

import (
	"regexp"
	"strings"
)

// Suggestion is a pair of names the scan offers for the Portal and API tags of a
// source, taken from the folders its files sit in. They are hints only: the
// server never applies one, and the client shows them as buttons the user may
// press, never as text it fills in (spec R7). Both fields hold plain words and
// nothing else: no path, no drive letter, no user-profile or machine name.
type Suggestion struct {
	Portal string `json:"portal,omitempty"`
	API    string `json:"api,omitempty"`
}

// maxSuggestWord is the longest a suggested name may be.
const maxSuggestWord = 40

// noiseSegments are folder names that say what a folder is for, not which
// portal or API it belongs to. They are compared whole, lower case.
var noiseSegments = map[string]bool{
	"logs": true, "log": true, "logfiles": true, "bin": true, "app_data": true, "inetpub": true,
	"wwwroot": true, "program files": true, "program files (x86)": true, "programdata": true,
	"users": true, "windows": true, "temp": true, "tmp": true, "system32": true, "inetsrv": true,
	"appdata": true, "local": true, "locallow": true, "roaming": true,
	"documents": true, "desktop": true, "downloads": true, "public": true,
	"debug": true, "release": true,
	"apps": true, "app": true, "applications": true, "sites": true, "websites": true, "www": true,
	"data": true, "services": true,
}

var (
	// A .NET target folder such as net8.0 or netcoreapp3.1.
	tfmSegment = regexp.MustCompile(`^net(coreapp|standard)?[0-9]+(\.[0-9]+)?$`)
	// A site number, a bare number or a GUID names nothing a person would say.
	idSegment = regexp.MustCompile(`^(w3svc[0-9]+|[0-9]+|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$`)
	// Everything outside this set is stripped from a word.
	unsafeWordChars = regexp.MustCompile(`[^A-Za-z0-9 ._-]`)
)

// secretish are words a folder name must not carry into a suggestion. A folder
// named after a credential is skipped, the same care ValidLogPath takes with
// config values.
var secretish = []string{"password", "passwd", "secret", "token", "accountkey", "connectionstring"}

// suggestNames derives a Portal and API suggestion from the folders of a
// finding's files. Each path is a file; the folder it sits in is the log folder.
// For several files the folders' common ancestor is used. The nearest meaningful
// folder name above the log folder is the API and the next one up the Portal; a
// single meaningful name is offered as the API. It returns nil when nothing
// meaningful remains. It makes no system call.
func suggestNames(paths []string) *Suggestion {
	segs := commonFolderSegments(paths)
	var words []string // nearest first
	for i := len(segs) - 1; i >= 0 && len(words) < 2; i-- {
		w, ok := meaningfulSegment(segs[i])
		if !ok {
			continue
		}
		words = append(words, w)
	}
	switch len(words) {
	case 0:
		return nil
	case 1:
		return &Suggestion{API: words[0]}
	default:
		return &Suggestion{Portal: words[1], API: words[0]}
	}
}

// commonFolderSegments returns the folder segments shared by every path's
// parent folder, outermost first, with the segments that can never be a name
// removed: the drive, a UNC server and share, and a user profile folder.
func commonFolderSegments(paths []string) []string {
	var common []string
	for n, p := range paths {
		segs := folderSegments(p)
		if n == 0 {
			common = segs
			continue
		}
		k := 0
		for k < len(common) && k < len(segs) && strings.EqualFold(common[k], segs[k]) {
			k++
		}
		common = common[:k]
	}
	return common
}

// folderSegments splits the parent folder of a file path into segments and
// removes the ones that identify a machine or a person.
func folderSegments(p string) []string {
	p = strings.ReplaceAll(p, "/", `\`)
	unc := strings.HasPrefix(p, `\\`)
	parts := strings.Split(p, `\`)
	if len(parts) > 0 {
		parts = parts[:len(parts)-1] // the file name
	}
	out := make([]string, 0, len(parts))
	for _, s := range parts {
		if s != "" {
			out = append(out, s)
		}
	}
	if unc && len(out) >= 2 {
		out = out[2:] // \\server\share
	} else if unc {
		return nil
	}
	if len(out) > 0 && len(out[0]) == 2 && out[0][1] == ':' {
		out = out[1:] // drive
	}
	// The folder after "Users" is a profile name, whoever owns it.
	kept := out[:0:0]
	for i := 0; i < len(out); i++ {
		if strings.EqualFold(out[i], "users") {
			i++ // skip the profile folder too
			continue
		}
		kept = append(kept, out[i])
	}
	return kept
}

// meaningfulSegment cleans one folder name into a suggested word. It reports
// false when the folder says nothing a person would call a portal or an API.
func meaningfulSegment(seg string) (string, bool) {
	w := unsafeWordChars.ReplaceAllString(seg, "")
	w = strings.Trim(w, " ._-")
	if len(w) > maxSuggestWord {
		w = strings.Trim(w[:maxSuggestWord], " ._-")
	}
	if w == "" {
		return "", false
	}
	l := strings.ToLower(w)
	if noiseSegments[l] || tfmSegment.MatchString(l) || idSegment.MatchString(l) {
		return "", false
	}
	for _, s := range secretish {
		if strings.Contains(l, s) {
			return "", false
		}
	}
	return w, true
}
