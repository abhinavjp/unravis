package cliattach

import (
	"strings"
	"unicode/utf16"
)

// parseEnvBlock reads an environment block (NAME=value strings, each ended by a zero, the whole ended by an empty one). An entry
// that starts with "=" (the per-drive working folders) is left out.
func parseEnvBlock(b []uint16) []string {
	var out []string
	for len(b) > 0 && b[0] != 0 {
		n := 0
		for n < len(b) && b[n] != 0 {
			n++
		}
		if kv := string(utf16.Decode(b[:n])); !strings.HasPrefix(kv, "=") {
			out = append(out, kv)
		}
		if n >= len(b) {
			break
		}
		b = b[n+1:]
	}
	return out
}

// childEnv is the environment of the headless owner: the default one of this user (not the one of this command line), without the names that would send the owner to
// another run folder or data folder than the one this command line reads (AUDIT_*, UNRAVIS_*), and with LOCALAPPDATA set to the real
// known folder, which is where this command line looks for the locator. Names are compared without regard to case, as Windows does.
func childEnv(environ []string, localAppData string) []string {
	out := make([]string, 0, len(environ)+1)
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		up := strings.ToUpper(name)
		if strings.HasPrefix(up, "AUDIT_") || strings.HasPrefix(up, "UNRAVIS_") || up == "LOCALAPPDATA" {
			continue
		}
		out = append(out, kv)
	}
	if localAppData != "" {
		out = append(out, "LOCALAPPDATA="+localAppData)
	}
	return out
}
