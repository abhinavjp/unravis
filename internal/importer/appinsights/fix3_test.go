package appinsights

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"strings"
	"testing"

	"api-audit-explorer/internal/evidence"
	"api-audit-explorer/internal/importer"
)

// csvRow builds one CSV row for the documented header (hdr) with correct
// quoting, so a name with commas or quotes stays one cell.
func csvRow(itemType, name, depType string) string {
	var b bytes.Buffer
	w := csv.NewWriter(&b)
	_ = w.Write([]string{"2001-02-03T04:05:06Z", itemType, name, "10", "200", "True", "1", "op-1", "WEB-A", depType, "x"})
	w.Flush()
	return b.String()
}

func nameOf(t *testing.T, itemType, name, depType string) (string, bool) {
	t.Helper()
	ev, _, err := parse(hdr+csvRow(itemType, name, depType), opts())
	if err != nil || len(ev) != 1 {
		t.Fatalf("%s %q: err %v events %d", itemType, name, err, len(ev))
	}
	n, has := ev[0].Attrs["name"]
	return n, has
}

// M1: without a host map, two machines in one file must not be merged into the
// picked host.
func TestNoHostMapManyInstancesRefused(t *testing.T) {
	o := opts()
	o.Hosts = nil
	o.Host = &importer.HostHint{Label: "web-a"}
	two := hdr +
		"2001-02-03T04:05:06Z,request,GET /a,10,200,True,1,op,web-a-1,,x\n" +
		"2001-02-03T04:05:07Z,request,GET /a,10,200,True,1,op,web-a-2,,x\n"
	if _, _, err := parse(two, o); !errors.Is(err, importer.ErrUnknownHost) {
		t.Fatalf("two instances without a host map: %v", err)
	} else if strings.Contains(err.Error(), "web-a-2") {
		t.Fatalf("raw instance name in error: %v", err)
	}
	// The same instance in a different case is one machine.
	same := hdr +
		"2001-02-03T04:05:06Z,request,GET /a,10,200,True,1,op,WEB-A-1,,x\n" +
		"2001-02-03T04:05:07Z,request,GET /a,10,200,True,1,op,web-a-1,,x\n"
	ev, _, err := parse(same, o)
	if err != nil || len(ev) != 2 || ev[0].Host != "web-a" || ev[1].Host != "web-a" {
		t.Fatalf("one instance in two cases: %v %+v", err, ev)
	}
	// With a host map each instance keeps its own label.
	o.Hosts = func(n string) (importer.HostHint, bool) {
		return importer.HostHint{Label: strings.ToLower(n)}, true
	}
	ev, _, err = parse(two, o)
	if err != nil || len(ev) != 2 || ev[0].Host == ev[1].Host {
		t.Fatalf("mapped instances: %v %+v", err, ev)
	}
}

// M3: names are kept only in shapes that carry no identifier or query text.
func TestNameScrubsIdentifiers(t *testing.T) {
	cases := []struct {
		kind, typ, in string
		want          string // "" means the attribute is absent
	}{
		{"request", "", "GET /users/alice@example.test/profile", "GET /users/{id}/profile"},
		{"request", "", "GET /orders/507f1f77bcf86cd799439011", "GET /orders/{id}"},
		{"request", "", "GET /cart;jsessionid=ABCDEF0123456789/view", "GET /cart/view"},
		{"request", "", "GET /reset/eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ4In0.c2ln", "GET /reset/{id}"},
		{"request", "", "GET /files/report.final.pdf", "GET /files/{id}"},
		{"request", "", "GET /orders/12345?token=" + sentinel, "GET /orders/{id}"},
		{"request", "", "POST /orders/3fa85f64-5717-4562-b3fc-2c963f66afa6", "POST /orders/{id}"},
		{"request", "", "GET /v1/ping", "GET /v1/ping"},
		{"request", "", "/health", "/health"},
		{"request", "", "ProcessQueue", "ProcessQueue"},
		{"request", "", "SELECT * FROM Customers WHERE Email='bob@example.test'", ""},
		{"request", "", "=SUM(1+1)", ""},
		{"request", "", "Order for Jane Smith", ""},
		{"dependency", "SQL", "sqlprod01.corp.example | CustomerDb", ""},
		{"dependency", "SQL", "SELECT * FROM Customers WHERE Email='bob@example.test'", ""},
		{"dependency", "", "SQL: sql-a.example.test | db-alpha", ""},
		{"dependency", "Http", "GET /v1/ping?key=" + sentinel, "GET /v1/ping"},
		{"dependency", "Http", "POST /users/bob@example.test", "POST /users/{id}"},
		{"dependency", "Http", "GET https://other.example.test/x", ""},
		{"dependency", "Azure Service Bus", "GET /v1/ping", ""},
		{"pageview", "", "Welcome bob@example.test", ""},
		{"customEvent", "", "OrderPlaced", "OrderPlaced"},
	}
	for _, c := range cases {
		got, has := nameOf(t, c.kind, c.in, c.typ)
		if got != c.want || has != (c.want != "") {
			t.Errorf("%s/%s %q: got %q (present %v) want %q", c.kind, c.typ, c.in, got, has, c.want)
		}
		if strings.Contains(got, sentinel) || strings.Contains(got, "@") {
			t.Errorf("%q leaked: %q", c.in, got)
		}
	}
}

func TestNameLengthCap(t *testing.T) {
	got, _ := nameOf(t, "request", "GET "+strings.Repeat("/abc", 200), "")
	if len(got) == 0 || len(got) > maxNameBytes {
		t.Fatalf("name length %d", len(got))
	}
}

// M2: every fixed sentence passes the causal-wording lint.
func TestFixedSentencesPassCausalLint(t *testing.T) {
	sentences := []string{TimestampHeader, DurationLabel, UseDocumentedQuery, GapSampled, GapTimestampStart,
		GapDuration, GapMissingRecord, GapNoItemCount, GapNoHost, GapEmpty,
		Absence(0), Absence(3), evidence.NotObservedGap(GapEmpty)}
	_, rep, err := parse(hdr+goodRows(3), opts())
	if err != nil {
		t.Fatal(err)
	}
	sentences = append(sentences, rep.KnownGaps...)
	sentences = append(sentences, rep.Quality.Sampling)
	_, rep, err = parse("timestamp [UTC],itemType\n2001-02-03T04:05:06Z,request\n", opts())
	if err != nil {
		t.Fatal(err)
	}
	sentences = append(sentences, rep.KnownGaps...)
	for _, s := range sentences {
		if err := evidence.LintCausal(s); err != nil {
			t.Errorf("%q: %v", s, err)
		}
	}
}

// Low 9: a direct Parse call is capped and archive-sniffed on its own, without
// relying on importer.Prepare.
func TestDirectParseSniffsAndCaps(t *testing.T) {
	emit := func(evidence.Event) error { return nil }
	_, err := New().Parse(context.Background(), strings.NewReader("PK\x03\x04"+hdr+goodRows(3)), opts(), emit)
	var qe *importer.QuarantineError
	if !errors.As(err, &qe) || qe.Reason != importer.ReasonArchive {
		t.Fatalf("archive: %v", err)
	}
	o := opts()
	o.Limits = importer.Limits{MaxBytes: 200}
	_, err = New().Parse(context.Background(), strings.NewReader(hdr+goodRows(40)), o, emit)
	var le *importer.LimitError
	if !errors.As(err, &le) || le.Limit != "bytes" {
		t.Fatalf("byte cap: %v", err)
	}
	// A normal file still parses through the same path.
	rep, err := New().Parse(context.Background(), strings.NewReader(hdr+goodRows(3)), opts(), emit)
	if err != nil || rep.Emitted != 3 {
		t.Fatalf("plain file: %v %+v", err, rep)
	}
}
