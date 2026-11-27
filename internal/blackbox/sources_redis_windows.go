//go:build windows

package blackbox

import (
	"strings"

	winreg "golang.org/x/sys/windows/registry"
)

// redisServiceConfs reads the command line of the services named Redis or Memurai* and returns the settings file they were
// installed with (the argument that ends in .conf). Read only; the registry key is the one the service control manager keeps.
func redisServiceConfs() []string {
	root, err := winreg.OpenKey(winreg.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services`, winreg.ENUMERATE_SUB_KEYS)
	if err != nil {
		return nil
	}
	defer func() { _ = root.Close() }()
	names, err := root.ReadSubKeyNames(-1)
	if err != nil {
		return nil
	}
	var out []string
	for _, n := range names {
		l := strings.ToLower(n)
		if l != "redis" && !strings.HasPrefix(l, "memurai") {
			continue
		}
		k, err := winreg.OpenKey(winreg.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services\`+n, winreg.QUERY_VALUE)
		if err != nil {
			continue
		}
		img, _, err := k.GetStringValue("ImagePath")
		_ = k.Close()
		if err != nil {
			continue
		}
		if p := confFromImagePath(img); p != "" {
			out = append(out, p)
		}
	}
	return out
}
