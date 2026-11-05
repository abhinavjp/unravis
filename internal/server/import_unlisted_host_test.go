package server

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"api-audit-explorer/internal/casestore"
	"api-audit-explorer/internal/importer"
)

// A real IIS log names its own server in s-computername. When that name is not
// in the host list, the computer picked for the file stands in for it. Names are
// synthetic.

func unlistedW3C(server string) string {
	var b strings.Builder
	b.WriteString("#Software: Microsoft Internet Information Services 10.0\n#Version: 1.0\n#Date: 2001-02-03 04:05:00\n")
	b.WriteString("#Fields: date time s-sitename s-computername s-ip cs-method cs-uri-stem sc-status time-taken\n")
	for i := 0; i < 5; i++ {
		fmt.Fprintf(&b, "2001-02-03 04:05:%02d W3SVC1 %s 192.0.2.10 GET /api/v1/orders/%d 200 %d\n", i, server, 1000+i, 10+i)
	}
	return b.String()
}

func TestImportW3CUnlistedNameUsesPickedHost(t *testing.T) {
	e := newIntEnv(t, intRepo())
	id := e.newCase("Unlisted server")
	body := unlistedW3C("ISS-PROD-07")

	// preview and commit both take the file under the picked computer
	if res := e.upload("/api/cases/"+id+"/imports?preview=1&importer=iis-w3c&host=web-a", nil, &upload{"u_ex010203.log", body}); res.Code != http.StatusOK {
		t.Fatalf("preview = %d %s", res.Code, res.Body)
	}
	res := e.upload("/api/cases/"+id+"/imports?importer=iis-w3c&host=web-a", ackFields, &upload{"u_ex010203.log", body})
	if res.Code != http.StatusCreated {
		t.Fatalf("commit = %d %s", res.Code, res.Body)
	}
	imports := e.importsOf(id)
	if len(imports) != 1 || imports[0].Importer != "iis-w3c" || imports[0].HostLabel != "web-a" || imports[0].Rows != 5 || imports[0].Rejected != 0 {
		t.Fatalf("imports = %+v", imports)
	}
	// the stored record names the picked computer, never the server name in the file
	if rec := e.importsOf(id)[0]; strings.Contains(fmt.Sprintf("%+v", rec), "ISS-PROD-07") {
		t.Errorf("record carries the server name: %+v", rec)
	}
}

func TestImportW3CUnlistedNameWithoutPickedHostRefused(t *testing.T) {
	e := newIntEnv(t, intRepo())
	id := e.newCase("Unlisted server, no pick")
	res := e.upload("/api/cases/"+id+"/imports?importer=iis-w3c", ackFields, &upload{"u_ex010203.log", unlistedW3C("ISS-PROD-07")})
	if res.Code != http.StatusUnprocessableEntity || failureOf(t, res).Reason != "unknown-host" {
		t.Fatalf("no pick = %d %s", res.Code, res.Body)
	}
	if n := len(e.importsOf(id)); n != 0 {
		t.Fatalf("a refused file left %d imports", n)
	}
	// a pick that is not in the host list is refused up front, as before
	res = e.upload("/api/cases/"+id+"/imports?importer=iis-w3c&host=nowhere", ackFields, &upload{"u_ex010203.log", unlistedW3C("ISS-PROD-07")})
	if res.Code != http.StatusUnprocessableEntity || failureOf(t, res).Reason != "unknown-host" {
		t.Fatalf("pick not listed = %d %s", res.Code, res.Body)
	}
}

func TestImportW3CListedNameKeepsItsOwnLabel(t *testing.T) {
	e := newIntEnv(t, intRepo())
	id := e.newCase("Listed server")
	// the file names a listed server (by alias): it imports as before, picked or not
	res := e.upload("/api/cases/"+id+"/imports?importer=iis-w3c&host=web-a", ackFields, &upload{"u_ex010203.log", unlistedW3C("web-a.example.test")})
	if res.Code != http.StatusCreated {
		t.Fatalf("listed = %d %s", res.Code, res.Body)
	}
	if imports := e.importsOf(id); len(imports) != 1 || imports[0].HostLabel != "web-a" || imports[0].Rows != 5 {
		t.Fatalf("imports = %+v", imports)
	}
	// and with no pick at all, as before
	id = e.newCase("Listed server, no pick")
	res = e.upload("/api/cases/"+id+"/imports?importer=iis-w3c", ackFields, &upload{"u_ex010203.log", unlistedW3C("WEB-A")})
	if res.Code != http.StatusCreated {
		t.Fatalf("listed, no pick = %d %s", res.Code, res.Body)
	}
}

// The record names the computer the rows really went to: one the file named,
// even when the person picked another one for the file.
func TestImportRecordShowsTheHostTheRowsWentTo(t *testing.T) {
	e := newIntEnv(t, intRepo())
	id := e.newCase("Rows went elsewhere")
	// the test host list carries web-a and mongo-a (intHostsDoc)
	res := e.upload("/api/cases/"+id+"/imports?importer=iis-w3c&host=web-a", ackFields, &upload{"u_ex010203.log", unlistedW3C("mongo-a")})
	if res.Code != http.StatusCreated {
		t.Fatalf("commit = %d %s", res.Code, res.Body)
	}
	if imports := e.importsOf(id); len(imports) != 1 || imports[0].HostLabel != "mongo-a" {
		t.Fatalf("imports = %+v, host label want mongo-a, never the picked web-a", imports)
	}
}

// A file that names no computer is recorded under the computer the person
// picked, as before.
func TestImportRecordKeepsThePickedHostWithoutAName(t *testing.T) {
	e := newIntEnv(t, intRepo())
	id := e.newCase("No server in the file")
	// the HTTP.sys error log carries no computer-name column at all
	res := e.upload("/api/cases/"+id+"/imports?importer=iis-httperr&host=web-a", ackFields, &upload{intFileName, intHTTPERR})
	if res.Code != http.StatusCreated {
		t.Fatalf("commit = %d %s", res.Code, res.Body)
	}
	if imports := e.importsOf(id); len(imports) != 1 || imports[0].HostLabel != "web-a" {
		t.Fatalf("imports = %+v, want the picked label web-a", imports)
	}
}

// DerivedHostLabel decides the recorded label from the report: one label wins,
// several name none, none keeps the picked one.
func TestImportMetaDerivedHostLabel(t *testing.T) {
	m := casestore.ImportMeta{HostLabel: "web-a"}
	if got := m.DerivedHostLabel(importer.Report{HostLabels: []string{"web-b"}}); got != "web-b" {
		t.Errorf("one label = %q, want web-b", got)
	}
	if got := m.DerivedHostLabel(importer.Report{HostLabels: []string{"web-a", "web-b"}}); got != "" {
		t.Errorf("two labels = %q, want none", got)
	}
	if got := m.DerivedHostLabel(importer.Report{}); got != "web-a" {
		t.Errorf("no labels = %q, want the picked web-a", got)
	}
}
