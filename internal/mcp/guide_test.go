package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
)

// BL-349: the guide is a constant string. Its hash is pinned, so a change shows in review, and it holds no tenant text.
func TestGuideGolden(t *testing.T) {
	sum := sha256.Sum256([]byte(investigationGuide))
	got := hex.EncodeToString(sum[:])
	path := "testdata/guide.sha256"
	if *updateGolden {
		if err := os.WriteFile(path, []byte(got+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(want)) != got {
		t.Fatalf("the guide changed (sha256 %s); review it and update the golden with: go test ./internal/mcp -run TestGuideGolden -update", got)
	}
}

func TestGuideNoTenantText(t *testing.T) {
	if m := regexp.MustCompile(`[0-9a-fA-F]{24}`).FindString(investigationGuide); m != "" {
		t.Errorf("the guide holds a 24-hex id: %q", m)
	}
	if m := regexp.MustCompile(`ev1_[a-z2-7]{26}`).FindString(investigationGuide); m != "" {
		t.Errorf("the guide holds an alias: %q", m)
	}
	srcs, err := audit.NewDemo(time.Now()).Sources(context.Background())
	if err != nil || len(srcs) == 0 {
		t.Fatal(err)
	}
	for _, s := range srcs {
		for _, text := range []string{s.ID, s.Label, s.Database, s.Collection} {
			if len(text) >= 4 && text != "audit" && strings.Contains(strings.ToLower(investigationGuide), strings.ToLower(text)) {
				t.Errorf("the guide names the source text %q", text)
			}
		}
	}
	// The guide tells the model to cite the reference, and mentions the raw id only as present when allowed.
	for _, want := range []string{"evidenceRef", "only when a row carries it"} {
		if !strings.Contains(investigationGuide, want) {
			t.Errorf("the guide lacks %q", want)
		}
	}
	if strings.Contains(investigationGuide, "Every finding must cite record ids") {
		t.Error("the guide still tells the model to cite raw record ids")
	}
}
