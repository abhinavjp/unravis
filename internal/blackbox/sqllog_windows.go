//go:build windows

package blackbox

import (
	"strings"

	winreg "golang.org/x/sys/windows/registry"
)

const sqlRegRoot = `SOFTWARE\Microsoft\Microsoft SQL Server`

// sqlLogInstancesPlatform lists the local SQL Server instances and where each one
// writes its ERRORLOG, from the registry: Instance Names\SQL maps an instance name
// to an id (MSSQL16.MSSQLSERVER), and that id's MSSQLServer\Parameters key holds the
// startup arguments, where "-e<path>" names the ERRORLOG. Read only.
var sqlLogInstancesPlatform = func() []sqlLogInstance {
	names, err := winreg.OpenKey(winreg.LOCAL_MACHINE, sqlRegRoot+`\Instance Names\SQL`, winreg.QUERY_VALUE)
	if err != nil {
		return nil
	}
	defer names.Close()
	list, err := names.ReadValueNames(0)
	if err != nil {
		return nil
	}
	var out []sqlLogInstance
	for _, name := range list {
		id, _, err := names.GetStringValue(name)
		if err != nil || id == "" || strings.ContainsAny(id, `\/`) {
			continue
		}
		pk, err := winreg.OpenKey(winreg.LOCAL_MACHINE, sqlRegRoot+`\`+id+`\MSSQLServer\Parameters`, winreg.QUERY_VALUE)
		if err != nil {
			continue
		}
		vals, _ := pk.ReadValueNames(0)
		for _, v := range vals {
			if !strings.HasPrefix(strings.ToLower(v), "sqlarg") {
				continue
			}
			s, _, err := pk.GetStringValue(v)
			if err == nil && len(s) > 2 && strings.EqualFold(s[:2], "-e") {
				out = append(out, sqlLogInstance{Label: strings.ToUpper(name), LogPath: s[2:]})
				break
			}
		}
		pk.Close()
	}
	return out
}
