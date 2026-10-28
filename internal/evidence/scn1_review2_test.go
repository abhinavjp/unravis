package evidence

import (
	"strings"
	"testing"
)

// Review2-1: ledger and case free text carries no secret pair or connection string.
func TestScn1Review2SecretsInFreeText(t *testing.T) {
	for _, tc := range []struct {
		in    string
		leaks []string
	}{
		{"retry with password=hunter2 token=abc123", []string{"hunter2", "abc123"}},
		{"Server=db01;User Id=sa;Password=x", []string{"Password", "User Id", "db01"}},
		{"api_key=zzz9 and pwd='my pass'", []string{"zzz9", "my pass"}},
	} {
		for name, got := range map[string]string{
			"cleanCaseText": cleanCaseText(tc.in, "withheld"),
			"cleanFreeText": cleanFreeText(tc.in, nil, "withheld"),
		} {
			for _, leak := range tc.leaks {
				if strings.Contains(got, leak) {
					t.Errorf("%s(%q) = %q still holds %q", name, tc.in, got, leak)
				}
			}
		}
	}
}
