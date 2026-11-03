package setup

import (
	"errors"
	"testing"
)

func TestNewFakeSystemLeavesAskQuitNil(t *testing.T) {
	s, _ := NewFakeSystem("1.0.0")
	if s.AskQuit != nil {
		t.Error("a new fake system has AskQuit set")
	}
}

func TestUseAskQuitCountsCalls(t *testing.T) {
	s, f := NewFakeSystem("1.0.0")
	f.UseAskQuit(s)
	for i := 0; i < 3; i++ {
		if err := s.AskQuit(); err != nil {
			t.Fatal(err)
		}
	}
	if f.AskQuitCalls != 3 {
		t.Errorf("AskQuitCalls = %d, want 3", f.AskQuitCalls)
	}
}

func TestUseAskQuitReturnsTheConfiguredError(t *testing.T) {
	s, f := NewFakeSystem("1.0.0")
	f.UseAskQuit(s)
	want := errors.New("no reply")
	f.AskQuitErr = want
	f.Proc.List_ = []Process{{PID: 1, Exe: `C:\u\unravis.exe`, SID: f.UserSID}}
	f.QuitRemovesApps = true
	if err := s.AskQuit(); !errors.Is(err, want) {
		t.Errorf("AskQuit = %v, want %v", err, want)
	}
	if f.AskQuitCalls != 1 || len(f.Proc.List_) != 1 {
		t.Errorf("calls %d, processes %d: a failed ask must not end anything", f.AskQuitCalls, len(f.Proc.List_))
	}
}

func TestUseAskQuitCanRemoveOnlyMyProcesses(t *testing.T) {
	s, f := NewFakeSystem("1.0.0")
	f.UseAskQuit(s)
	f.Proc.List_ = []Process{
		{PID: 1, Exe: `C:\u\unravis.exe`, SID: f.UserSID},
		{PID: 2, Exe: `C:\u\unravis.exe`, SID: "S-1-5-21-2000"},
		{PID: 3, Exe: `C:\u\unravis.exe`},
		{PID: 4, Exe: `C:\u\unravis.exe`, SID: UnknownOwner},
	}
	if err := s.AskQuit(); err != nil {
		t.Fatal(err)
	}
	if len(f.Proc.List_) != 4 {
		t.Fatalf("without QuitRemovesApps %d processes are left, want 4", len(f.Proc.List_))
	}
	f.QuitRemovesApps = true
	if err := s.AskQuit(); err != nil {
		t.Fatal(err)
	}
	var left []uint32
	for _, q := range f.Proc.List_ {
		left = append(left, q.PID)
	}
	if len(left) != 2 || left[0] != 2 || left[1] != 4 {
		t.Errorf("processes left %v, want the other users' 2 and 4", left)
	}
}

func TestFakeTerminateIfChecksTheExe(t *testing.T) {
	_, f := NewFakeSystem("1.0.0")
	pid := f.Proc.Run(`C:\u\unravis.exe`)
	if err := f.Proc.TerminateIf(pid, `C:\other\unravis.exe`); !errors.Is(err, ErrNotFound) {
		t.Fatalf("TerminateIf with another exe = %v, want ErrNotFound", err)
	}
	if len(f.Proc.List_) != 1 {
		t.Fatal("the process was removed")
	}
	if err := f.Proc.TerminateIf(pid, `C:\U\Unravis.exe`); err != nil || len(f.Proc.List_) != 0 {
		t.Errorf("TerminateIf with the same exe = %v, %d left", err, len(f.Proc.List_))
	}
	if err := f.Proc.TerminateIf(pid, `C:\u\unravis.exe`); !errors.Is(err, ErrNotFound) {
		t.Errorf("TerminateIf on a gone process = %v", err)
	}
}
