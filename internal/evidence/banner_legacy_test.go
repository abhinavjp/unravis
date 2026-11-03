package evidence

import (
	"strings"
	"testing"
)

// The banner was reworded to drop internal R-codes. Acknowledgements made against the earlier wording must still be recognised.
func TestEarlierBannerWordingStillKnown(t *testing.T) {
	const earlier = "Parsers are built and tested on synthetic files only. Importing real client exports needs: R2 preservation, R8 content review, R11 AI/MCP approval, R12 DBA dry run (SQL files), R13 Mongo access, R16 retention."
	if got, ok := KnownBanner(earlier); !ok || got != earlier {
		t.Fatalf("earlier wording not recognised: %q %v", got, ok)
	}
	if strings.Contains(GatingBanner(), "R2 ") || strings.Contains(GatingBanner(), "R16") {
		t.Fatal("the banner shown today must not carry internal R-codes")
	}
	if got, ok := KnownBanner(GatingBanner()); !ok || got != GatingBanner() {
		t.Fatal("current banner not recognised")
	}
}
