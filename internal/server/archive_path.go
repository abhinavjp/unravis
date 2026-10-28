package server

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

// Archive path rules (spec 7.2, step 2, and Appendix C). The person types a full
// local folder path; Unravis makes the file name. Every refusal is one error,
// errArchiveFolder, that the route answers with M8: the reason is never put in
// the reply, because the path is the person's own text and the answer to "why"
// is the same for every rule (choose a folder on this computer you can write to).

// maxArchivePath is the longest folder-plus-file-name path accepted, in UTF-16
// units, checked after the long-path prefix is stripped and short names are
// resolved (R4-8).
const maxArchivePath = 240

// archiveExt is the case file extension (casestore.CaseFileExt, repeated here so
// the name builder reads on its own).
const archiveExt = ".unravis-case"

// errArchiveFolder is the one refusal of a folder that cannot be used (M8).
var errArchiveFolder = errors.New("archive folder cannot be used")

// errDriveUnknown is a drive type Windows could not give.
var errDriveUnknown = errors.New("drive type unknown")

// archiveFolderRule is an errArchiveFolder carrying the rule that refused it, for
// tests and logs. Its text names the rule and never the path.
type archiveFolderRule struct{ rule string }

func (e *archiveFolderRule) Error() string   { return "archive folder refused: " + e.rule }
func (e *archiveFolderRule) Is(t error) bool { return t == errArchiveFolder }

func refuseFolder(rule string) error { return &archiveFolderRule{rule: rule} }

// archiveEnv is what the path rules ask of the operating system. The server uses
// realArchiveEnv; tests stand in for it where the machine has no network drive.
type archiveEnv struct {
	// resolve returns the final folder after short names, symlinks and junctions
	// (finalFolder).
	resolve func(string) (string, error)
	// remote reports whether the volume root (such as `C:\`) is a network or
	// mapped drive.
	remote func(volume string) (bool, error)
	// writable proves the folder accepts a new file.
	writable func(dir string) error
}

func realArchiveEnv() archiveEnv {
	return archiveEnv{resolve: finalFolder, remote: driveIsRemote, writable: probeWritable}
}

// probeWritable creates and removes a uniquely named file in dir. It never
// touches an existing file.
func probeWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".unravis-write-test-*")
	if err != nil {
		return err
	}
	name := f.Name()
	cerr := f.Close()
	rerr := os.Remove(name)
	if cerr != nil {
		return cerr
	}
	return rerr
}

// stripLongPrefix removes a `\\?\C:\` prefix (the drive form only). A `\\?\UNC\`
// or any other device prefix is left in place so the shape check refuses it.
func stripLongPrefix(p string) string {
	if len(p) >= 7 && strings.HasPrefix(p, `\\?\`) {
		rest := p[4:]
		if isDriveLetter(rest[0]) && rest[1] == ':' && rest[2] == '\\' {
			return rest
		}
	}
	return p
}

func isDriveLetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

// checkArchiveShape applies the rules that need no operating system call: an
// absolute drive path, no UNC, no relative form, no "." or ".." segment, no
// stream or reserved character. It returns the path with backslashes and one
// upper-case drive letter, and no trailing separator (except a drive root).
func checkArchiveShape(typed string) (string, error) {
	if typed == "" || len(typed) > maxCaseFilePath {
		return "", refuseFolder("empty-or-long")
	}
	for _, r := range typed {
		if r < 0x20 || r == 0x7f || r == 0xFFFD {
			return "", refuseFolder("chars")
		}
	}
	p := strings.ReplaceAll(typed, "/", `\`)
	p = stripLongPrefix(p)
	if strings.HasPrefix(p, `\\`) {
		return "", refuseFolder("unc-or-device")
	}
	if len(p) < 3 || !isDriveLetter(p[0]) || p[1] != ':' || p[2] != '\\' {
		return "", refuseFolder("not-absolute")
	}
	rest := strings.TrimSuffix(p[3:], `\`)
	var segs []string
	if rest != "" {
		for _, s := range strings.Split(rest, `\`) {
			switch {
			case s == "" || s == "." || s == "..":
				return "", refuseFolder("segment")
			case strings.ContainsAny(s, `<>:"|?*`):
				return "", refuseFolder("chars")
			case strings.HasSuffix(s, ".") || strings.HasSuffix(s, " "):
				return "", refuseFolder("dot-space")
			}
			segs = append(segs, s)
		}
	}
	return strings.ToUpper(p[:1]) + `:\` + strings.Join(segs, `\`), nil
}

// resolveArchiveFolder checks a typed folder against every rule of step 2 and
// returns the final, resolved folder. dataFolder is Unravis's own data folder;
// the result may not be it or inside it, whichever name or link reached it.
func (e archiveEnv) resolveArchiveFolder(typed, dataFolder string) (string, error) {
	clean, err := checkArchiveShape(typed)
	if err != nil {
		return "", err
	}
	fi, err := os.Stat(clean)
	if err != nil || !fi.IsDir() {
		return "", refuseFolder("not-a-folder")
	}
	real, err := e.resolve(clean)
	if err != nil {
		return "", refuseFolder("resolve")
	}
	// The resolved name may carry a device prefix or have left the local drive
	// (a link to a share): run the shape rules on it again.
	real, err = checkArchiveShape(real)
	if err != nil {
		return "", err
	}
	remote, err := e.remote(volumeRoot(real))
	if err != nil || remote {
		return "", refuseFolder("remote")
	}
	if dataFolder != "" {
		data := dataFolder
		if r, rerr := e.resolve(dataFolder); rerr == nil {
			data = r
		}
		if cd, derr := checkArchiveShape(data); derr == nil {
			data = cd
		}
		if insideRoot(real, data) {
			return "", refuseFolder("data-folder")
		}
	}
	if err := e.writable(real); err != nil {
		return "", refuseFolder("not-writable")
	}
	return real, nil
}

// pathUnits is the length of p in UTF-16 units, the unit Windows counts.
func pathUnits(p string) int { return len(utf16.Encode([]rune(p))) }

// checkArchiveLength refuses a folder plus file name longer than 240 characters.
func checkArchiveLength(folder, fileName string) error {
	if pathUnits(filepath.Join(folder, fileName)) > maxArchivePath {
		return refuseFolder("length")
	}
	return nil
}

var reservedDeviceNames = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true, "com5": true, "com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true, "lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

// maxSlug is the longest slug, in characters.
const maxSlug = 40

// archiveSlug makes the readable part of a case file name from the case title:
// lowercase ASCII letters and digits, every other run of characters one "-",
// trimmed, at most 40 characters; empty gives "case"; a Windows reserved device
// name gets the prefix "case-". It never comes from tags. A non-ASCII letter is
// not transliterated: it is an "other character".
func archiveSlug(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range title {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = true
		}
	}
	s := strings.TrimRight(b.String(), "-")
	if len(s) > maxSlug {
		s = strings.TrimRight(s[:maxSlug], "-")
	}
	if s == "" {
		return "case"
	}
	if reservedDeviceNames[s] {
		return "case-" + s
	}
	return s
}

// archiveFileName builds `<slug>-<short-code>-<yyyymmdd-hhmmss>.unravis-case`.
// taken says whether a name is already in use (a file there, or a name this
// server has promised); on a collision "-2", "-3" is added before the extension.
// It never returns a name that is taken, so a file is never overwritten.
func archiveFileName(title, shortCode string, at time.Time, taken func(string) bool) string {
	base := archiveSlug(title) + "-" + strings.ToLower(shortCode) + "-" + at.Format("20060102-150405")
	name := base + archiveExt
	for n := 2; taken != nil && taken(name); n++ {
		name = base + "-" + strconv.Itoa(n) + archiveExt
		if n > 10000 {
			// Not reachable in practice; stop rather than loop for ever.
			return fmt.Sprintf("%s-%d%s", base, n, archiveExt)
		}
	}
	return name
}
