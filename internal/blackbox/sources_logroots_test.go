package blackbox

import (
	"fmt"
	"reflect"
	"testing"
	"time"
)

func TestMergeLogRoots(t *testing.T) {
	cases := []struct {
		name       string
		conf, find []string
		want       []string
	}{
		{"adds a new folder", []string{`C:\a`}, []string{`E:\Logs\B\W3SVC2`}, []string{`C:\a`, `E:\Logs\B\W3SVC2`}},                                          // winok: pure string logic and an in-memory fake machine, no real path is touched
		{"same folder, other case and slash", []string{`C:\Logs\`}, []string{`c:\logs`}, []string{`C:\Logs\`}},                                               // winok: pure string logic and an in-memory fake machine, no real path is touched
		{"below a configured root", []string{`C:\inetpub\logs\LogFiles`}, []string{`C:\inetpub\logs\LogFiles\W3SVC1`}, []string{`C:\inetpub\logs\LogFiles`}}, // winok: pure string logic and an in-memory fake machine, no real path is touched
		{"a longer name is not below", []string{`C:\Logs`}, []string{`C:\Logs2\W3SVC1`}, []string{`C:\Logs`, `C:\Logs2\W3SVC1`}},                             // winok: pure string logic and an in-memory fake machine, no real path is touched
		{"blank skipped, repeated found added once", nil, []string{"", `D:\x`, `D:\X`}, []string{`D:\x`}},
	}
	for _, c := range cases {
		got := MergeLogRoots(c.conf, c.find)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestMergeLogRootsCapAndNoMutation(t *testing.T) {
	conf := make([]string, 0, maxLogPaths+5)
	for i := 0; i < maxLogPaths-1; i++ {
		conf = append(conf, fmt.Sprintf(`C:\c%d`, i))
	}
	got := MergeLogRoots(conf, []string{`E:\one`, `E:\two`, `E:\three`}) // winok: pure string logic and an in-memory fake machine, no real path is touched
	if len(got) != maxLogPaths || got[len(got)-1] != `E:\one` {          // winok: pure string logic and an in-memory fake machine, no real path is touched
		t.Fatalf("cap: %d %v", len(got), got[len(got)-3:])
	}
	if len(conf) != maxLogPaths-1 || conf[:cap(conf)][maxLogPaths-1] != "" {
		t.Fatalf("configured slice was touched")
	}
}

func TestKeeperLogRootsIISOnly(t *testing.T) {
	m := newMachine()
	m.iis(iisXML(``, `<site name="A" id="1"/><site name="B" id="2"><logFile directory="E:\Logs\B"/></site><site name="C" id="3"/>`, ``)) // winok: pure string logic and an in-memory fake machine, no real path is touched
	m.addDir(`C:\inetpub\logs\LogFiles\W3SVC1`, fFile("u_ex1.log", 1, 1))                                                                // winok: pure string logic and an in-memory fake machine, no real path is touched
	m.addDir(`E:\Logs\B\W3SVC2`, fFile("u_ex2.log", 1, 1))                                                                               // winok: pure string logic and an in-memory fake machine, no real path is touched
	// Site 3's folder is missing; HTTPERR exists but is not an IIS site.
	m.addDir(`C:\Windows\System32\LogFiles\HTTPERR`, fFile("httperr1.log", 1, 1))                                                             // winok: pure string logic and an in-memory fake machine, no real path is touched
	k := NewSourcesKeeper(t.TempDir(), Config{Folder: `D:\UnravisBlackbox`}, m.env(), "HOST1", "1", func() time.Time { return testNow }, nil) // winok: pure string logic and an in-memory fake machine, no real path is touched
	if got := k.LogRoots(); got != nil {
		t.Fatalf("before the first look: %v", got)
	}
	k.Refresh()
	want := []string{`C:\inetpub\logs\LogFiles\W3SVC1`, `E:\Logs\B\W3SVC2`} // winok: pure string logic and an in-memory fake machine, no real path is touched
	if got := k.LogRoots(); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}
