//go:build windows

package blackbox

import (
	"crypto/sha1" //nolint:gosec // the certificate thumbprint is a SHA-1 by definition, not a security use
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	winreg "golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// UNVERIFIED on a live server: this file compiles and every question is read-only, but it has only been exercised
// through the fake in facts_test.go. The first live read is part of BL-696.

const factsSupported = true

func regString(root winreg.Key, path, name string) (string, bool) {
	k, err := winreg.OpenKey(root, path, winreg.QUERY_VALUE)
	if err != nil {
		return "", false
	}
	defer k.Close()
	v, _, err := k.GetStringValue(name)
	return v, err == nil
}

func regDWord(root winreg.Key, path, name string) (uint32, bool) {
	k, err := winreg.OpenKey(root, path, winreg.QUERY_VALUE)
	if err != nil {
		return 0, false
	}
	defer k.Close()
	v, _, err := k.GetIntegerValue(name)
	return uint32(v), err == nil
}

func regKeyExists(root winreg.Key, path string) bool {
	k, err := winreg.OpenKey(root, path, winreg.QUERY_VALUE)
	if err != nil {
		return false
	}
	k.Close()
	return true
}

func readFactsPlatform() *FactsGroup {
	f := &FactsGroup{}
	lm := winreg.LOCAL_MACHINE
	const cv = `SOFTWARE\Microsoft\Windows NT\CurrentVersion`
	if name, ok := regString(lm, cv, "ProductName"); ok {
		build, _ := regString(lm, cv, "CurrentBuild")
		ubr, _ := regDWord(lm, cv, "UBR")
		f.OS = strings.TrimSpace(fmt.Sprintf("%s %s.%d", name, build, ubr))
	} else {
		f.Unread = append(f.Unread, "os")
	}
	if r, ok := regDWord(lm, `SOFTWARE\Microsoft\NET Framework Setup\NDP\v4\Full`, "Release"); ok {
		f.DotNetRelease = int(r)
	}
	if maj, ok := regDWord(lm, `SOFTWARE\Microsoft\InetStp`, "MajorVersion"); ok {
		minor, _ := regDWord(lm, `SOFTWARE\Microsoft\InetStp`, "MinorVersion")
		f.IIS = fmt.Sprintf("%d.%d", maj, minor)
	}
	f.TimeZone, _ = regString(lm, `SYSTEM\CurrentControlSet\Control\TimeZoneInformation`, "TimeZoneKeyName")
	f.PendingReboot = regKeyExists(lm, `SOFTWARE\Microsoft\Windows\CurrentVersion\Component Based Servicing\RebootPending`) ||
		regKeyExists(lm, `SOFTWARE\Microsoft\Windows\CurrentVersion\WindowsUpdate\Auto Update\RebootRequired`)
	f.SQL = readSQLVersions()
	f.Services = readServiceFacts(&f.Unread)
	f.Certs = readCertFacts(&f.Unread)
	return f
}

// readSQLVersions maps each instance name to "<version> <edition>" from the instance's Setup key.
func readSQLVersions() map[string]string {
	const root = `SOFTWARE\Microsoft\Microsoft SQL Server`
	names, err := winreg.OpenKey(winreg.LOCAL_MACHINE, root+`\Instance Names\SQL`, winreg.QUERY_VALUE)
	if err != nil {
		return nil
	}
	defer names.Close()
	list, err := names.ReadValueNames(0)
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for _, n := range list {
		id, _, err := names.GetStringValue(n)
		if err != nil || id == "" || strings.ContainsAny(id, `\/`) {
			continue
		}
		setup := root + `\` + id + `\Setup`
		ver, _ := regString(winreg.LOCAL_MACHINE, setup, "Version")
		ed, _ := regString(winreg.LOCAL_MACHINE, setup, "Edition")
		out[strings.ToUpper(n)] = strings.TrimSpace(ver + " " + ed)
	}
	return out
}

func stateWord(s svc.State) string {
	switch s {
	case svc.Running:
		return "running"
	case svc.Stopped:
		return "stopped"
	case svc.StartPending:
		return "start-pending"
	case svc.StopPending:
		return "stop-pending"
	}
	return "other"
}

func startWord(t uint32) string {
	switch t {
	case mgr.StartAutomatic:
		return "auto"
	case mgr.StartManual:
		return "manual"
	case mgr.StartDisabled:
		return "disabled"
	}
	return "other"
}

// readServiceFacts reports the state and start type of the stack's services. The recorder runs as LocalSystem, so it
// may open the service manager. Names and states only: never the account a service runs as or its command line.
func readServiceFacts(unread *[]string) []ServiceFact {
	m, err := mgr.Connect()
	if err != nil {
		*unread = append(*unread, "services")
		return nil
	}
	defer m.Disconnect()
	names, err := m.ListServices()
	if err != nil {
		*unread = append(*unread, "services")
		return nil
	}
	var out []ServiceFact
	for _, n := range names {
		if !factServiceWanted(n) {
			continue
		}
		s, err := m.OpenService(n)
		if err != nil {
			continue
		}
		fact := ServiceFact{Name: n, State: "other", Start: "other"}
		if st, err := s.Query(); err == nil {
			fact.State = stateWord(st.State)
		}
		if cfg, err := s.Config(); err == nil {
			fact.Start = startWord(cfg.StartType)
		}
		s.Close()
		out = append(out, fact)
	}
	return out
}

// readCertFacts lists the certificates of the computer's Personal store: subject, thumbprint and end date. The store is
// opened read-only. Private keys are never touched.
func readCertFacts(unread *[]string) []CertFact {
	name, err := windows.UTF16PtrFromString("MY")
	if err != nil {
		return nil
	}
	store, err := windows.CertOpenStore(windows.CERT_STORE_PROV_SYSTEM, 0, 0,
		windows.CERT_SYSTEM_STORE_LOCAL_MACHINE|windows.CERT_STORE_OPEN_EXISTING_FLAG|windows.CERT_STORE_READONLY_FLAG,
		uintptr(unsafe.Pointer(name)))
	if err != nil {
		*unread = append(*unread, "certs")
		return nil
	}
	defer windows.CertCloseStore(store, 0)
	var out []CertFact
	var prev *windows.CertContext
	for len(out) < maxFactCerts {
		ctx, err := windows.CertEnumCertificatesInStore(store, prev)
		if err != nil || ctx == nil {
			break
		}
		prev = ctx
		der := unsafe.Slice(ctx.EncodedCert, ctx.Length)
		c, err := x509.ParseCertificate(der)
		if err != nil {
			continue
		}
		sum := sha1.Sum(c.Raw) //nolint:gosec // thumbprint, see above
		out = append(out, CertFact{
			Subject:    c.Subject.String(),
			Thumbprint: strings.ToUpper(hex.EncodeToString(sum[:])),
			NotAfter:   c.NotAfter.UTC().Format(time.RFC3339),
		})
	}
	return out
}
