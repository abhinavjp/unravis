package discover_test

import (
	"context"
	"strings"
	"testing"

	"api-audit-explorer/internal/discover"
	"api-audit-explorer/internal/discover/fake"
)

func TestIISSiteDirsListsApplicationFolders(t *testing.T) {
	e := fake.New()
	putAppHost(e, appHostXML(
		siteXML("1", `%SystemDrive%\inetpub\wwwroot`, "")+
			siteXML("2", `D:\sites\web-a`, "")+
			siteXML("3", `d:\SITES\web-a`, "")+ // same folder, other case
			siteXML("4", `\\share.example.test\site`, "")+ // a share is refused
			siteXML("5", `%USERPROFILE%\x`, ""), // other variables are refused
		stockDefaults))
	got := discover.IISSiteDirs(e.Env(nil), discover.DefaultIISRoots())(context.Background())
	want := []string{`C:\inetpub\wwwroot`, `D:\sites\web-a`}
	if len(got) != len(want) {
		t.Fatalf("site dirs = %q, want %q", got, want)
	}
	for i := range want {
		if !strings.EqualFold(got[i], want[i]) {
			t.Errorf("site dir %d = %q, want %q", i, got[i], want[i])
		}
	}
	for _, d := range got {
		if strings.Contains(d, sentinel) {
			t.Errorf("a secret reached a site dir: %q", d)
		}
	}
}

func TestIISSiteDirsGivesNothingWithoutAConfig(t *testing.T) {
	e := fake.New()
	if got := discover.IISSiteDirs(e.Env(nil), discover.IISRoots{})(context.Background()); len(got) != 0 {
		t.Errorf("no config gave %q", got)
	}
	e.FS.AddFile(appHostPath, 5, day, []byte("<conf"))
	if got := discover.IISSiteDirs(e.Env(nil), discover.IISRoots{})(context.Background()); len(got) != 0 {
		t.Errorf("a broken config gave %q", got)
	}
	e2 := fake.New()
	putAppHost(e2, appHostXML(siteXML("1", `C:\www`, ""), stockDefaults))
	e2.FS.Deny(appHostPath)
	if got := discover.IISSiteDirs(e2.Env(nil), discover.IISRoots{})(context.Background()); len(got) != 0 {
		t.Errorf("a denied config gave %q", got)
	}
}
