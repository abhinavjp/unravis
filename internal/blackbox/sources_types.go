package blackbox

import (
	"io/fs"
	"time"
)

// The sources manifest (sources.json): a small read-only file in the recordings folder that says WHERE the logs of
// this server are, found the way the recorder finds them itself (applicationHost.config for IIS, the registry for
// SQL Server, the Redis settings file, the event log folder, the settings of this Blackbox). It holds paths, sizes
// and times only, never a line of any log. Servers (Unravis) reads it so nobody types a folder by hand.
//
// Format: docs/blackbox/SOURCES-MANIFEST.md. Bump SourcesSchema only for a change a reader cannot ignore.

// SourcesName is the file name in the recordings folder, and the path served at /sources.
const SourcesName = "sources.json"

// SourcesSchema is the version of the document.
const SourcesSchema = 1

// Kinds of source. They are the kinds Unravis imports, so a reader maps one to one.
const (
	SourceRecordings = "blackbox"  // this Blackbox's own recordings
	SourceIIS        = "iis"       // IIS site logs (one entry per site, or the central log)
	SourceHTTPErr    = "httperr"   // HTTP.sys error logs
	SourceSQL        = "sqlserver" // SQL Server ERRORLOG, one per instance
	SourceRedis      = "redis"     // Redis or Memurai log
	SourceWinEvents  = "winevents" // a Windows event log channel (the .evtx file)
	SourceApp        = "applog"    // a folder named in this Blackbox's settings
)

// Limits that keep the file small and the work cheap.
const (
	maxSourceEntries  = 200
	maxSourceScan     = 20000 // directory entries read for one source; more is "truncated"
	sourcesRefreshMin = 15    // minutes between looks
)

// SourceEntry is one place that holds logs.
type SourceEntry struct {
	// ID is stable between runs: "recordings", "iis.site.<siteId>", "iis.central", "httperr", "sql.<instance>", "redis.<n>",
	// "evt.<channel>", "app.<n>".
	ID string `json:"id"`
	// Kind is one of the Source* constants.
	Kind string `json:"kind"`
	// Label is plain words for a person: "IIS site Default Web Site".
	Label string `json:"label"`
	// Dir is the folder (a full local path, backslashes, no trailing one).
	Dir string `json:"dir"`
	// Pattern says which files in Dir belong to it: a file name pattern, several joined with "|".
	Pattern string `json:"pattern"`
	// Channel is the event log channel, for winevents only.
	Channel string `json:"channel,omitempty"`
	// Origin says how it was found: "applicationHost.config", "registry", "redis settings", "settings", "default".
	Origin string `json:"origin"`
	// Exists says the folder is there. Readable says this recorder could list it (its own account, not yours).
	Exists   bool `json:"exists"`
	Readable bool `json:"readable"`
	// Files, Bytes and Newest describe the files that match Pattern: how many, the sum of their sizes, and the newest
	// write time in Unix seconds (0 when none). Truncated is true when listing stopped at maxSourceScan entries.
	Files     int   `json:"files"`
	Bytes     int64 `json:"bytes"`
	Newest    int64 `json:"newestUnix,omitempty"`
	Truncated bool  `json:"truncated,omitempty"`
	// Err is a fixed word when the folder could not be looked at: "not-found", "access-denied" or "error".
	Err string `json:"err,omitempty"`
}

// SourcesDoc is sources.json.
type SourcesDoc struct {
	Schema    int           `json:"schema"`
	Host      string        `json:"host"`
	Version   string        `json:"version,omitempty"`
	Generated time.Time     `json:"generated"`
	Sources   []SourceEntry `json:"sources"`
}

// SourcesEnv is everything discovery reads from the machine, so tests give a fake and nothing else is touched.
// Every function may be nil in a test; the discovery then treats it as "nothing there".
type SourcesEnv struct {
	// Getenv reads an environment variable (windir, SystemDrive, ProgramFiles ...).
	Getenv func(string) string
	// ReadFile reads at most max bytes of one file.
	ReadFile func(path string, max int64) ([]byte, error)
	// ReadDir lists one folder. It must not follow links into other folders.
	ReadDir func(path string) ([]fs.DirEntry, error)
	// SQLLogs lists the local SQL Server instances and the path of each ERRORLOG (the registry on Windows).
	SQLLogs func() []sqlLogInstance
	// RedisConfs lists full paths of Redis or Memurai settings files found from the installed services.
	RedisConfs func() []string
}
