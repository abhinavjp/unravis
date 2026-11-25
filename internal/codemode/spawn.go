package codemode

import (
	"context"
	"errors"
	"io"
)

// Sandbox modes a Worker can report (spec 3.7). The second is the degraded fallback: the network is NOT blocked.
const (
	ModeLPAC     = "lpac"
	ModeFallback = "restricted_token_fallback"
)

// Spawner starts the sandbox worker. The production spawner (BL-366, BL-367: job object, handle list, mitigations, LPAC) and the worker
// itself (BL-362) are not built yet, so a Broker without a Spawner refuses every run with sandbox_unavailable. There is no way to run a
// script without a Spawner and nothing in the production build starts an unsandboxed process: tests give the broker a scripted fake that
// speaks the wire protocol and runs no JavaScript.
type Spawner interface {
	// Available says whether Start can work here at all (worker file present and matching its pin, a supported Windows). It must not
	// start anything. An error is the reason, for the log only; the user sees sandbox_unavailable.
	Available() error
	// Start spawns one worker for one run. It hashes the worker file against the pin first.
	Start(ctx context.Context, opts SpawnOptions) (Worker, error)
}

// SpawnOptions are what the broker tells the spawner.
type SpawnOptions struct {
	// AllowDegraded says the user allowed the restricted-token fallback (setting allowDegraded). Without it the spawner must refuse
	// to start in fallback mode.
	AllowDegraded bool
}

// Worker is one running sandbox process, seen through its two pipes.
type Worker interface {
	// Stdin is the pipe the broker writes frames to; Stdout the pipe it reads frames from. Both end when the worker is killed.
	Stdin() io.WriteCloser
	Stdout() io.ReadCloser
	// Mode is the sandbox mode the worker really runs in (ModeLPAC or ModeFallback), read from its token, not from its own words.
	Mode() string
	// SHA256 is the hash of the worker file the spawner checked before it started it (64 hex).
	SHA256() string
	// Kill ends the process and everything in its job. It is safe to call more than once and from any goroutine.
	Kill()
	// Wait returns when the process is gone (after Kill, or by itself).
	Wait() error
}

// ErrDegradedRefused is what a Spawner returns from Start when it could only start in the fallback mode and the options did not allow
// it. The run then ends with sandbox_degraded_refused.
var ErrDegradedRefused = errors.New("codemode: reduced isolation is not allowed")
