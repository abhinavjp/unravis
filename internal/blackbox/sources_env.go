package blackbox

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// OSSourcesEnv reads this machine. Listings stop one entry past the scan limit, so a folder with millions of files costs
// one bounded read, and links are never followed (a directory entry is not resolved).
func OSSourcesEnv() SourcesEnv {
	return SourcesEnv{
		Getenv: os.Getenv,
		ReadFile: func(path string, max int64) ([]byte, error) {
			f, err := os.Open(path) //nolint:gosec // a settings file the discovery names
			if err != nil {
				return nil, err
			}
			defer func() { _ = f.Close() }()
			return io.ReadAll(io.LimitReader(f, max))
		},
		ReadDir: func(path string) ([]fs.DirEntry, error) {
			f, err := os.Open(path) //nolint:gosec // a folder the discovery names; listed, never read
			if err != nil {
				return nil, err
			}
			defer func() { _ = f.Close() }()
			return f.ReadDir(scanLimit + 1)
		},
		SQLLogs:    func() []sqlLogInstance { return sqlLogInstancesOrNil() },
		RedisConfs: redisConfCandidates,
	}
}

func sqlLogInstancesOrNil() []sqlLogInstance {
	if sqlLogInstancesPlatform == nil {
		return nil
	}
	return sqlLogInstancesPlatform()
}

// redisConfCandidates are the settings files of Redis or Memurai that exist: the usual install folders, plus the file a
// Windows service was installed with (redisServiceConfs).
func redisConfCandidates() []string {
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		k := strings.ToLower(filepath.Clean(p))
		if p == "" || seen[k] {
			return
		}
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() { //nolint:gosec // a well-known settings path, only stat'ed
			seen[k] = true
			out = append(out, p)
		}
	}
	for _, root := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramW6432"), `C:\Program Files`, `C:\`, `D:\`} {
		if root == "" {
			continue
		}
		for _, rel := range []string{`Redis\redis.windows-service.conf`, `Redis\redis.windows.conf`, `Redis\redis.conf`, `Memurai\memurai.conf`, `Redis-x64\redis.windows-service.conf`} {
			add(filepath.Join(root, rel))
		}
	}
	for _, p := range redisServiceConfs() {
		add(p)
	}
	return out
}

// confFromImagePath finds the settings file in a service command line, quoted or not:
//
//	"C:\Program Files\Redis\RedisService.exe" --service-run "C:\Program Files\Redis\redis.windows-service.conf"
//	C:\Redis\redis-server.exe --service-run C:\Redis\redis.conf
//
// It returns "" unless the argument is a full drive path ending in .conf.
func confFromImagePath(img string) string {
	low := strings.ToLower(img)
	end := strings.LastIndex(low, ".conf")
	if end < 0 {
		return ""
	}
	end += len(".conf")
	head := img[:end]
	var cand string
	if q := strings.LastIndex(head, `"`); q >= 0 && isDrivePath(head[q+1:]) {
		cand = head[q+1:]
	} else if sp := strings.LastIndex(head, " "); sp >= 0 && isDrivePath(head[sp+1:]) {
		cand = head[sp+1:]
	} else if isDrivePath(head) {
		cand = head
	}
	return cand
}

func isDrivePath(s string) bool {
	return len(s) > 3 && s[1] == ':' && s[2] == '\\' && ((s[0] >= 'A' && s[0] <= 'Z') || (s[0] >= 'a' && s[0] <= 'z'))
}
