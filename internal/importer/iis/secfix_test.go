package iis

import (
	"errors"
	"strings"
	"testing"

	"api-audit-explorer/internal/importer"
)

// secfix L5: "#" directive lines count toward MaxRows, so a file made only of
// them is refused with the row limit instead of running to MaxBytes.
func TestDirectiveLinesCountTowardRowLimit(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("#Software: Test\n#Fields: date time cs-method\n")
	for i := 0; i < 50; i++ {
		sb.WriteString("#Remark: synthetic\n")
	}
	o := baseOpts()
	o.Limits = importer.Limits{MaxRows: 20}
	for _, imp := range []importer.Importer{NewW3C(), NewHTTPERR()} {
		_, _, err := parse(t, imp, []byte(sb.String()), o)
		var le *importer.LimitError
		if !errors.As(err, &le) || le.Limit != "rows" {
			t.Errorf("%T: err = %v, want a rows LimitError", imp, err)
		}
	}
}

// A normal file with a few directive lines is unaffected.
func TestDirectiveLinesWithinLimitStillParse(t *testing.T) {
	o := baseOpts()
	o.Limits = importer.Limits{MaxRows: 100}
	mustParse(t, NewW3C(), fixture(t, "w3c_good.log"), o)
}
