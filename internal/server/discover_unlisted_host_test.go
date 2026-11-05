package server

// Host discovery imports every file of a finding under the computer the scan
// ran on. A file whose own server name is not in the host list is refused
// here: the picked computer stands in only for a person's explicit import.
// The names in these files are synthetic.

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"api-audit-explorer/internal/importer"
)

// discUnlistedW3C is a small IIS W3C log in the discovery fixture's window that
// names a server the host list does not know (the fixture list carries web-a
// and mongo-a only).
func discUnlistedW3C(day, server string) string {
	var b strings.Builder
	b.WriteString("#Software: Microsoft Internet Information Services 10.0\n")
	b.WriteString("#Version: 1.0\n#Date: " + day + " 00:00:00\n")
	b.WriteString("#Fields: date time s-computername s-ip cs-method cs-uri-stem sc-status time-taken\n")
	for i := 0; i < 3; i++ {
		fmt.Fprintf(&b, "%s 00:00:%02d %s 192.0.2.10 GET /api/v1/orders/%d 200 %d\n", day, i+1, server, 1000+i, 10+i)
	}
	return b.String()
}

// A discovery import refuses a file that names a computer outside the host
// list, even though the scan ran on a listed computer: nothing of the finding
// is stored. The same bytes through the explicit wizard import with the person's
// pick do import, under the picked computer.
func TestDiscoverUnlistedServerNameRefusedButWizardImportWorks(t *testing.T) {
	f := newDiscFix(t, nil)
	// both fixture logs leave, two logs naming an unlisted server enter, so no
	// pull order can store a listed host's file before the refusal
	f.addLog("u_ex010911.log", discUnlistedW3C("2001-09-11", "ISS-PROD-09"), discDay)
	f.addLog("u_ex010912.log", discUnlistedW3C("2001-09-12", "ISS-PROD-09"), discDay.AddDate(0, 0, 1))
	cid := f.e.newCase("Discovery, unlisted server name")
	sid, sc := f.scan()
	iis := f.iisFinding(sc)
	if iis.Files != 2 {
		t.Fatalf("finding = %+v, want two files", iis)
	}
	jobs := f.importJobs(cid, sid, []string{iis.ID}, discFrom, discTo)
	if len(jobs) != 1 {
		t.Fatalf("jobs %d", len(jobs))
	}
	f.wantFailed(jobs[0], http.StatusUnprocessableEntity, string(importer.ReasonUnknownHost))
	if n := len(f.e.importsOf(cid)); n != 0 {
		t.Fatalf("a refused discovery stored %d imports", n)
	}
	// the same file through the explicit wizard import, computer web-a picked
	res := f.e.upload("/api/cases/"+cid+"/imports?importer=iis-w3c&host=web-a", ackFields, &upload{"u_unlisted.log", discUnlistedW3C("2001-09-11", "ISS-PROD-09")})
	if res.Code != http.StatusCreated {
		t.Fatalf("wizard import = %d %s", res.Code, res.Body)
	}
	imports := f.e.importsOf(cid)
	if len(imports) != 1 || imports[0].HostLabel != "web-a" || imports[0].Rows != 3 {
		t.Fatalf("imports = %+v, want the picked label web-a with 3 rows", imports)
	}
}
