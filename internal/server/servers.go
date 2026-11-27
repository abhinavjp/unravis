package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"api-audit-explorer/internal/bbsaved"
	"api-audit-explorer/internal/blackbox"
	"api-audit-explorer/internal/remote/pull"
	"api-audit-explorer/internal/remote/registry"
	"api-audit-explorer/internal/remote/saltmaster"
	"api-audit-explorer/internal/remote/service"
	"api-audit-explorer/internal/remote/sshdirect"
	"api-audit-explorer/internal/remote/transport"
)

// Servers in Unravis: the saved list of servers (sealed), the host key check, the test, the folder browser and the pulls that bring a
// server's Blackbox recordings and logs to this PC. The secret of a server goes from the sealed list into the sign-in code and nowhere
// else: not into an answer, a log line or a case file. Every action reads; nothing here changes the server.

// ServersRegistry is the saved list. *registry.Store is the real one.
type ServersRegistry interface {
	List() ([]registry.Entry, error)
	Get(id string) (registry.Entry, error)
	Add(in registry.AddInput) (registry.Entry, error)
	Update(id string, p registry.Patch) (registry.Entry, error)
	SetSecret(id, auth, secret, passphrase string) error
	Remove(id string) error
}

// ServersService does the work on a server. *service.Service is the real one; tests give a fake.
type ServersService interface {
	HostKeys(ctx context.Context, id string) ([]sshdirect.HostKey, error)
	Pin(ctx context.Context, id, fingerprint string) error
	ReplaceKey(ctx context.Context, id, oldFingerprint, newFingerprint string) error
	Machines(ctx context.Context, id string) ([]service.MachineView, error)
	Test(ctx context.Context, id, machine string) (service.TestResult, error)
	Discover(ctx context.Context, id, machine string) (service.Discovery, error)
	Browse(ctx context.Context, id, machine, dir string) ([]transport.FileInfo, error)
	Plan(ctx context.Context, id, machine string, req pull.Request) (pull.Plan, error)
	Pull(ctx context.Context, id, machine string, req pull.Request, jobID string, progress func(pull.Progress)) (service.Pulled, error)
}

// ServersDeps wires the Servers pages. Reg, Svc and StagingRoot are required.
type ServersDeps struct {
	Reg ServersRegistry
	Svc ServersService
	// StagingRoot is the folder pulls land in: StagingRoot/<server id>/<job id>. A pulled file is served only from here.
	StagingRoot string
	Now         func() time.Time
}

// WithServers attaches the Servers pages. A build without the list leaves them off.
func WithServers(d ServersDeps) Option {
	return func(h *handler) {
		if d.Reg == nil || d.Svc == nil || d.StagingRoot == "" {
			return
		}
		h.servers = newServersHub(d)
	}
}

type serversHub struct {
	deps ServersDeps
	// tries is the budget of actions that open a connection to a server, so a loop in the page cannot hammer a server.
	tries connectionTestLimiter
	jobs  srvJobs
}

func newServersHub(d ServersDeps) *serversHub {
	s := &serversHub{deps: d}
	s.tries.limit = 60
	s.jobs.byID = map[string]*srvJob{}
	return s
}

func (s *serversHub) now() time.Time {
	if s.deps.Now != nil {
		return s.deps.Now()
	}
	return time.Now()
}

const (
	srvActionTimeout = 90 * time.Second
	srvMaxJobs       = 30
	srvMaxSinceHours = 24 * 366
)

var (
	srvIDRe  = regexp.MustCompile(`^srv-[0-9a-f]{24}$`)
	srvJobRe = regexp.MustCompile(`^job-[0-9a-f]{16}$`)
)

/* ------------------------------------------------------------- routes */

// srvPath matches /api/servers/{id}/{tail...} for an id the list made, and returns "id" or "id/rest". The tail is the exact shape given;
// "*" stands for one path segment.
func srvPath(tail ...string) func(string) (string, bool) {
	return func(path string) (string, bool) {
		rest, ok := strings.CutPrefix(path, "/api/servers/")
		if !ok {
			return "", false
		}
		parts := strings.Split(rest, "/")
		if len(parts) != 1+len(tail) || !srvIDRe.MatchString(parts[0]) {
			return "", false
		}
		for i, want := range tail {
			got := parts[i+1]
			if got == "" || (want != "*" && got != want) {
				return "", false
			}
			if want == "*" && (len(got) > 120 || strings.ContainsAny(got, "\\\x00")) {
				return "", false
			}
		}
		return rest, true
	}
}

func srvRoute(pattern string, match func(string) (string, bool), methods map[string]routeFunc) route {
	return route{pattern: pattern, match: match, methods: methods}
}

func srvFn(fn func(*handler, http.ResponseWriter, *http.Request, string)) routeFunc { return fn }

func init() {
	routes = append(routes,
		srvRoute("/api/servers", exactPath("/api/servers"), map[string]routeFunc{
			http.MethodGet:  srvFn(func(h *handler, w http.ResponseWriter, r *http.Request, _ string) { h.srvList(w, r) }),
			http.MethodPost: srvFn(func(h *handler, w http.ResponseWriter, r *http.Request, _ string) { h.srvAdd(w, r) }),
		}),
		srvRoute("/api/servers/{id}", srvPath(), map[string]routeFunc{
			http.MethodPatch:  srvFn(func(h *handler, w http.ResponseWriter, r *http.Request, id string) { h.srvPatch(w, r, id) }),
			http.MethodDelete: srvFn(func(h *handler, w http.ResponseWriter, r *http.Request, id string) { h.srvRemove(w, r, id) }),
		}),
		srvRoute("/api/servers/{id}/secret", srvPath("secret"), bbM(http.MethodPut, func(h *handler, w http.ResponseWriter, r *http.Request, a string) { h.srvSecret(w, r, srvID(a)) })),
		srvRoute("/api/servers/{id}/host-keys", srvPath("host-keys"), bbM(http.MethodPost, func(h *handler, w http.ResponseWriter, r *http.Request, a string) { h.srvHostKeys(w, r, srvID(a)) })),
		srvRoute("/api/servers/{id}/pin", srvPath("pin"), bbM(http.MethodPost, func(h *handler, w http.ResponseWriter, r *http.Request, a string) { h.srvPin(w, r, srvID(a)) })),
		srvRoute("/api/servers/{id}/replace-key", srvPath("replace-key"), bbM(http.MethodPost, func(h *handler, w http.ResponseWriter, r *http.Request, a string) { h.srvReplaceKey(w, r, srvID(a)) })),
		srvRoute("/api/servers/{id}/machines", srvPath("machines"), bbM(http.MethodPost, func(h *handler, w http.ResponseWriter, r *http.Request, a string) { h.srvMachines(w, r, srvID(a)) })),
		srvRoute("/api/servers/{id}/test", srvPath("test"), bbM(http.MethodPost, func(h *handler, w http.ResponseWriter, r *http.Request, a string) { h.srvTest(w, r, srvID(a)) })),
		srvRoute("/api/servers/{id}/sources", srvPath("sources"), bbM(http.MethodPost, func(h *handler, w http.ResponseWriter, r *http.Request, a string) { h.srvSources(w, r, srvID(a)) })),
		srvRoute("/api/servers/{id}/browse", srvPath("browse"), bbM(http.MethodPost, func(h *handler, w http.ResponseWriter, r *http.Request, a string) { h.srvBrowse(w, r, srvID(a)) })),
		srvRoute("/api/servers/{id}/plan", srvPath("plan"), bbM(http.MethodPost, func(h *handler, w http.ResponseWriter, r *http.Request, a string) { h.srvPlan(w, r, srvID(a)) })),
		srvRoute("/api/servers/{id}/pulls", srvPath("pulls"), map[string]routeFunc{
			http.MethodGet:  srvFn(func(h *handler, w http.ResponseWriter, r *http.Request, a string) { h.srvJobList(w, r, srvID(a)) }),
			http.MethodPost: srvFn(func(h *handler, w http.ResponseWriter, r *http.Request, a string) { h.srvPullStart(w, r, srvID(a)) }),
		}),
		srvRoute("/api/servers/{id}/pulls/{job}", srvPath("pulls", "*"), map[string]routeFunc{
			http.MethodGet:    srvFn(func(h *handler, w http.ResponseWriter, r *http.Request, a string) { h.srvJobGet(w, r, a) }),
			http.MethodDelete: srvFn(func(h *handler, w http.ResponseWriter, r *http.Request, a string) { h.srvJobCancel(w, r, a) }),
		}),
		srvRoute("/api/servers/{id}/pulls/{job}/files/{name}", srvPath("pulls", "*", "files", "*"), bbM(http.MethodGet, func(h *handler, w http.ResponseWriter, r *http.Request, a string) { h.srvJobFile(w, r, a) })),
	)
}

// srvID is the server id of a route argument ("id" or "id/rest").
func srvID(arg string) string {
	id, _, _ := strings.Cut(arg, "/")
	return id
}

/* -------------------------------------------------------------- bodies */

type srvAddBody struct {
	Name         string              `json:"name"`
	Host         string              `json:"host"`
	Port         int                 `json:"port"`
	User         string              `json:"user"`
	OS           string              `json:"os"`
	Auth         string              `json:"auth"`
	Secret       string              `json:"secret"`
	Passphrase   string              `json:"passphrase"`
	Transports   []string            `json:"transports"`
	MinionIDs    []string            `json:"minionIds"`
	SaltSudo     bool                `json:"saltSudo"`
	Folders      map[string][]string `json:"folders"`
	BlackboxAddr string              `json:"blackboxAddr"`
	// BlackboxID names a saved Blackbox that holds the read key. BlackboxKey saves a new one for this server (the key goes to the Blackbox
	// list's sealed file and is not kept here).
	BlackboxID  string `json:"blackboxId"`
	BlackboxKey string `json:"blackboxKey"`
}

type srvPatchBody struct {
	Name         *string              `json:"name"`
	Transports   *[]string            `json:"transports"`
	MinionIDs    *[]string            `json:"minionIds"`
	SaltSudo     *bool                `json:"saltSudo"`
	Folders      *map[string][]string `json:"folders"`
	BlackboxAddr *string              `json:"blackboxAddr"`
	BlackboxID   *string              `json:"blackboxId"`
	BlackboxKey  string               `json:"blackboxKey"`
}

type srvSecretBody struct {
	Auth       string `json:"auth"`
	Secret     string `json:"secret"`
	Passphrase string `json:"passphrase"`
}

type srvPinBody struct {
	Fingerprint string `json:"fingerprint"`
}

type srvReplaceBody struct {
	Old string `json:"old"`
	New string `json:"new"`
}

type srvBrowseBody struct {
	// Machine is the Salt machine to read; empty for a server read directly.
	Machine string `json:"machine"`
	Dir     string `json:"dir"`
}

type srvPullBody struct {
	// Machine is the Salt machine to read; empty for a server read directly.
	Machine string `json:"machine"`
	Kind    string `json:"kind"`
	Dir     string `json:"dir"`
	Pattern string `json:"pattern"`
	// SinceHours limits the pull to files written in the last so many hours; 0 means no limit.
	SinceHours int `json:"sinceHours"`
	// Resume is the id of an interrupted job of this server to carry on.
	Resume string `json:"resume"`
}

type srvFailure struct {
	Error     string `json:"error"`
	Reason    string `json:"reason"`
	Next      string `json:"next,omitempty"`
	Pinned    string `json:"pinned,omitempty"`
	Presented string `json:"presented,omitempty"`
}

/* ------------------------------------------------------------- helpers */

func (h *handler) srvReady(w http.ResponseWriter) bool {
	if h.servers == nil {
		writeJSON(w, http.StatusServiceUnavailable, srvFailure{Error: "Servers are not available in this build.", Reason: "unavailable"}, 4096)
		return false
	}
	return true
}

// srvFail answers a failure with a plain sentence and a stable reason. Text the code did not write itself is never passed on: an
// unknown error is logged by type and answered generally.
func (h *handler) srvFail(w http.ResponseWriter, r *http.Request, err error) {
	status, f := srvClassify(err)
	h.eventWarn(r, "servers.failed", "reason", f.Reason)
	writeJSON(w, status, f, 4096)
}

// srvClassify maps an error to the status and the failure the page shows.
func srvClassify(err error) (int, srvFailure) {
	var changed *sshdirect.HostKeyChanged
	var tooLarge pull.ErrPlanTooLarge
	status, reason, next := http.StatusInternalServerError, "failed", ""
	text := "Something went wrong. Nothing was changed."
	switch {
	case errors.As(err, &changed):
		return http.StatusConflict, srvFailure{Error: changed.Error(), Reason: "host-key-changed", Pinned: changed.Pinned, Presented: changed.Presented,
			Next: "If the server was rebuilt on purpose, compare the new key with the person who runs it, then replace the pinned key."}
	case errors.Is(err, registry.ErrNotFound):
		status, reason, text = http.StatusNotFound, "not-listed", err.Error()
	case errors.Is(err, registry.ErrCorrupt):
		status, reason, text = http.StatusConflict, "list-damaged", err.Error()
		next = "The file was left as it is. Restore it from a backup, or move it away to start a new list."
	case errors.Is(err, registry.ErrUnavailable):
		status, reason, text = http.StatusServiceUnavailable, "list-unavailable", err.Error()
	case errors.Is(err, registry.ErrInvalid), errors.Is(err, registry.ErrTooMany):
		status, reason, text = http.StatusUnprocessableEntity, "invalid", err.Error()
	case errors.Is(err, registry.ErrDuplicate):
		status, reason, text = http.StatusConflict, "duplicate", err.Error()
	case errors.Is(err, registry.ErrHostKeySet):
		status, reason, text = http.StatusConflict, "host-key-set", err.Error()
	case errors.Is(err, registry.ErrNoHostKey), errors.Is(err, service.ErrNeedHostKey), errors.Is(err, sshdirect.ErrNoHostKey):
		status, reason, text = http.StatusConflict, "need-host-key", service.ErrNeedHostKey.Error()
	case errors.Is(err, service.ErrKeyNotShown):
		status, reason, text = http.StatusConflict, "key-not-shown", err.Error()
	case errors.Is(err, service.ErrNoSSH):
		status, reason, text = http.StatusUnprocessableEntity, "no-ssh", err.Error()
	case errors.Is(err, service.ErrNoSalt):
		status, reason, text = http.StatusUnprocessableEntity, "no-salt", err.Error()
	case errors.Is(err, service.ErrNeedMachine):
		status, reason, text = http.StatusUnprocessableEntity, "need-machine", err.Error()
	case errors.Is(err, service.ErrMachineNotListed):
		status, reason, text = http.StatusUnprocessableEntity, "machine-not-listed", err.Error()
	case errors.Is(err, service.ErrNoExec):
		status, reason, text = http.StatusUnprocessableEntity, "no-exec", err.Error()
	case errors.Is(err, saltmaster.ErrNoMinion), errors.Is(err, saltmaster.ErrOffline):
		status, reason, text = http.StatusBadGateway, "machine-offline", err.Error()
		next = "Check that the machine is on and connected to the Salt master, then try again."
	case errors.Is(err, saltmaster.ErrTooLarge):
		status, reason, text = http.StatusUnprocessableEntity, "too-many-files", err.Error()
	case errors.Is(err, saltmaster.ErrBadReply), errors.Is(err, saltmaster.ErrMasterCmd), errors.Is(err, saltmaster.ErrShortChunk):
		status, reason, text = http.StatusBadGateway, "salt-master", err.Error()
		next = "If the jump host needs sudo to run salt, tick that option on the server. Otherwise ask whoever runs the Salt master."
	case errors.Is(err, service.ErrFolderNotKind):
		status, reason, text = http.StatusUnprocessableEntity, "folder-not-allowed", err.Error()
	case errors.Is(err, service.ErrNoBlackbox):
		status, reason, text = http.StatusUnprocessableEntity, "no-blackbox", err.Error()
	case errors.Is(err, transport.ErrNotAllowed):
		status, reason, text = http.StatusUnprocessableEntity, "not-allowed", err.Error()
	case errors.Is(err, sshdirect.ErrListTooLarge):
		status, reason, text = http.StatusUnprocessableEntity, "too-many-files", err.Error()
	case errors.As(err, &tooLarge):
		status, reason, text = http.StatusUnprocessableEntity, "too-large", tooLarge.Error()
	case errors.Is(err, sshdirect.ErrSignIn):
		status, reason, text = http.StatusBadGateway, "sign-in", err.Error()
		next = "Check the user name and the key or password saved for this server."
	case errors.Is(err, sshdirect.ErrUnreachable):
		status, reason, text = http.StatusBadGateway, "unreachable", err.Error()
		next = "Check the address and port. If the server is only reachable over a VPN or office network, connect to it first, then try again."
	case errors.Is(err, sshdirect.ErrTunnelRefused), errors.Is(err, sshdirect.ErrNoTunnel):
		status, reason, text = http.StatusBadGateway, "tunnel", err.Error()
	case errors.Is(err, sshdirect.ErrCommand):
		status, reason, text = http.StatusBadGateway, "server-command", err.Error()
	case errors.Is(err, context.DeadlineExceeded):
		status, reason, text = http.StatusGatewayTimeout, "timeout", "The server took too long to answer."
	case errors.Is(err, context.Canceled):
		status, reason, text = http.StatusBadRequest, "cancelled", "That was cancelled."
	}
	return status, srvFailure{Error: text, Reason: reason, Next: next}
}

// srvAllow is the connection budget; true means go on.
func (h *handler) srvAllow(w http.ResponseWriter) bool {
	if h.servers.tries.allow() {
		return true
	}
	writeJSON(w, http.StatusTooManyRequests, srvFailure{Error: "Too many tries in a minute.", Reason: "rate", Next: "Wait a minute and try again."}, 4096)
	return false
}

// srvAction runs one connection action under its own time limit, apart from the page's request lifetime only by the limit.
func srvAction(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), srvActionTimeout)
}

// srvSaveKey stores a Blackbox read key in the Blackbox list and returns the saved entry's id. A recorder already listed under the same
// place is reused.
func (h *handler) srvSaveKey(r *http.Request, name, host, addr, key string) (string, error) {
	if h.bb == nil {
		return "", errors.New("Blackbox is not available in this build, so its key cannot be saved.") //nolint:staticcheck // shown to the person as a sentence
	}
	if addr == "" {
		addr = "7790"
	}
	if !strings.Contains(addr, ":") {
		addr = host + ":" + addr
	}
	loc, err := blackbox.ParseLocation("http://" + addr)
	if err != nil {
		return "", err
	}
	e, err := h.bb.deps.Store.Add(bbsaved.AddInput{Name: name, Location: loc, Key: key, RecorderHost: host, RecorderName: name})
	if err != nil && !errors.Is(err, bbsaved.ErrDuplicate) {
		return "", err
	}
	if errors.Is(err, bbsaved.ErrDuplicate) {
		if serr := h.bb.deps.Store.SetKey(e.ID, key); serr != nil {
			return "", serr
		}
	}
	h.event(r, "servers.blackbox-key.saved")
	return e.ID, nil
}

/* ------------------------------------------------------------ handlers */

type srvListAnswer struct {
	Items []srvItem `json:"items"`
}

type srvItem struct {
	registry.Entry
	// Running is the id of the pull that is running on this server now.
	Running string `json:"running,omitempty"`
}

func (h *handler) srvList(w http.ResponseWriter, r *http.Request) {
	if !h.srvReady(w) {
		return
	}
	list, err := h.servers.deps.Reg.List()
	if err != nil {
		h.srvFail(w, r, err)
		return
	}
	out := srvListAnswer{Items: make([]srvItem, 0, len(list))}
	for _, e := range list {
		out.Items = append(out.Items, srvItem{Entry: e, Running: h.servers.jobs.runningOn(e.ID)})
	}
	writeJSON(w, http.StatusOK, out, 1<<20)
}

func (h *handler) srvAdd(w http.ResponseWriter, r *http.Request) {
	if !h.srvReady(w) {
		return
	}
	var b srvAddBody
	if !decodeJSON(w, r, &b) {
		return
	}
	if b.BlackboxKey != "" && b.BlackboxID == "" {
		id, err := h.srvSaveKey(r, strings.TrimSpace(b.Name), strings.TrimSpace(b.Host), strings.TrimSpace(b.BlackboxAddr), b.BlackboxKey)
		if err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, srvFailure{Error: err.Error(), Reason: "blackbox-key"}, 4096)
			return
		}
		b.BlackboxID = id
	}
	e, err := h.servers.deps.Reg.Add(registry.AddInput{
		Name: b.Name, Host: b.Host, Port: b.Port, User: b.User, OS: b.OS, Auth: b.Auth, Secret: b.Secret, Passphrase: b.Passphrase,
		Transports: b.Transports, MinionIDs: b.MinionIDs, SaltSudo: b.SaltSudo, Folders: b.Folders, BlackboxAddr: b.BlackboxAddr, BlackboxID: b.BlackboxID,
	})
	if err != nil {
		h.srvFail(w, r, err)
		return
	}
	h.event(r, "servers.added", "server", e.ID)
	writeJSON(w, http.StatusCreated, e, 1<<16)
}

func (h *handler) srvPatch(w http.ResponseWriter, r *http.Request, id string) {
	if !h.srvReady(w) {
		return
	}
	id = srvID(id)
	var b srvPatchBody
	if !decodeJSON(w, r, &b) {
		return
	}
	p := registry.Patch{Name: b.Name, Transports: b.Transports, MinionIDs: b.MinionIDs, SaltSudo: b.SaltSudo, Folders: b.Folders, BlackboxAddr: b.BlackboxAddr, BlackboxID: b.BlackboxID}
	if b.BlackboxKey != "" && b.BlackboxID == nil {
		cur, err := h.servers.deps.Reg.Get(id)
		if err != nil {
			h.srvFail(w, r, err)
			return
		}
		addr := cur.BlackboxAddr
		if b.BlackboxAddr != nil {
			addr = *b.BlackboxAddr
		}
		bbID, err := h.srvSaveKey(r, cur.Name, cur.Host, addr, b.BlackboxKey)
		if err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, srvFailure{Error: err.Error(), Reason: "blackbox-key"}, 4096)
			return
		}
		p.BlackboxID = &bbID
	}
	e, err := h.servers.deps.Reg.Update(id, p)
	if err != nil {
		h.srvFail(w, r, err)
		return
	}
	h.event(r, "servers.updated", "server", id)
	writeJSON(w, http.StatusOK, e, 1<<16)
}

func (h *handler) srvRemove(w http.ResponseWriter, r *http.Request, id string) {
	if !h.srvReady(w) {
		return
	}
	id = srvID(id)
	if h.servers.jobs.runningOn(id) != "" {
		writeJSON(w, http.StatusConflict, srvFailure{Error: "A pull is running on this server.", Reason: "busy", Next: "Stop the pull first."}, 4096)
		return
	}
	if err := h.servers.deps.Reg.Remove(id); err != nil {
		h.srvFail(w, r, err)
		return
	}
	h.servers.jobs.forget(id)
	h.event(r, "servers.removed", "server", id)
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) srvSecret(w http.ResponseWriter, r *http.Request, id string) {
	if !h.srvReady(w) {
		return
	}
	var b srvSecretBody
	if !decodeJSON(w, r, &b) {
		return
	}
	if err := h.servers.deps.Reg.SetSecret(id, b.Auth, b.Secret, b.Passphrase); err != nil {
		h.srvFail(w, r, err)
		return
	}
	h.event(r, "servers.secret.saved", "server", id, "auth", b.Auth)
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) srvHostKeys(w http.ResponseWriter, r *http.Request, id string) {
	if !h.srvReady(w) || !h.srvAllow(w) {
		return
	}
	ctx, cancel := srvAction(r)
	defer cancel()
	keys, err := h.servers.deps.Svc.HostKeys(ctx, id)
	if err != nil {
		h.srvFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Keys []sshdirect.HostKey `json:"keys"`
	}{keys}, 1<<16)
}

func (h *handler) srvPin(w http.ResponseWriter, r *http.Request, id string) {
	if !h.srvReady(w) || !h.srvAllow(w) {
		return
	}
	var b srvPinBody
	if !decodeJSON(w, r, &b) {
		return
	}
	ctx, cancel := srvAction(r)
	defer cancel()
	if err := h.servers.deps.Svc.Pin(ctx, id, b.Fingerprint); err != nil {
		h.srvFail(w, r, err)
		return
	}
	h.event(r, "servers.host-key.pinned", "server", id)
	e, err := h.servers.deps.Reg.Get(id)
	if err != nil {
		h.srvFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, e, 1<<16)
}

func (h *handler) srvReplaceKey(w http.ResponseWriter, r *http.Request, id string) {
	if !h.srvReady(w) || !h.srvAllow(w) {
		return
	}
	var b srvReplaceBody
	if !decodeJSON(w, r, &b) {
		return
	}
	ctx, cancel := srvAction(r)
	defer cancel()
	if err := h.servers.deps.Svc.ReplaceKey(ctx, id, b.Old, b.New); err != nil {
		h.srvFail(w, r, err)
		return
	}
	h.event(r, "servers.host-key.replaced", "server", id)
	e, err := h.servers.deps.Reg.Get(id)
	if err != nil {
		h.srvFail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, e, 1<<16)
}

func (h *handler) srvTest(w http.ResponseWriter, r *http.Request, id string) {
	if !h.srvReady(w) || !h.srvAllow(w) {
		return
	}
	var b srvBrowseBody // the body is optional: a Salt server names the machine in it
	if r.ContentLength != 0 && !decodeJSON(w, r, &b) {
		return
	}
	ctx, cancel := srvAction(r)
	defer cancel()
	res, err := h.servers.deps.Svc.Test(ctx, id, b.Machine)
	if err != nil {
		h.srvFail(w, r, err)
		return
	}
	h.event(r, "servers.tested", "server", id)
	writeJSON(w, http.StatusOK, res, 1<<18)
}

func (h *handler) srvSources(w http.ResponseWriter, r *http.Request, id string) {
	if !h.srvReady(w) || !h.srvAllow(w) {
		return
	}
	var b srvBrowseBody
	if r.ContentLength != 0 && !decodeJSON(w, r, &b) {
		return
	}
	ctx, cancel := srvAction(r)
	defer cancel()
	res, err := h.servers.deps.Svc.Discover(ctx, id, b.Machine)
	if err != nil {
		h.srvFail(w, r, err)
		return
	}
	h.event(r, "servers.sources", "server", id)
	writeJSON(w, http.StatusOK, res, 1<<18)
}

// srvMachines lists the machines behind a Salt server's master, with the ones the person ticked marked. It signs in and changes nothing.
func (h *handler) srvMachines(w http.ResponseWriter, r *http.Request, id string) {
	if !h.srvReady(w) || !h.srvAllow(w) {
		return
	}
	ctx, cancel := srvAction(r)
	defer cancel()
	list, err := h.servers.deps.Svc.Machines(ctx, id)
	if err != nil {
		h.srvFail(w, r, err)
		return
	}
	if list == nil {
		list = []service.MachineView{}
	}
	h.event(r, "servers.machines.listed", "server", id)
	writeJSON(w, http.StatusOK, struct {
		Machines []service.MachineView `json:"machines"`
	}{list}, 1<<20)
}

func (h *handler) srvBrowse(w http.ResponseWriter, r *http.Request, id string) {
	if !h.srvReady(w) || !h.srvAllow(w) {
		return
	}
	var b srvBrowseBody
	if !decodeJSON(w, r, &b) {
		return
	}
	ctx, cancel := srvAction(r)
	defer cancel()
	files, err := h.servers.deps.Svc.Browse(ctx, id, b.Machine, b.Dir)
	if err != nil {
		h.srvFail(w, r, err)
		return
	}
	if files == nil {
		files = []transport.FileInfo{}
	}
	writeJSON(w, http.StatusOK, struct {
		Dir   string               `json:"dir"`
		Files []transport.FileInfo `json:"files"`
	}{b.Dir, files}, 4<<20)
}

func (h *handler) srvRequest(b srvPullBody) (pull.Request, bool) {
	req := pull.Request{Kind: b.Kind, Dir: b.Dir, Pattern: b.Pattern}
	if b.SinceHours < 0 || b.SinceHours > srvMaxSinceHours {
		return req, false
	}
	if b.SinceHours > 0 {
		req.Since = h.servers.now().Add(-time.Duration(b.SinceHours) * time.Hour)
	}
	return req, true
}

func (h *handler) srvPlan(w http.ResponseWriter, r *http.Request, id string) {
	if !h.srvReady(w) || !h.srvAllow(w) {
		return
	}
	var b srvPullBody
	if !decodeJSON(w, r, &b) {
		return
	}
	req, ok := h.srvRequest(b)
	if !ok {
		writeJSON(w, http.StatusUnprocessableEntity, srvFailure{Error: "That time range is not valid.", Reason: "invalid"}, 4096)
		return
	}
	ctx, cancel := srvAction(r)
	defer cancel()
	plan, err := h.servers.deps.Svc.Plan(ctx, id, b.Machine, req)
	var tooLarge pull.ErrPlanTooLarge
	if err != nil && !errors.As(err, &tooLarge) {
		h.srvFail(w, r, err)
		return
	}
	if plan.Files == nil {
		plan.Files = []pull.PlanFile{}
	}
	if plan.Skipped == nil {
		plan.Skipped = []pull.Skipped{}
	}
	if err != nil {
		plan.Problem = tooLarge.Problem
	}
	writeJSON(w, http.StatusOK, plan, 4<<20)
}

/* ---------------------------------------------------------------- pulls */

// srvJob is one pull, running or finished, kept in memory so the page can follow it. The files stay on disk.
type srvJob struct {
	id, server string
	machine    string // the Salt machine read; empty for a direct read
	kind, dir  string
	started    time.Time

	mu       sync.Mutex
	state    string // running, done, failed, stopped
	progress pull.Progress
	result   *service.Pulled
	failure  *srvFailure
	finished time.Time
	bbID     string
	bbName   string
	bbNote   string
	cancel   context.CancelFunc
}

type srvJobs struct {
	mu   sync.Mutex
	byID map[string]*srvJob
}

func (j *srvJobs) runningOn(server string) string {
	j.mu.Lock()
	defer j.mu.Unlock()
	for id, job := range j.byID {
		if job.server == server && job.running() {
			return id
		}
	}
	return ""
}

func (j *srvJobs) forget(server string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for id, job := range j.byID {
		if job.server == server && !job.running() {
			delete(j.byID, id)
		}
	}
}

func (j *srvJobs) get(server, id string) *srvJob {
	j.mu.Lock()
	defer j.mu.Unlock()
	job := j.byID[id]
	if job == nil || job.server != server {
		return nil
	}
	return job
}

// add keeps a job, dropping the oldest finished ones past the cap. It reports false when the server already has a job running.
func (j *srvJobs) add(job *srvJob) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, other := range j.byID {
		if other.server == job.server && other.running() {
			return false
		}
	}
	for len(j.byID) >= srvMaxJobs {
		var oldest *srvJob
		for _, other := range j.byID {
			if !other.running() && (oldest == nil || other.started.Before(oldest.started)) {
				oldest = other
			}
		}
		if oldest == nil {
			break
		}
		delete(j.byID, oldest.id)
	}
	j.byID[job.id] = job
	return true
}

func (j *srvJob) running() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.state == "running"
}

type srvProgressView struct {
	File       string `json:"file"`
	FileBytes  int64  `json:"fileBytes"`
	JobBytes   int64  `json:"jobBytes"`
	FilesTotal int    `json:"filesTotal"`
	FilesDone  int    `json:"filesDone"`
}

type srvJobView struct {
	ID       string           `json:"id"`
	Server   string           `json:"server"`
	Machine  string           `json:"machine,omitempty"`
	Kind     string           `json:"kind"`
	Dir      string           `json:"dir"`
	State    string           `json:"state"`
	Started  time.Time        `json:"started"`
	Finished *time.Time       `json:"finished,omitempty"`
	Progress *srvProgressView `json:"progress,omitempty"`
	Result   *service.Pulled  `json:"result,omitempty"`
	Failure  *srvFailure      `json:"failure,omitempty"`
	// Blackbox is the saved Blackbox a pulled recording was added as, so the Blackbox view reads it.
	BlackboxID   string `json:"blackboxId,omitempty"`
	BlackboxName string `json:"blackboxName,omitempty"`
	BlackboxNote string `json:"blackboxNote,omitempty"`
}

func (j *srvJob) view() srvJobView {
	j.mu.Lock()
	defer j.mu.Unlock()
	v := srvJobView{ID: j.id, Server: j.server, Machine: j.machine, Kind: j.kind, Dir: j.dir, State: j.state, Started: j.started, Result: j.result, Failure: j.failure,
		BlackboxID: j.bbID, BlackboxName: j.bbName, BlackboxNote: j.bbNote}
	if !j.finished.IsZero() {
		f := j.finished
		v.Finished = &f
	}
	if j.state == "running" || j.progress.FilesTotal > 0 {
		p := j.progress
		v.Progress = &srvProgressView{File: p.File, FileBytes: p.FileBytes, JobBytes: p.JobBytes, FilesTotal: p.FilesTotal, FilesDone: p.FilesDone}
	}
	return v
}

func newJobID() (string, error) {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "job-" + hex.EncodeToString(raw[:]), nil
}

func (h *handler) srvPullStart(w http.ResponseWriter, r *http.Request, id string) {
	if !h.srvReady(w) || !h.srvAllow(w) {
		return
	}
	var b srvPullBody
	if !decodeJSON(w, r, &b) {
		return
	}
	req, ok := h.srvRequest(b)
	if !ok {
		writeJSON(w, http.StatusUnprocessableEntity, srvFailure{Error: "That time range is not valid.", Reason: "invalid"}, 4096)
		return
	}
	if _, err := h.servers.deps.Reg.Get(id); err != nil {
		h.srvFail(w, r, err)
		return
	}
	jobID := b.Resume
	if jobID == "" {
		var err error
		if jobID, err = newJobID(); err != nil {
			h.srvFail(w, r, err)
			return
		}
	} else if !srvJobRe.MatchString(jobID) {
		writeJSON(w, http.StatusUnprocessableEntity, srvFailure{Error: "That pull cannot be resumed.", Reason: "invalid"}, 4096)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	job := &srvJob{id: jobID, server: id, machine: b.Machine, kind: req.Kind, dir: req.Dir, started: h.servers.now(), state: "running", cancel: cancel}
	if !h.servers.jobs.add(job) {
		cancel()
		writeJSON(w, http.StatusConflict, srvFailure{Error: "A pull is already running on this server.", Reason: "busy", Next: "Wait for it to finish, or stop it."}, 4096)
		return
	}
	h.event(r, "servers.pull.started", "server", id, "job", jobID, "kind", req.Kind)
	go h.srvRunPull(ctx, job, req)
	writeJSON(w, http.StatusAccepted, job.view(), 1<<16)
}

func (h *handler) srvRunPull(ctx context.Context, job *srvJob, req pull.Request) {
	defer job.cancel()
	res, err := h.servers.deps.Svc.Pull(ctx, job.server, job.machine, req, job.id, func(p pull.Progress) {
		job.mu.Lock()
		job.progress = p
		job.mu.Unlock()
	})
	job.mu.Lock()
	job.finished = h.servers.now()
	if res.JobID != "" {
		job.result = &res
	}
	switch {
	case err == nil && res.OK():
		job.state = "done"
	case ctx.Err() != nil || res.Interrupted:
		job.state = "stopped"
	default:
		job.state = "failed"
	}
	if err != nil && job.state != "stopped" {
		job.failure = srvFailureOf(err)
	}
	job.mu.Unlock()
	h.logEvent("servers.pull.finished", "server", job.server, "job", job.id, "state", job.view().State)
	if res.BlackboxFolder != nil && err == nil && res.OK() {
		h.srvListPulledBlackbox(job, res)
	}
}

// srvFailureOf is srvFail's mapping for a job that ends in the background.
func srvFailureOf(err error) *srvFailure {
	_, f := srvClassify(err)
	return &f
}

// srvListPulledBlackbox adds a pulled recorder folder to the Blackbox list, so the Blackbox view reads it like any folder.
func (h *handler) srvListPulledBlackbox(job *srvJob, res service.Pulled) {
	note := ""
	var id, name string
	switch h.bb {
	case nil:
		note = "Blackbox is not available in this build, so the recording was kept as files only."
	default:
		label := "Pulled " + job.kind
		if e, err := h.servers.deps.Reg.Get(job.server); err == nil {
			label = e.Name
			if job.machine != "" {
				label += " / " + job.machine
			}
			label += " (pulled " + job.started.Local().Format("2 Jan 15:04") + ")"
		}
		e, err := h.bb.deps.Store.Add(bbsaved.AddInput{Name: label, Location: *res.BlackboxFolder})
		if err != nil && !errors.Is(err, bbsaved.ErrDuplicate) {
			note = "The files were pulled but could not be added to the Blackbox list: " + err.Error()
		} else {
			id, name = e.ID, e.Name
		}
	}
	job.mu.Lock()
	job.bbID, job.bbName, job.bbNote = id, name, note
	job.mu.Unlock()
}

func (h *handler) srvJobList(w http.ResponseWriter, r *http.Request, id string) {
	if !h.srvReady(w) {
		return
	}
	h.servers.jobs.mu.Lock()
	var jobs []*srvJob
	for _, j := range h.servers.jobs.byID {
		if j.server == id {
			jobs = append(jobs, j)
		}
	}
	h.servers.jobs.mu.Unlock()
	sort.Slice(jobs, func(a, b int) bool { return jobs[a].started.After(jobs[b].started) })
	out := struct {
		Items []srvJobView `json:"items"`
	}{Items: make([]srvJobView, 0, len(jobs))}
	for _, j := range jobs {
		out.Items = append(out.Items, j.view())
	}
	writeJSON(w, http.StatusOK, out, 4<<20)
}

func (h *handler) srvJobGet(w http.ResponseWriter, r *http.Request, arg string) {
	if !h.srvReady(w) {
		return
	}
	server, job, _ := strings.Cut(arg, "/pulls/")
	j := h.servers.jobs.get(server, job)
	if j == nil {
		writeJSON(w, http.StatusNotFound, srvFailure{Error: "That pull is not known any more.", Reason: "not-listed"}, 4096)
		return
	}
	writeJSON(w, http.StatusOK, j.view(), 4<<20)
}

func (h *handler) srvJobCancel(w http.ResponseWriter, r *http.Request, arg string) {
	if !h.srvReady(w) {
		return
	}
	server, job, _ := strings.Cut(arg, "/pulls/")
	j := h.servers.jobs.get(server, job)
	if j == nil {
		writeJSON(w, http.StatusNotFound, srvFailure{Error: "That pull is not known any more.", Reason: "not-listed"}, 4096)
		return
	}
	j.cancel()
	h.event(r, "servers.pull.cancelled", "server", server, "job", job)
	w.WriteHeader(http.StatusNoContent)
}

// srvJobFile sends one pulled file, so the page can hand it to the case import. Only a finished file in the job's own folder is served.
func (h *handler) srvJobFile(w http.ResponseWriter, r *http.Request, arg string) {
	if !h.srvReady(w) {
		return
	}
	parts := strings.Split(arg, "/")
	if len(parts) != 5 {
		http.NotFound(w, r)
		return
	}
	server, job, name := parts[0], parts[2], parts[4]
	if !srvIDRe.MatchString(server) || !srvJobRe.MatchString(job) || name == "" || name != filepath.Base(name) ||
		strings.HasPrefix(name, ".") || hasInternalSuffix(name) {
		http.NotFound(w, r)
		return
	}
	path := filepath.Join(h.servers.deps.StagingRoot, server, job, name)
	info, err := os.Lstat(path) //nolint:gosec // name checked above: no separators, no dot prefix
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(path) //nolint:gosec // path comes from a checked, app-owned location
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close() //nolint:errcheck // best-effort cleanup, nothing to do on failure
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+strings.NewReplacer("\"", "", "\r", "", "\n", "").Replace(name)+"\"")
	http.ServeContent(w, r, name, info.ModTime(), f)
}

// hasInternalSuffix is true for the job's own bookkeeping files, which are never served as pulled files.
func hasInternalSuffix(name string) bool {
	low := strings.ToLower(name)
	return strings.HasSuffix(low, ".part") || strings.HasSuffix(low, ".state") || strings.HasSuffix(low, ".tmp")
}
