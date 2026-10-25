package discover

import (
	"bytes"
	"context"

	"api-audit-explorer/internal/discover/configparse"
)

// MaxSiteDirs bounds how many site folders IISSiteDirs returns.
const MaxSiteDirs = 256

// IISSiteDirs returns a SiteDirs that lists the physical folder of every IIS
// application in applicationHost.config. The applog and host detectors look in
// those folders (spec 2.3). It reads the one config file through env.FS, keeps
// only the folders, and gives none when the file is missing, too large,
// unparsable or unreadable: a missing site folder is not an error for them. The
// folders are validated like any config path (absolute, local, no variables
// other than the four IISRoots stand for) and returned without duplicates.
func IISSiteDirs(env Env, roots IISRoots) SiteDirs {
	roots = roots.filled()
	return func(ctx context.Context) []string {
		if env.FS == nil || ctx.Err() != nil {
			return nil
		}
		cfg := WinJoin(roots.WinDir, `System32\inetsrv\config\applicationHost.config`)
		fi, err := env.FS.Stat(cfg)
		if err != nil || fi.IsDir || fi.Reparse || fi.Size > MaxConfigBytes {
			return nil
		}
		b, err := env.FS.Head(cfg, MaxConfigBytes)
		if err != nil {
			return nil
		}
		ah, err := configparse.ParseAppHost(bytes.NewReader(b))
		if err != nil || ah == nil {
			return nil
		}
		seen := map[string]bool{}
		var out []string
		for _, s := range ah.Sites {
			for _, app := range s.AppPaths {
				if ctx.Err() != nil || len(out) >= MaxSiteDirs {
					return out
				}
				phys, err := ValidLogPath(roots.expand(app), "")
				if err != nil {
					continue
				}
				if k := CanonKey(phys); !seen[k] {
					seen[k] = true
					out = append(out, phys)
				}
			}
		}
		return out
	}
}
