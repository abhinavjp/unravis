package blackbox

import (
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// B7: server facts, once an hour. Versions, the services of the stack and their state, certificate end dates, the time
// zone and whether Windows waits for a restart. These are the "what is this server" figures a timeline case needs next
// to the counters, and they answer the first questions of a call ("which SQL build? did the certificate run out?").
//
// Only fixed, read-only questions are asked: a registry value, the service list, the computer's certificate store.
// Never a password, a private key, an account name, a file's content or a connection string.

func init() { RegisterGroup("facts", true, newFactsGroup) }

// Bounds: a server with an odd number of services or certificates must not make a sample large.
const (
	maxFactServices = 64
	maxFactCerts    = 200
	maxFactText     = 128
)

// ServiceFact is one service of the stack.
type ServiceFact struct {
	Name  string `json:"name"`
	State string `json:"state"` // running, stopped, start-pending, stop-pending, other
	Start string `json:"start"` // auto, manual, disabled, other
}

// CertFact is one certificate of the computer store: who it is for, which one it is, and when it ends.
type CertFact struct {
	Subject    string `json:"subject"`
	Thumbprint string `json:"thumbprint"`
	NotAfter   string `json:"notAfter"` // RFC 3339, UTC
}

// FactsGroup is the sample block of the server facts.
type FactsGroup struct {
	OS            string            `json:"os,omitempty"`            // "Windows Server 2019 Standard 17763.5936"
	DotNetRelease int               `json:"dotNetRelease,omitempty"` // .NET Framework 4.x release number
	IIS           string            `json:"iis,omitempty"`           // "10.0"
	SQL           map[string]string `json:"sql,omitempty"`           // instance name -> "16.0.1000.6 Enterprise Edition"
	TimeZone      string            `json:"timeZone,omitempty"`      // Windows zone key, such as "GMT Standard Time"
	PendingReboot bool              `json:"pendingReboot,omitempty"`
	Services      []ServiceFact     `json:"services,omitempty"`
	Certs         []CertFact        `json:"certs,omitempty"`
	// Unread names the questions that could not be answered (access denied, not present), so a gap is visible.
	Unread []string `json:"unread,omitempty"`
}

// factsPlatform reads the facts from the machine. Windows reads the registry, the service list and the certificate
// store; elsewhere it answers nothing. It is a variable so tests run without Windows.
var factsPlatform = readFactsPlatform

type factsGroup struct{ read func() *FactsGroup }

func newFactsGroup(GroupDeps) (Group, error) {
	if !factsSupported {
		return nil, errNotApplicable
	}
	return &factsGroup{read: factsPlatform}, nil
}

func (g *factsGroup) Name() string   { return "facts" }
func (g *factsGroup) Optional() bool { return true }

func (g *factsGroup) Collect(time.Time, float64) (any, error) {
	f := g.read()
	if f == nil {
		return nil, errGroupUnavailable
	}
	return boundFacts(f), nil
}

// boundFacts sorts, deduplicates and caps what the platform returned.
func boundFacts(f *FactsGroup) *FactsGroup {
	out := *f
	out.OS = clip(out.OS)
	out.IIS = clip(out.IIS)
	out.TimeZone = clip(out.TimeZone)
	if len(f.SQL) > 0 {
		out.SQL = map[string]string{}
		keys := make([]string, 0, len(f.SQL))
		for k := range f.SQL {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for i, k := range keys {
			if i >= 16 {
				break
			}
			out.SQL[clip(k)] = clip(f.SQL[k])
		}
	}
	out.Services = nil
	seen := map[string]bool{}
	for _, s := range f.Services {
		k := strings.ToLower(s.Name)
		if seen[k] || len(out.Services) >= maxFactServices {
			continue
		}
		seen[k] = true
		out.Services = append(out.Services, ServiceFact{Name: clip(s.Name), State: clip(s.State), Start: clip(s.Start)})
	}
	sort.Slice(out.Services, func(i, j int) bool { return out.Services[i].Name < out.Services[j].Name })
	out.Certs = nil
	seenC := map[string]bool{}
	for _, c := range f.Certs {
		if seenC[c.Thumbprint] {
			continue
		}
		seenC[c.Thumbprint] = true
		out.Certs = append(out.Certs, CertFact{Subject: clip(c.Subject), Thumbprint: clip(c.Thumbprint), NotAfter: c.NotAfter})
	}
	// The soonest end date first: that is the one worth looking at. The cut comes AFTER the sort, so a store with
	// more than the cap never loses the certificates that are about to end.
	sort.Slice(out.Certs, func(i, j int) bool { return out.Certs[i].NotAfter < out.Certs[j].NotAfter })
	unread := append([]string(nil), f.Unread...)
	if len(out.Certs) > maxFactCerts {
		out.Certs = out.Certs[:maxFactCerts]
		unread = append(unread, "certs: truncated")
	}
	if len(f.Services) > maxFactServices {
		unread = append(unread, "services: truncated")
	}
	out.Unread = nil
	for _, u := range unread {
		if len(out.Unread) < 16 {
			out.Unread = append(out.Unread, clip(u))
		}
	}
	return &out
}

func clip(s string) string {
	if len(s) > maxFactText {
		// Cut on a character boundary: half a character would be written as a replacement character.
		cut := maxFactText
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut]
	}
	return strings.Map(func(r rune) rune {
		if r < 0x20 {
			return -1
		}
		return r
	}, s)
}

// factServiceWanted says whether a service belongs to the stack the Blackbox reports on. The list is fixed in code.
func factServiceWanted(name string) bool {
	n := strings.ToLower(name)
	switch n {
	case "w3svc", "was", "mssqlserver", "sqlserveragent", "sqlbrowser", "redis", "salt-minion", "unravisblackbox", "unravisblackboxsql", "msdtc":
		return true
	}
	return strings.HasPrefix(n, "mssql$") || strings.HasPrefix(n, "sqlagent$") || strings.HasPrefix(n, "redis")
}
