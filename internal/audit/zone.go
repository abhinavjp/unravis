package audit

import (
	"time"
	_ "time/tzdata" // embedded zone database; Windows ships none
)

// maxZoneNameLength bounds a time-zone name read from a request.
const maxZoneNameLength = 64

// LoadZone resolves an IANA zone name such as "America/New_York". An empty name
// is UTC. "Local" is refused: it would mean the server's zone, not the
// browser's. The error never carries the name back.
func LoadZone(name string) (*time.Location, error) {
	if name == "" {
		return time.UTC, nil
	}
	if len(name) > maxZoneNameLength || name == "Local" {
		return nil, invalidInputf("tz must be an IANA time zone name such as UTC or America/New_York")
	}
	for _, c := range name {
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '/' || c == '_' || c == '-' || c == '+'
		if !ok {
			return nil, invalidInputf("tz must be an IANA time zone name such as UTC or America/New_York")
		}
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, invalidInputf("tz must be an IANA time zone name such as UTC or America/New_York")
	}
	return loc, nil
}
