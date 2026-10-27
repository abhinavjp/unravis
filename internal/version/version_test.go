package version

import (
	"strings"
	"testing"
)

func stamp(t *testing.T, version, commit, date string) {
	t.Helper()
	oldVersion, oldCommit, oldDate := Version, Commit, Date
	t.Cleanup(func() { Version, Commit, Date = oldVersion, oldCommit, oldDate })
	Version, Commit, Date = version, commit, date
}

func TestDefaultBuildSaysDev(t *testing.T) {
	stamp(t, "dev", "", "")
	if got := String(); got != "Unravis dev" {
		t.Fatalf("String() = %q", got)
	}
	if got := Short(); got != "Unravis dev" {
		t.Fatalf("Short() = %q", got)
	}
}

func TestStampedBuildNamesVersionCommitAndDate(t *testing.T) {
	stamp(t, "1.2.3", "abc1234def5678", "2026-10-27")
	if got := String(); got != "Unravis v1.2.3 (commit abc1234, built 2026-10-27)" {
		t.Fatalf("String() = %q", got)
	}
	if got := Short(); got != "Unravis v1.2.3" {
		t.Fatalf("Short() = %q", got)
	}
}

func TestVersionAlreadyStartingWithVIsNotDoubled(t *testing.T) {
	stamp(t, "v1.2.3-4-gabc1234-dirty", "abc1234", "2026-10-27")
	if got := Short(); got != "Unravis v1.2.3-4-gabc1234-dirty" {
		t.Fatalf("Short() = %q", got)
	}
}

func TestNonNumericVersionsAreShownAsWritten(t *testing.T) {
	stamp(t, "ci-abc1234", "", "")
	if got := String(); got != "Unravis ci-abc1234" {
		t.Fatalf("String() = %q", got)
	}
}

func TestNoEmptyBrackets(t *testing.T) {
	for _, c := range [][3]string{{"1.0.0", "", ""}, {"", "", ""}, {"  ", " ", " "}, {"dev", "", "2026-10-27"}, {"dev", "abc", ""}} {
		stamp(t, c[0], c[1], c[2])
		got := String()
		for _, bad := range []string{"()", "( )", "(, ", ", )", "commit )", "commit ,", "built )"} {
			if strings.Contains(got, bad) {
				t.Errorf("String() for %q = %q has %q", c, got, bad)
			}
		}
	}
	stamp(t, "dev", "", "2026-10-27")
	if got := String(); got != "Unravis dev (built 2026-10-27)" {
		t.Fatalf("String() = %q", got)
	}
	stamp(t, "dev", "abc", "")
	if got := String(); got != "Unravis dev (commit abc)" {
		t.Fatalf("String() = %q", got)
	}
}
