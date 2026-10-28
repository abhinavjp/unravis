package server

import (
	"net/http"
	"testing"
	"time"

	"api-audit-explorer/internal/casestore"
)

// F5 of the P3 review and A14: a recording never runs into a case that is being
// archived, and Cancel starts the recording again exactly when it was running.

func (a *archPC) startRecording(id string) {
	a.t.Helper()
	h := a.handler()
	t := a.t
	p := samplerParams{interval: 60, flush: time.Hour, source: &discSampler{}}
	h.disc.smu.Lock()
	f := h.launchSamplerLocked(casestore.CaseID(id), p)
	h.disc.smu.Unlock()
	if f != nil {
		t.Fatalf("recording did not start: %+v", f)
	}
	t.Cleanup(h.stopSamplerAll)
}

func (a *archPC) recording(id string) bool {
	_, running := a.handler().samplerRunningFor(casestore.CaseID(id))
	return running
}

func (a *archPC) tryRecording(id string) *launchFailure {
	h := a.handler()
	h.disc.smu.Lock()
	defer h.disc.smu.Unlock()
	return h.launchSamplerLocked(casestore.CaseID(id), samplerParams{interval: 60, flush: time.Hour, source: &discSampler{}})
}

// A recording cannot start while the case is Archiving or once it is archived.
func TestF5_NoRecordingIntoACaseBeingArchived(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	id := a.seed("recorded", 3)
	p := pauseAt(t, "part-written")
	if res := a.archiveReq(id, a.folder(), "open"); res.Code != http.StatusAccepted {
		t.Fatalf("archive = %d %s", res.Code, res.Body)
	}
	<-p.reached
	f := a.tryRecording(id)
	if f == nil || f.err != casestore.ErrArchiveRunning {
		t.Fatalf("recording while Archiving: %+v, want ErrArchiveRunning", f)
	}
	if a.recording(id) {
		t.Fatal("a recording runs into the case being archived")
	}
	close(p.proceed)
	if j := a.wait(id, "archive"); j.State != fileJobDone {
		t.Fatalf("archive = %+v", j)
	}
	if f := a.tryRecording(id); f == nil || f.err == nil {
		t.Fatalf("recording into an archived case: %+v, want a refusal", f)
	}
	if a.recording(id) {
		t.Fatal("a recording runs into the archived case")
	}
}

// A recording another tab starts after the archive looked and before it stamped
// Archiving is stopped right after the stamp.
func TestF5_RecordingStartedInTheWindowIsStopped(t *testing.T) {
	a := newArchPC(t, cfsPassA)
	id := a.seed("window", 3)
	fired := false
	p := pauseAt(t, "part-written")
	// pauseAt replaced the hook; chain the window hook back in front of it.
	pause := archiveInject
	archiveInject = func(pt, part, final string) bool {
		if pt == "before-begin" && !fired {
			fired = true
			a.startRecording(id)
			return false
		}
		return pause(pt, part, final)
	}
	if res := a.archiveReq(id, a.folder(), "open"); res.Code != http.StatusAccepted {
		t.Fatalf("archive = %d %s", res.Code, res.Body)
	}
	<-p.reached
	if !fired {
		t.Fatal("the window hook never ran")
	}
	if a.recording(id) {
		t.Fatal("the recording started in the window is still running into the case")
	}
	close(p.proceed)
	if j := a.wait(id, "archive"); j.State != fileJobDone {
		t.Fatalf("archive = %+v", j)
	}
	if a.recording(id) {
		t.Fatal("the recording came back after a finished archive")
	}
}

// A14: Cancel starts the recording again when it was running, and does not start one
// that was not.
func TestA14_CancelRestartsTheRecordingOnlyIfItWasRunning(t *testing.T) {
	for _, was := range []bool{true, false} {
		name := "was not recording"
		if was {
			name = "was recording"
		}
		t.Run(name, func(t *testing.T) {
			a := newArchPC(t, cfsPassA)
			id := a.seed("cancelled", 3)
			if was {
				a.startRecording(id)
			}
			p := pauseAt(t, "part-written")
			if res := a.archiveReq(id, a.folder(), "open"); res.Code != http.StatusAccepted {
				t.Fatalf("archive = %d %s", res.Code, res.Body)
			}
			<-p.reached
			if a.recording(id) {
				t.Fatal("still recording while Archiving")
			}
			if res := a.req(http.MethodDelete, "/api/cases/"+id+"/archive", "", "archiving"); res.Code != http.StatusNoContent && res.Code != http.StatusOK && res.Code != http.StatusAccepted {
				t.Fatalf("cancel = %d %s", res.Code, res.Body)
			}
			close(p.proceed)
			if j := a.wait(id, "archive"); j.State == fileJobDone {
				t.Fatalf("the cancelled archive finished: %+v", j)
			}
			if got := a.recording(id); got != was {
				t.Fatalf("recording after Cancel = %v, want %v", got, was)
			}
		})
	}
}
