package blackbox

import (
	"strings"
	"testing"
	"time"
)

var dn = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

func dline(ago time.Duration, rest string) string {
	return dn.Add(-ago).Format("2006-01-02T15:04:05.000Z") + " " + rest + "\n"
}

func has(rep DiagReport, code string) bool {
	for _, f := range rep.Findings {
		if f.Code == code {
			return true
		}
	}
	return false
}

func TestDiagnoseNoLog(t *testing.T) {
	rep := DiagnoseLate("", dn, 5)
	if !has(rep, "no-log") {
		t.Fatalf("%+v", rep)
	}
}

func TestDiagnoseHealthy(t *testing.T) {
	log := dline(5*time.Minute, "beat rounds=60 avg=120 max=300 lagmax=20 missed=0 errs=0 hkmax=10 appendmax=8 suppressed=0 slow=- interval=5 effective=5 free=40.0")
	rep := DiagnoseLate(log, dn, 5)
	if !has(rep, "healthy") || !strings.Contains(rep.Headline, "healthy") {
		t.Fatalf("%+v", rep)
	}
}

func TestDiagnoseHostSlow(t *testing.T) {
	log := dline(3*time.Minute, "round seq=9 total=2100 lag=0 missed=0 hk=0 feed=1 collect=2100 host=2000 append=0 groups=- err=host-slow paused=false")
	rep := DiagnoseLate(log, dn, 5)
	if !has(rep, "host-slow") || rep.Findings[0].Code != "host-slow" {
		t.Fatalf("%+v", rep)
	}
}

func TestDiagnoseSlowGroupFromBeatAndRound(t *testing.T) {
	log := dline(8*time.Minute, "beat rounds=60 avg=900 max=1900 lagmax=0 missed=0 errs=0 hkmax=0 appendmax=0 suppressed=0 slow=iis:1000ms:x12 interval=5 effective=5") +
		dline(2*time.Minute, "round seq=3 total=1500 lag=0 missed=0 hk=0 feed=0 collect=1500 host=100 append=1 groups=sqlHost:1000:timeout err=- paused=false")
	rep := DiagnoseLate(log, dn, 5)
	if !has(rep, "group-slow:iis") || !has(rep, "group-slow:sqlHost") {
		t.Fatalf("%+v", rep)
	}
}

func TestDiagnoseWriteSlowCleanupSlowCpuStarved(t *testing.T) {
	log := dline(time.Minute, "beat rounds=50 avg=200 max=800 lagmax=4500 missed=3 errs=0 hkmax=2500 appendmax=1800 suppressed=0 slow=- interval=15 effective=15")
	rep := DiagnoseLate(log, dn, 15)
	for _, c := range []string{"write-slow", "cleanup-slow", "cpu-starved", "missed"} {
		if !has(rep, c) {
			t.Fatalf("missing %s: %+v", c, rep)
		}
	}
}

func TestDiagnoseSelfThrottleIsNotAFault(t *testing.T) {
	log := dline(4*time.Minute, "budget window=over reason=cpu cpu=2.1 heapMB=12.0 privateMB=60.0 action=widen from=5 to=15") +
		dline(time.Minute, "beat rounds=20 avg=100 max=200 lagmax=0 missed=0 errs=0 hkmax=0 appendmax=0 suppressed=0 slow=- interval=5 effective=15")
	rep := DiagnoseLate(log, dn, 5)
	if !has(rep, "self-throttle") || !strings.Contains(rep.Findings[0].Detail, "too much CPU") {
		t.Fatalf("%+v", rep)
	}
}

func TestDiagnoseLogStopped(t *testing.T) {
	log := dline(40*time.Minute, "beat rounds=60 avg=100 max=200 lagmax=0 missed=0 errs=0 hkmax=0 appendmax=0 suppressed=0 slow=- interval=5 effective=5")
	rep := DiagnoseLate(log, dn, 5)
	if rep.Findings[0].Code != "log-stopped" {
		t.Fatalf("%+v", rep)
	}
}

func TestDiagnoseTooMuchWork(t *testing.T) {
	log := dline(time.Minute, "beat rounds=20 avg=4500 max=5200 lagmax=0 missed=0 errs=0 hkmax=0 appendmax=0 suppressed=0 slow=- interval=5 effective=5")
	if rep := DiagnoseLate(log, dn, 5); !has(rep, "too-much-work") {
		t.Fatalf("%+v", rep)
	}
}

func TestFormatDiagTextCapsLines(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 500; i++ {
		b.WriteString(dline(time.Duration(500-i)*time.Second, "round seq=1"))
	}
	txt := FormatDiagText([]string{"State: Running late"}, DiagnoseLate(b.String(), dn, 5), b.String(), 100)
	if n := strings.Count(txt, " round seq=1"); n != 100 {
		t.Fatalf("%d lines copied, want 100", n)
	}
	if !strings.Contains(txt, "Likely cause:") || !strings.Contains(txt, "State: Running late") {
		t.Fatal(txt)
	}
}
