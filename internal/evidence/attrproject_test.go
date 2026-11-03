package evidence

import "testing"

func sixAttrs() map[string]string {
	return map[string]string{
		"client-ip": "CANARY_IP", "referer": "CANARY_REF", "cookie:sid": "CANARY_CK",
		"cookie:Other_1": "CANARY_CK2", "status": "200", "method": "GET",
	}
}

func TestProjectEventAttrsDropsSensitiveByDefault(t *testing.T) {
	got := ProjectEventAttrs(sixAttrs(), false)
	if len(got) != 2 || got["status"] != "200" || got["method"] != "GET" {
		t.Fatalf("default projection = %v", got)
	}
}

func TestProjectEventAttrsOptInKeepsAllAndCopies(t *testing.T) {
	in := sixAttrs()
	all := ProjectEventAttrs(in, true)
	if len(all) != 6 {
		t.Fatalf("opt-in kept %d pairs, want 6", len(all))
	}
	for k, v := range in {
		if all[k] != v {
			t.Fatalf("value of %q changed: %q", k, all[k])
		}
	}
	all["status"] = "mutated"
	all["new"] = "x"
	safe := ProjectEventAttrs(in, false)
	safe["status"] = "mutated"
	safe["new"] = "x"
	if in["status"] != "200" || len(in) != 6 {
		t.Fatalf("input map was changed: %v", in)
	}
}

func TestProjectEventAttrsNilAndPrefixEdges(t *testing.T) {
	for _, inc := range []bool{false, true} {
		if got := ProjectEventAttrs(nil, inc); got == nil || len(got) != 0 {
			t.Fatalf("nil input (include=%v) = %#v", inc, got)
		}
	}
	for _, k := range []string{"cookie", "cookies:x", "x-cookie:y", "Client-IP"} {
		if IsSensitiveAttr(k) {
			t.Errorf("%q should not be sensitive", k)
		}
		if got := ProjectEventAttrs(map[string]string{k: "v"}, false); got[k] != "v" {
			t.Errorf("%q was dropped", k)
		}
	}
	for _, k := range []string{"cookie:", "cookie:sid", "client-ip", "referer"} {
		if !IsSensitiveAttr(k) {
			t.Errorf("%q should be sensitive", k)
		}
	}
	if got := ProjectEventAttrs(map[string]string{"cookie:": "v"}, false); len(got) != 0 {
		t.Errorf("empty cookie name kept: %v", got)
	}
}
