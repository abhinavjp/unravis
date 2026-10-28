package keeper

import "testing"

func TestServiceSID(t *testing.T) {
	// Windows' own NT SERVICE\TrustedInstaller SID is the known vector for the service SID rule.
	if got, want := ServiceSID("TrustedInstaller"), "S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464"; got != want {
		t.Fatalf("ServiceSID = %s, want %s", got, want)
	}
	if ServiceSID("unraviskeeper") != ServiceSID(ServiceName) {
		t.Fatal("the service SID ignores case")
	}
}

func TestTrustedPipeOwner(t *testing.T) {
	for _, sid := range []string{"S-1-5-18", "S-1-5-19", "S-1-5-20", "S-1-5-32-544", ServiceSID(ServiceName)} {
		if !trustedPipeOwner(sid, nil) {
			t.Errorf("%s must be trusted", sid)
		}
	}
	person := "S-1-5-21-1-2-3-1001"
	for _, sid := range []string{"", person, "S-1-5-32-545", "S-1-1-0", "S-1-5-4", ServiceSID("SomeOtherService")} {
		if trustedPipeOwner(sid, nil) {
			t.Errorf("%s must not be trusted", sid)
		}
	}
	if !trustedPipeOwner(person, []string{person}) || trustedPipeOwner("", []string{""}) {
		t.Fatal("extra owners: only a named SID")
	}
}
