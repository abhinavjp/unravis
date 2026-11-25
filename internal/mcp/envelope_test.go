package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestErrorEnvelopeHasTheShapeOfEveryErrorAnswer(t *testing.T) {
	body := ErrorEnvelope("audit_compare", WirePermissionRequired, "This host was revoked in Unravis.", "Open Unravis > AI access to approve it again.")
	var env map[string]any
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatal(err)
	}
	e, _ := env["error"].(map[string]any)
	if env["schemaVersion"] != float64(SchemaVersion) || env["tool"] != "audit_compare" || env["partial"] != false || e["code"] != WirePermissionRequired ||
		e["message"] != "This host was revoked in Unravis." || e["action"] == "" || e["retryable"] != false {
		t.Fatalf("%s", body)
	}
	// the same shape as an error the tool made itself
	own := encodeError("audit_compare", coded(CodeInvalidArguments, "x"))
	var ownEnv map[string]any
	_ = json.Unmarshal(own, &ownEnv)
	for k := range ownEnv {
		if _, ok := env[k]; !ok {
			t.Errorf("field %q is missing from ErrorEnvelope", k)
		}
	}
}

func TestErrorEnvelopeNeverEchoesAToolNameOrAnUnknownCode(t *testing.T) {
	body := string(ErrorEnvelope("CLIENT-TEXT", "CLIENT-CODE", strings.Repeat("é", 500), ""))
	if strings.Contains(body, "CLIENT-TEXT") || strings.Contains(body, "CLIENT-CODE") {
		t.Errorf("client text is echoed: %.200s", body)
	}
	var env struct {
		Tool  string
		Error struct{ Code, Message string }
	}
	if err := json.Unmarshal([]byte(body), &env); err != nil || env.Tool != "unknown" || env.Error.Code != WireInternal || len(env.Error.Message) > maxErrorMessage {
		t.Errorf("%v %+v", err, env)
	}
}

func TestCallEnvelopeAnswersAnUnknownToolWithoutEchoingIt(t *testing.T) {
	b := NewLocal(nil, "t")
	body, isErr := b.CallEnvelope(context.Background(), "CLIENT-TEXT", nil)
	if !isErr || strings.Contains(string(body), "CLIENT-TEXT") || !strings.Contains(string(body), `"invalid_arguments"`) {
		t.Errorf("%v %s", isErr, body)
	}
	if KnownTool("CLIENT-TEXT") || !KnownTool("audit_compare") {
		t.Error("KnownTool")
	}
}

func TestStrictJSON(t *testing.T) {
	for in, want := range map[string]bool{
		`{"a":1}`: true, `{"a":{"b":1},"c":[{"d":1},{"d":2}]}`: true,
		`{"a":1,"a":2}`: false, `{"a":1,"A":2}`: false, `{"a":{"b":1,"B":2}}`: false, `{"a":1} {}`: false, ``: false, `{`: false,
		"{\"k\":1,\"K\":2}": true, // the Kelvin sign is not a duplicate of k for a reader that folds only ASCII case... see below
	} {
		if in == "{\"k\":1,\"K\":2}" {
			want = false // encoding/json folds the Kelvin sign onto k, so this is a duplicate there
		}
		if got := StrictJSON([]byte(in)); got != want {
			t.Errorf("%q: %v", in, got)
		}
	}
}
