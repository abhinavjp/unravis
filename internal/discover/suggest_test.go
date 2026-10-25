package discover

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestSuggestNames(t *testing.T) {
	long := strings.Repeat("a", 60)
	cases := []struct {
		name  string
		paths []string
		want  *Suggestion
	}{
		{"portal and api folders", []string{`D:\Sites\clienta-prod\portal\api-orders\logs\app.log`}, &Suggestion{Portal: "portal", API: "api-orders"}},
		{"iis inetpub", []string{`C:\inetpub\logs\LogFiles\W3SVC2\u_ex010911.log`}, nil},
		{"iis site folders", []string{`D:\inetpub\wwwroot\shop-portal\api-cart\logs\u_ex010911.log`}, &Suggestion{Portal: "shop-portal", API: "api-cart"}},
		{"log4net under an app folder", []string{`D:\Apps\orders-api\logs\app.log`}, &Suggestion{API: "orders-api"}},
		{"user profile never leaks", []string{`C:\Users\Name\AppData\Local\Temp\logs\app.log`}, nil},
		{"user profile with an app", []string{`C:\Users\Name\Documents\my-portal\billing\logs\app.log`}, &Suggestion{Portal: "my-portal", API: "billing"}},
		{"unc path drops server and share", []string{`\\SERVER01\logs$\clienta\portal-x\app.log`}, &Suggestion{Portal: "clienta", API: "portal-x"}},
		{"unc share only", []string{`\\SERVER01\share\app.log`}, nil},
		{"deep path keeps the nearest two", []string{`D:\a1\b2\c3\d4\e5\f6\g7\h8\logs\app.log`}, &Suggestion{Portal: "g7", API: "h8"}},
		{"only noise", []string{`C:\Windows\Temp\logs\app.log`}, nil},
		{"very long name is capped", []string{`D:\` + long + `\app.log`}, &Suggestion{API: strings.Repeat("a", 40)}},
		{"unicode is stripped", []string{`D:\Журналы\billing-ünï\logs\app.log`}, &Suggestion{API: "billing-n"}},
		{"unicode only is skipped", []string{`D:\portal\Журналы\app.log`}, &Suggestion{API: "portal"}},
		{"site ids and target folders are skipped", []string{`D:\Apps\orders-api\bin\Release\net8.0\logs\app.log`}, &Suggestion{API: "orders-api"}},
		{"guid and number folders are skipped", []string{`D:\shop\0f8fad5b-d9cb-469f-a165-70867728950e\12\app.log`}, &Suggestion{API: "shop"}},
		{"credential-like folder is skipped", []string{`D:\portal\password-store\app.log`}, &Suggestion{API: "portal"}},
		{"set in one folder", []string{`D:\portal\api-x\logs\a.log`, `D:\portal\api-x\logs\b.log`}, &Suggestion{Portal: "portal", API: "api-x"}},
		{"set across folders uses the common ancestor", []string{`D:\portal\api-x\logs\a.log`, `D:\portal\api-y\logs\b.log`}, &Suggestion{API: "portal"}},
		{"forward slashes and drive letter", []string{`d:/portal/api-x/logs/a.log`}, &Suggestion{Portal: "portal", API: "api-x"}},
		{"file at a drive root", []string{`D:\app.log`}, nil},
		{"no paths", nil, nil},
		{"empty path", []string{""}, nil},
		{"case of noise ignored", []string{`D:\Portal\API-Orders\LOGS\app.log`}, &Suggestion{Portal: "Portal", API: "API-Orders"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := suggestNames(c.paths)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

// What the scan sends must never carry a drive letter, a profile name or a
// machine name, whatever the path.
func TestSuggestNeverLeaksIdentity(t *testing.T) {
	paths := [][]string{
		{`C:\Users\Name\Documents\logs\app.log`},
		{`C:\Users\Name\app.log`},
		{`C:\Users\Name\clienta\portal\api\app.log`},
		{`\\WEB-A\share\portal\api\app.log`},
		{`\\WEB-A\share`},
		{`\\WEB-A\share\app.log`},
		{`C:\Users`},
		{`C:\Users\`},
	}
	for _, p := range paths {
		s := suggestNames(p)
		b, _ := json.Marshal(s)
		for _, bad := range []string{"Name", "WEB-A", "share", "C:", `\`} {
			if strings.Contains(string(b), bad) {
				t.Errorf("%v: suggestion %s carries %q", p, b, bad)
			}
		}
	}
}

func TestSuggestWordsOnlyHoldSafeCharacters(t *testing.T) {
	s := suggestNames([]string{`D:\por<tal>"|?*\api;rm -rf\app.log`})
	if s == nil {
		t.Fatal("want a suggestion")
	}
	for _, w := range []string{s.Portal, s.API} {
		if strings.ContainsAny(w, `<>"|?*;\/:`) || len(w) > maxSuggestWord {
			t.Errorf("unsafe word %q", w)
		}
	}
}

type suggestDetector struct{ f Finding }

func (suggestDetector) Name() string { return "suggest" }
func (d suggestDetector) Detect(context.Context, Env) ([]Finding, []Gap) {
	return []Finding{d.f}, nil
}

// Run fills Suggest from the paths, and the JSON carries the words and no path.
func TestRunSetsSuggestWithoutLeakingThePath(t *testing.T) {
	f := Finding{Detector: "suggest", Kind: KindLogFile, Importer: "iis-w3c", Display: "x", Shows: "discover.iis.shows"}
	f.SetPaths(`C:\Users\SENTINEL-7f3a\Sites\clienta-prod\portal\api-orders\logs\u_ex010911.log`)
	got := Run(context.Background(), Env{}, []Detector{suggestDetector{f}})
	if len(got.Findings) != 1 {
		t.Fatalf("%+v", got.Findings)
	}
	want := &Suggestion{Portal: "portal", API: "api-orders"}
	if !reflect.DeepEqual(got.Findings[0].Suggest, want) {
		t.Fatalf("suggest %+v, want %+v", got.Findings[0].Suggest, want)
	}
	b, _ := json.Marshal(got)
	if !strings.Contains(string(b), `"suggest":{"portal":"portal","api":"api-orders"}`) {
		t.Errorf("suggest missing from JSON: %s", b)
	}
	for _, bad := range []string{"SENTINEL", "Users", "C:", "clienta"} {
		if strings.Contains(string(b), bad) {
			t.Errorf("JSON carries %q: %s", bad, b)
		}
	}
	// A finding with no files has no suggestion and no key.
	ch := Finding{Detector: "suggest", Kind: KindEventChannel, Display: "System", Shows: "discover.events.shows"}
	got = Run(context.Background(), Env{}, []Detector{suggestDetector{ch}})
	if got.Findings[0].Suggest != nil || strings.Contains(mustJSON(t, got), `"suggest":`) {
		t.Error("a finding without files must carry no suggestion")
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
