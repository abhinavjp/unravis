package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestCheckUpdateFlagStandsAlone(t *testing.T) {
	if f, err := parseFlags([]string{"--check-update"}); err != nil || !f.checkUpdate {
		t.Fatalf("--check-update alone: %+v, %v", f, err)
	}
	for _, a := range [][]string{{"--check-update", "--demo"}, {"--check-update", "--no-browser"}, {"--quit", "--check-update"}} {
		if _, err := parseFlags(a); err == nil || !strings.Contains(err.Error(), "cannot be combined") {
			t.Errorf("%v: want a 'cannot be combined' error, got %v", a, err)
		}
	}
}

// A development build (no version stamped, as in tests) has no channel, so it never calls out and the answer is "dev" with exit 2.
func TestRunCheckUpdateDevBuildMakesNoCall(t *testing.T) {
	var out, errOut bytes.Buffer
	code := runCheckUpdate(&out, &errOut, 5*time.Second)
	if code == 0 && !strings.Contains(out.String(), `"status":"latest"`) && !strings.Contains(out.String(), `"status":"available"`) {
		t.Fatalf("exit 0 without a good answer: %s", out.String())
	}
	if out.Len() > 0 {
		var st map[string]any
		if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &st); err != nil {
			t.Fatalf("output is not one JSON line: %q (%v)", out.String(), err)
		}
		if st["status"] == "dev" && code != 2 {
			t.Fatalf("dev build must exit 2, got %d", code)
		}
	}
}
