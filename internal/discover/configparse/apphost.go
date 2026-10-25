package configparse

import (
	"encoding/xml"
	"io"
	"strings"
	"unicode"
)

// Site is what an extractor keeps of one IIS site. Empty fields were not set on
// the site (inherit the defaults). Values are raw: the caller runs them through
// the path checks.
type Site struct {
	ID        string // digits only
	LogDir    string // system.applicationHost/sites/site/logFile@directory
	LogFormat string // logFile@logFormat
	FrebDir   string // traceFailedRequestsLogging@directory
	// AppPaths are the physical paths of the site's applications, kept only to
	// find each web.config. At most maxApps.
	AppPaths []string
}

// Pool is an application pool's name and .NET CLR version. Identity, password
// and every other setting are dropped.
type Pool struct {
	Name           string
	RuntimeVersion string // "v4.0", "v2.0" or "" for no managed code
}

// AppHost is everything kept from applicationHost.config.
type AppHost struct {
	Sites []Site
	Pools []Pool
	// siteDefaults values.
	DefaultLogDir    string
	DefaultLogFormat string
	DefaultFrebDir   string
	// Dropped counts entries left out: a site whose id is not a plain number, a
	// pool with an unusable name, or an entry beyond a cap.
	Dropped int
}

const (
	maxSites = 2048
	maxApps  = 32
	maxPools = 1024
)

func join(base []string, more ...string) []string {
	return append(append([]string(nil), base...), more...)
}

var (
	pathSites    = []string{"configuration", "system.applicationHost", "sites"}
	pathPools    = []string{"configuration", "system.applicationHost", "applicationPools"}
	pathSite     = join(pathSites, "site")
	pathDefaults = join(pathSites, "siteDefaults")
	pathPoolAdd  = join(pathPools, "add")
	pathSiteLog  = join(pathSite, "logFile")
	pathSiteFreb = join(pathSite, "traceFailedRequestsLogging")
	pathSiteVDir = join(pathSite, "application", "virtualDirectory")
	pathDefLog   = join(pathDefaults, "logFile")
	pathDefFreb  = join(pathDefaults, "traceFailedRequestsLogging")
)

// ParseAppHost reads applicationHost.config and keeps only: each site's id,
// logFile directory and format, traceFailedRequestsLogging directory and the
// physical path of each application root; the siteDefaults logFile directory
// and format and traceFailedRequestsLogging directory; and each application
// pool's name and managedRuntimeVersion. Bindings, host names, site and
// application names, identities, passwords, connection strings and every other
// attribute are read past and dropped.
func ParseAppHost(r io.Reader) (*AppHost, error) {
	ah := &AppHost{}
	cur := -1 // index into ah.Sites of the site being read, -1 when it was dropped
	err := scan(r, func(path []string, attrs []xml.Attr) {
		switch {
		case pathIs(path, pathSite...):
			cur = -1
			id := attr(attrs, "id")
			if !plainID(id) || len(ah.Sites) >= maxSites {
				ah.Dropped++
				return
			}
			ah.Sites = append(ah.Sites, Site{ID: id})
			cur = len(ah.Sites) - 1
		case pathIs(path, pathSiteLog...):
			if cur < 0 {
				return
			}
			dir, format := logAttrs(attrs)
			if dir != "" {
				ah.Sites[cur].LogDir = dir
			}
			if format != "" {
				ah.Sites[cur].LogFormat = format
			}
		case pathIs(path, pathSiteFreb...):
			if cur < 0 {
				return
			}
			if dir := attr(attrs, "directory"); dir != "" {
				ah.Sites[cur].FrebDir = clip(dir)
			}
		case pathIs(path, pathSiteVDir...):
			if cur < 0 || len(ah.Sites[cur].AppPaths) >= maxApps {
				return
			}
			var vpath, phys string
			for _, a := range attrs {
				switch a.Name.Local {
				case "path":
					vpath = a.Value
				case "physicalPath":
					phys = a.Value
				}
			}
			if vpath == "/" && phys != "" {
				ah.Sites[cur].AppPaths = append(ah.Sites[cur].AppPaths, clip(phys))
			}
		case pathIs(path, pathDefLog...):
			dir, format := logAttrs(attrs)
			if dir != "" {
				ah.DefaultLogDir = dir
			}
			if format != "" {
				ah.DefaultLogFormat = format
			}
		case pathIs(path, pathDefFreb...):
			if dir := attr(attrs, "directory"); dir != "" {
				ah.DefaultFrebDir = clip(dir)
			}
		case pathIs(path, pathPoolAdd...):
			var name, rt string
			for _, a := range attrs {
				switch a.Name.Local {
				case "name":
					name = a.Value
				case "managedRuntimeVersion":
					rt = a.Value
				}
			}
			if !plainName(name) || len(ah.Pools) >= maxPools {
				ah.Dropped++
				return
			}
			if len(rt) > 16 || (rt != "" && !plainName(rt)) {
				rt = ""
			}
			ah.Pools = append(ah.Pools, Pool{Name: name, RuntimeVersion: rt})
		}
	})
	if err != nil {
		return nil, err
	}
	return ah, nil
}

func logAttrs(attrs []xml.Attr) (dir, format string) {
	for _, a := range attrs {
		switch a.Name.Local {
		case "directory":
			dir = clip(a.Value)
		case "logFormat":
			format = a.Value
			if len(format) > 32 {
				format = Overlong
			}
		}
	}
	return dir, format
}

func attr(attrs []xml.Attr, name string) string {
	for _, a := range attrs {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

// plainID is a site id: one to ten digits.
func plainID(s string) bool {
	if len(s) == 0 || len(s) > 10 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// plainName is a pool name or version: 1 to 128 characters, none a control
// character.
func plainName(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || r == 0xFFFD {
			return false
		}
	}
	return strings.TrimSpace(s) != ""
}
