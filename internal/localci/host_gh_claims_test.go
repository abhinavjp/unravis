package localci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// A fake `gh`: the test binary, copied to a folder at the front of PATH under the name gh, answers the few
// git/refs and git/commits endpoints the way GitHub does (create of an existing ref is a 422), keeping its state in
// a file because every gh call is a new process. It also writes down every request, so a test can see which
// endpoints were asked.

type fakeGHCommit struct {
	Message string `json:"message"`
	Stamp   int64  `json:"stamp"`
}

type fakeGHState struct {
	Refs     map[string]string       `json:"refs"`
	Commits  map[string]fakeGHCommit `json:"commits"`
	Log      []string                `json:"log"`
	Backdate int                     `json:"backdate"` // seconds the stamp of the next commits is set back
}

func loadFakeGH(dir string) fakeGHState {
	var s fakeGHState
	if b, err := os.ReadFile(filepath.Join(dir, "gh-state.json")); err == nil { //nolint:gosec // test helper reads its own temp folder
		_ = json.Unmarshal(b, &s)
	}
	if s.Refs == nil {
		s.Refs = map[string]string{}
	}
	if s.Commits == nil {
		s.Commits = map[string]fakeGHCommit{}
	}
	return s
}

func saveFakeGH(dir string, s fakeGHState) {
	b, _ := json.Marshal(s)
	_ = os.WriteFile(filepath.Join(dir, "gh-state.json"), b, 0o600) //nolint:gosec // test helper writes its own temp folder
}

// fakeGHMain is the child process: it plays `gh api -i [-X M] [--input -] endpoint`.
func fakeGHMain(args []string, dir string) int {
	method, endpoint, hasBody := "GET", "", false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "api", "-i":
		case "-X":
			i++
			method = args[i]
		case "-H":
			i++
		case "--input":
			i++
			hasBody = true
		default:
			endpoint = args[i]
		}
	}
	var body []byte
	if hasBody {
		body, _ = io.ReadAll(os.Stdin)
	}
	s := loadFakeGH(dir)
	s.Log = append(s.Log, method+" "+endpoint)
	code, out := fakeGHHandle(&s, method, endpoint, body)
	saveFakeGH(dir, s)
	fmt.Printf("HTTP/2.0 %d X\r\nDate: %s\r\nContent-Type: application/json\r\n\r\n%s", code, time.Now().UTC().Format(time.RFC1123), out)
	if code >= 300 {
		return 1
	}
	return 0
}

func fakeGHHandle(s *fakeGHState, method, endpoint string, body []byte) (int, string) {
	_, ep, _ := strings.Cut(endpoint, "/git/")
	path, _, _ := strings.Cut(ep, "?")
	msg := func(m string) string { b, _ := json.Marshal(map[string]string{"message": m}); return string(b) }
	switch {
	case method == "POST" && path == "commits":
		var v map[string]any
		_ = json.Unmarshal(body, &v)
		who, _ := v["author"].(map[string]any)
		_, hasDate := who["date"]
		if v["tree"] != emptyTree || who["name"] != "ci" || hasDate || len(v["parents"].([]any)) != 0 {
			return 400, msg("unexpected commit")
		}
		sha := fmt.Sprintf("%040x", len(s.Commits)+1)
		s.Commits[sha] = fakeGHCommit{Message: v["message"].(string), Stamp: time.Now().Add(-time.Duration(s.Backdate) * time.Second).Unix()}
		return 201, fmt.Sprintf(`{"sha":%q}`, sha)
	case method == "GET" && strings.HasPrefix(path, "commits/"):
		c, ok := s.Commits[strings.TrimPrefix(path, "commits/")]
		if !ok {
			return 404, msg("Not Found")
		}
		b, _ := json.Marshal(map[string]any{"message": c.Message, "committer": map[string]any{"date": time.Unix(c.Stamp, 0).UTC().Format(time.RFC3339)}})
		return 200, string(b)
	case method == "POST" && path == "refs":
		var v struct{ Ref, SHA string }
		_ = json.Unmarshal(body, &v)
		if _, ok := s.Refs[v.Ref]; ok {
			return 422, msg("Reference already exists")
		}
		s.Refs[v.Ref] = v.SHA
		return 201, `{}`
	case method == "GET" && strings.HasPrefix(path, "matching-refs/"):
		prefix := "refs/" + strings.TrimPrefix(path, "matching-refs/")
		list := []map[string]any{}
		for ref, sha := range s.Refs {
			if strings.HasPrefix(ref, prefix) {
				list = append(list, map[string]any{"ref": ref, "object": map[string]any{"sha": sha}})
			}
		}
		b, _ := json.Marshal(list)
		return 200, string(b)
	case method == "PATCH" && strings.HasPrefix(path, "refs/"):
		ref := "refs/" + strings.TrimPrefix(path, "refs/")
		if _, ok := s.Refs[ref]; !ok {
			return 422, msg("Reference does not exist")
		}
		var v struct {
			SHA   string
			Force bool
		}
		_ = json.Unmarshal(body, &v)
		s.Refs[ref] = v.SHA
		return 200, `{}`
	case method == "DELETE" && strings.HasPrefix(path, "refs/"):
		ref := "refs/" + strings.TrimPrefix(path, "refs/")
		if _, ok := s.Refs[ref]; !ok {
			return 422, msg("Reference does not exist")
		}
		delete(s.Refs, ref)
		return 204, ""
	}
	return 404, msg("Not Found")
}

// fakeGHHost puts the fake gh first on PATH and returns a claim host that uses it, and the state folder.
func fakeGHHost(t *testing.T) (*GHClaimHost, string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	name := "gh"
	if runtime.GOOS == "windows" {
		name = "gh.exe"
	}
	b, err := os.ReadFile(exe) //nolint:gosec // the test binary itself, copied as the fake gh
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, name), b, 0o700); err != nil { //nolint:gosec // the copy must be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	state := t.TempDir()
	env := append(os.Environ(), "LOCALCI_TEST_CHILD=gh", "LOCALCI_TEST_STATE="+state)
	return NewGHClaimHost(&GHHost{Env: env}), state
}

func TestGHClaimHostRoundTrip(t *testing.T) {
	h, state := fakeGHHost(t)
	ctx := context.Background()
	rec := FleetRecord{V: 1, Agent: "m-aaaa1111", State: ClaimHeld, Item: "pr 7", Workflow: "ci.yml", SHA: testSHA}
	name := testSHA + "-ci.yml"

	if err := h.CreateRef(ctx, "o/r", RefNSClaim, name, rec); err != nil {
		t.Fatal(err)
	}
	if err := h.CreateRef(ctx, "o/r", RefNSClaim, name, rec); !errors.Is(err, ErrRefExists) {
		t.Fatalf("a second create must report the ref exists: %v", err)
	}
	got, err := h.ListRefs(ctx, "o/r", RefNSClaim, testSHA)
	if err != nil || len(got) != 1 || got[0].Name != name || got[0].Rec.Agent != "m-aaaa1111" || got[0].Rec.Item != "pr 7" {
		t.Fatalf("list: %+v %v", got, err)
	}
	if got[0].Age < 0 || got[0].Age > 10*time.Second {
		t.Fatalf("a record just written is young: %v", got[0].Age)
	}
	if other, _ := h.ListRefs(ctx, "o/r", RefNSAgent, ""); len(other) != 0 {
		t.Fatalf("another namespace is separate: %+v", other)
	}

	rec.State, rec.Conclusion = ClaimDone, "success"
	if err := h.UpdateRef(ctx, "o/r", RefNSClaim, name, rec); err != nil {
		t.Fatal(err)
	}
	got, _ = h.ListRefs(ctx, "o/r", RefNSClaim, "")
	if len(got) != 1 || got[0].Rec.State != ClaimDone || got[0].Rec.Conclusion != "success" {
		t.Fatalf("after update: %+v", got)
	}
	if err := h.UpdateRef(ctx, "o/r", RefNSClaim, testSHA+"-missing.yml", rec); !errors.Is(err, ErrRefGone) {
		t.Fatalf("update of a missing ref: %v", err)
	}

	if err := h.DeleteRef(ctx, "o/r", RefNSClaim, name); err != nil {
		t.Fatal(err)
	}
	if err := h.DeleteRef(ctx, "o/r", RefNSClaim, name); err != nil {
		t.Fatalf("a ref that is gone already is not an error: %v", err)
	}
	if got, _ := h.ListRefs(ctx, "o/r", RefNSClaim, ""); len(got) != 0 {
		t.Fatalf("after delete: %+v", got)
	}
	// only the git/commits, git/refs and git/matching-refs endpoints of the repository were used
	for _, l := range loadFakeGH(state).Log {
		_, ep, _ := strings.Cut(l, " ")
		if !strings.HasPrefix(ep, "repos/o/r/git/commits") && !strings.HasPrefix(ep, "repos/o/r/git/refs") && !strings.HasPrefix(ep, "repos/o/r/git/matching-refs/") {
			t.Errorf("unexpected request: %s", l)
		}
	}
}

func TestGHClaimHostAgeIsTheHostsOwn(t *testing.T) {
	h, state := fakeGHHost(t)
	ctx := context.Background()
	s := loadFakeGH(state)
	s.Backdate = 120
	saveFakeGH(state, s)
	if err := h.CreateRef(ctx, "o/r", RefNSAgent, "m-aaaa1111", FleetRecord{V: 1, Agent: "m-aaaa1111", State: "ready"}); err != nil {
		t.Fatal(err)
	}
	got, err := h.ListRefs(ctx, "o/r", RefNSAgent, "")
	if err != nil || len(got) != 1 {
		t.Fatalf("%+v %v", got, err)
	}
	if got[0].Age < 118*time.Second || got[0].Age > 130*time.Second {
		t.Fatalf("a record stamped two minutes ago is about two minutes old: %v", got[0].Age)
	}
}

func TestGHClaimHostRefusesOutsideItsNamespaces(t *testing.T) {
	h, state := fakeGHHost(t)
	ctx := context.Background()
	rec := FleetRecord{V: 1}
	bad := []struct{ ns, name string }{
		{"heads", "master"}, {"tags", "v1"}, {"ci-claim/x", "y"}, {"", "x"},
		{RefNSClaim, ""}, {RefNSClaim, "../heads/master"}, {RefNSClaim, "a/b"}, {RefNSClaim, "x.lock"}, {RefNSClaim, ".x"},
	}
	for _, c := range bad {
		if err := h.DeleteRef(ctx, "o/r", c.ns, c.name); err == nil {
			t.Errorf("delete of %q %q must be refused", c.ns, c.name)
		}
		if err := h.CreateRef(ctx, "o/r", c.ns, c.name, rec); err == nil {
			t.Errorf("create of %q %q must be refused", c.ns, c.name)
		}
		if err := h.UpdateRef(ctx, "o/r", c.ns, c.name, rec); err == nil {
			t.Errorf("update of %q %q must be refused", c.ns, c.name)
		}
	}
	if _, err := h.ListRefs(ctx, "o/r", "heads", ""); err == nil {
		t.Error("listing another namespace must be refused")
	}
	if log := loadFakeGH(state).Log; len(log) != 0 {
		t.Fatalf("a refused call must not reach the host: %v", log)
	}
}

func TestGHClaimHostTellsAboutRights(t *testing.T) {
	r := &ghResponse{Status: 403, Body: []byte(`{"message":"Resource not accessible by personal access token"}`)}
	err := refused(r, "create it")
	if !errors.Is(err, ErrClaimDenied) || !strings.Contains(err.Error(), "write access") {
		t.Fatalf("a refusal for want of rights is ErrClaimDenied: %v", err)
	}
	if errors.Is(refused(&ghResponse{Status: 500}, "create it"), ErrClaimDenied) {
		t.Fatal("a server error is not a missing right")
	}
}

// Two fleets over the real host code (the commits and refs of the fake gh): one wins, the other finds it busy, and
// a lease the host says is old is taken over.
func TestFleetsOverTheGHClaimHost(t *testing.T) {
	h, state := fakeGHHost(t)
	ctx := context.Background()
	a, b := newTestFleet(h, "m-aaaa1111"), newTestFleet(h, "m-bbbb2222")
	r := claimReq("r1")
	mustAcquire(t, a, r)
	d, err := b.Acquire(ctx, claimReq("r2"))
	if err != nil || d.Outcome != ClaimBusy || d.Holder != "m-aaaa1111" {
		t.Fatalf("second PC: %+v %v", d, err)
	}
	if lost := a.Refresh(ctx); len(lost) != 0 {
		t.Fatalf("refresh: %v", lost)
	}
	// the host's stamp of the claim is old now: the lease is stale
	s := loadFakeGH(state)
	for sha, c := range s.Commits {
		c.Stamp -= 600
		s.Commits[sha] = c
	}
	saveFakeGH(state, s)
	d, err = b.Acquire(ctx, claimReq("r2"))
	if err != nil || d.Outcome != ClaimWon {
		t.Fatalf("takeover of a stale lease: %+v %v", d, err)
	}
	if got := loadFakeGH(state).Refs; len(got) != 1 {
		t.Fatalf("the replaced generation is deleted: %v", got)
	}
	if lost := a.Refresh(ctx); len(lost) != 1 {
		t.Fatalf("the first PC finds out at its next refresh: %v", lost)
	}
	b.Release(ctx, claimReq("r2"))
	if got := loadFakeGH(state).Refs; len(got) != 0 {
		t.Fatalf("release deletes the claim: %v", got)
	}
}
