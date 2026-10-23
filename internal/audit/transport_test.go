package audit

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTransportJSONUsesExplicitCamelCaseNames(t *testing.T) {
	value := struct {
		Query            Query            `json:"query"`
		Source           Source           `json:"source"`
		Input            SourceInput      `json:"input"`
		ConnectionInput  ConnectionInput  `json:"connectionInput"`
		ConnectionResult ConnectionResult `json:"connectionResult"`
		Result           SourceResult     `json:"result"`
		PageInput        PageInput        `json:"pageInput"`
		Record           RequestRecord    `json:"record"`
	}{
		Query:            Query{SourceIDs: []string{"s"}, BucketSeconds: 10},
		Source:           Source{ID: "s", Label: "S", Database: "db", Collection: "requests"},
		Input:            SourceInput{Label: "S", URI: "mongodb://example.invalid/db", Username: "reader", Password: "secret", AuthSource: "admin", TLSMode: "custom", CACertificate: "pem", SavedConnectionID: "vault-id", Database: "db", Collection: "requests"},
		ConnectionInput:  ConnectionInput{URI: "mongodb://example.invalid/db", Username: "reader", Password: "secret", AuthSource: "admin", TLSMode: "custom", CACertificate: "pem", SavedConnectionID: "vault-id"},
		ConnectionResult: ConnectionResult{Databases: []string{"db"}, Warning: "warning"},
		Result:           SourceResult{SourceID: "s", OK: true},
		PageInput:        PageInput{APIKey: "key"},
		Record:           RequestRecord{ID: "r", SourceID: "s", RequestKB: 1, ResponseKB: 2},
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	body := string(encoded)
	for _, field := range []string{`"sourceIds"`, `"bucketSeconds"`, `"baselineStart"`, `"sourceId"`, `"uri"`, `"username"`, `"password"`, `"authSource"`, `"tlsMode"`, `"caCertificate"`, `"savedConnectionId"`, `"databases"`, `"warning"`, `"ok"`, `"apiKey"`, `"requestKB"`, `"responseKB"`} {
		if !strings.Contains(body, field) {
			t.Errorf("JSON contract field %s missing: %s", field, body)
		}
	}
	if strings.Contains(body, `"URI"`) || strings.Contains(body, `"SourceID"`) || strings.Contains(body, `"OK"`) {
		t.Fatalf("Go field names leaked: %s", body)
	}
	var sourceOnly struct {
		Source Source `json:"source"`
	}
	if err := json.Unmarshal(encoded, &sourceOnly); err != nil {
		t.Fatal(err)
	}
	sourceJSON, err := json.Marshal(sourceOnly.Source)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sourceJSON), "uri") {
		t.Fatalf("public Source includes URI: %s", sourceJSON)
	}
}
