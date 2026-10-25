package configparse

import (
	"encoding/xml"
	"io"
	"strings"
)

// Stdout is the ASP.NET Core Module console log setting of one aspNetCore
// element.
type Stdout struct {
	Enabled bool   // @stdoutLogEnabled
	File    string // @stdoutLogFile, raw, relative to the web.config folder
}

const maxStdout = 8

// ParseWebConfig reads a web.config and keeps only stdoutLogFile and
// stdoutLogEnabled from system.webServer/aspNetCore, in the file or under a
// location element. processPath, arguments, environmentVariables (often secrets)
// and every other element and attribute are dropped. An aspNetCore element with
// no stdoutLogFile is not reported.
func ParseWebConfig(r io.Reader) ([]Stdout, error) {
	var out []Stdout
	err := scan(r, func(path []string, attrs []xml.Attr) {
		if !pathIs(path, "configuration", "system.webServer", "aspNetCore") &&
			!pathIs(path, "configuration", "location", "system.webServer", "aspNetCore") {
			return
		}
		var s Stdout
		for _, a := range attrs {
			switch a.Name.Local {
			case "stdoutLogFile":
				s.File = clip(a.Value)
			case "stdoutLogEnabled":
				s.Enabled = strings.EqualFold(strings.TrimSpace(a.Value), "true")
			}
		}
		if s.File != "" && len(out) < maxStdout {
			out = append(out, s)
		}
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
