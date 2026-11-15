package blackbox

import "testing"

// W1-03: the resolver matches pool and service names with EqualFold, so config
// validation must judge duplicates the same way. Two spellings of one Windows
// target used to pass as distinct and resolve to the same process twice.
func TestConfigRejectsCaseAliasTargets(t *testing.T) {
	base := Default()
	base.Name = "srv-01"
	for _, tc := range []struct {
		name    string
		targets []Target
		wantErr bool
	}{
		{"service case alias", []Target{{Kind: "service", Name: "EmailSvc"}, {Kind: "service", Name: "emailsvc"}}, true},
		{"pool case alias", []Target{{Kind: "appPool", Name: "Payroll"}, {Kind: "appPool", Name: "PAYROLL"}}, true},
		{"exact duplicate", []Target{{Kind: "service", Name: "EmailSvc"}, {Kind: "service", Name: "EmailSvc"}}, true},
		{"same name different kind", []Target{{Kind: "service", Name: "Payroll"}, {Kind: "appPool", Name: "payroll"}}, false},
		{"distinct names", []Target{{Kind: "service", Name: "EmailSvc"}, {Kind: "service", Name: "PayrollSvc"}}, false},
	} {
		cfg := base
		cfg.Targets = tc.targets
		err := cfg.Validate()
		if tc.wantErr && err == nil {
			t.Errorf("%s: accepted, want a duplicate-target refusal", tc.name)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("%s: refused (%v), want accepted", tc.name, err)
		}
	}
}
