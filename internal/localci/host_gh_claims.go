package localci

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// GHClaimHost is the ClaimHost that keeps its records as git refs on GitHub, through `gh api`.
//
// A record is the message of a commit that has no files (it points to the well known empty tree) and no parent.
// The ref refs/<ns>/<name> points to it. The commit is made with the fixed author "ci", so no person's name goes
// into a public repository, and with no date, so GitHub stamps it with its own clock: the age of a record is that
// stamp against the Date header of the same answer, and a PC with a wrong clock cannot change it.
//
// Creating a ref that exists is refused by GitHub (HTTP 422), which is the one atomic operation the claims rest on.
// Making refs needs a token that can write the repository's contents; a token that can only post statuses cannot,
// and the fleet then fails closed (see Fleet).
type GHClaimHost struct {
	H *GHHost

	mu    sync.Mutex
	cache map[string]ghCommit // by commit id; a commit never changes
}

// NewGHClaimHost makes the claim host for h.
func NewGHClaimHost(h *GHHost) *GHClaimHost { return &GHClaimHost{H: h} }

// emptyTree is the id git gives the tree with no entries; every repository has it.
const emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

const (
	ghClaimCacheMax = 512
	ghClaimPageMax  = 10
	ghRecordMax     = 4096 // bytes of a record; a longer message is not one of ours
)

type ghCommit struct {
	Rec  FleetRecord
	When time.Time // the host's stamp
}

var _ ClaimHost = (*GHClaimHost)(nil)

func checkRef(ns, name string) error {
	if !ValidRefNS(ns) {
		return fmt.Errorf("refusing to touch refs/%s: not a ref of the fleet", ns)
	}
	if !ValidRefName(name) {
		return fmt.Errorf("refusing to touch refs/%s/%s: not a name this tool makes", ns, name)
	}
	return nil
}

// refEndpoint is the git/refs path of one ref, which is also valid for the matching-refs listing.
func refEndpoint(repo, kind, ns, name string) string {
	return "repos/" + repo + "/git/" + kind + "/" + ns + "/" + escapePath(name)
}

func (c *GHClaimHost) ListRefs(ctx context.Context, repo, ns, prefix string) ([]RefRecord, error) {
	if !ValidRefNS(ns) {
		return nil, fmt.Errorf("refusing to list refs/%s: not a ref of the fleet", ns)
	}
	if prefix != "" && !ValidRefName(prefix) {
		return nil, fmt.Errorf("refusing to list refs/%s/%s: not a name this tool makes", ns, prefix)
	}
	var out []RefRecord
	for page := 1; page <= ghClaimPageMax; page++ {
		ep := fmt.Sprintf("repos/%s/git/matching-refs/%s/%s?per_page=100&page=%d", repo, ns, escapePath(prefix), page)
		resp, err := c.H.ghCall(ctx, "GET", ep, "", nil)
		if err != nil {
			return nil, err
		}
		if resp.Status != 200 {
			return nil, resp.failed("list the " + ns + " refs of " + repo)
		}
		var v []struct {
			Ref    string `json:"ref"`
			Object struct {
				SHA string `json:"sha"`
			} `json:"object"`
		}
		if err := json.Unmarshal(resp.Body, &v); err != nil {
			return nil, fmt.Errorf("list the %s refs of %s: %w", ns, repo, err)
		}
		now := serverNow(resp)
		for _, r := range v {
			name, ok := strings.CutPrefix(r.Ref, "refs/"+ns+"/")
			if !ok || !ValidRefName(name) {
				continue // not ours
			}
			cm, err := c.commit(ctx, repo, r.Object.SHA)
			if err != nil {
				return nil, err
			}
			age := now.Sub(cm.When)
			if cm.When.IsZero() || age < 0 {
				age = 0
			}
			out = append(out, RefRecord{Name: name, Rec: cm.Rec, Age: age})
		}
		if !strings.Contains(resp.Header["link"], `rel="next"`) {
			break
		}
	}
	return out, nil
}

// serverNow is the host's clock at the time of the answer.
func serverNow(r *ghResponse) time.Time {
	if t, err := http.ParseTime(r.Header["date"]); err == nil {
		return t
	}
	return time.Now()
}

// commit reads one commit's record and stamp, from the cache when it was read before.
func (c *GHClaimHost) commit(ctx context.Context, repo, sha string) (ghCommit, error) {
	key := repo + "@" + sha
	c.mu.Lock()
	cm, ok := c.cache[key]
	c.mu.Unlock()
	if ok {
		return cm, nil
	}
	resp, err := c.H.ghCall(ctx, "GET", "repos/"+repo+"/git/commits/"+sha, "", nil)
	if err != nil {
		return ghCommit{}, err
	}
	if resp.Status != 200 {
		return ghCommit{}, resp.failed("read a claim record of " + repo)
	}
	var v struct {
		Message   string `json:"message"`
		Committer struct {
			Date time.Time `json:"date"`
		} `json:"committer"`
	}
	if err := json.Unmarshal(resp.Body, &v); err != nil {
		return ghCommit{}, fmt.Errorf("read a claim record of %s: %w", repo, err)
	}
	cm = ghCommit{When: v.Committer.Date}
	if len(v.Message) <= ghRecordMax {
		var rec FleetRecord
		if json.Unmarshal([]byte(v.Message), &rec) == nil {
			cm.Rec = rec
		}
	}
	c.mu.Lock()
	if c.cache == nil || len(c.cache) >= ghClaimCacheMax {
		c.cache = map[string]ghCommit{}
	}
	c.cache[key] = cm
	c.mu.Unlock()
	return cm, nil
}

// newCommit writes the record as a commit and returns its id.
func (c *GHClaimHost) newCommit(ctx context.Context, repo string, rec FleetRecord) (string, error) {
	msg, err := json.Marshal(rec)
	if err != nil {
		return "", err
	}
	who := map[string]string{"name": "ci", "email": "ci@localhost"}
	body, err := json.Marshal(map[string]any{"message": string(msg), "tree": emptyTree, "parents": []string{}, "author": who, "committer": who})
	if err != nil {
		return "", err
	}
	resp, err := c.H.ghCall(ctx, "POST", "repos/"+repo+"/git/commits", "", body)
	if err != nil {
		return "", err
	}
	if resp.Status != 201 {
		return "", refused(resp, "write a claim record to "+repo)
	}
	var v struct {
		SHA string `json:"sha"`
	}
	if err := json.Unmarshal(resp.Body, &v); err != nil || v.SHA == "" {
		return "", fmt.Errorf("write a claim record to %s: no commit id in the answer", repo)
	}
	return v.SHA, nil
}

// refused is the error of a write the host did not take. A refusal for want of rights (401, 403, and 404 for a
// repository the token cannot write) is ErrClaimDenied, so the caller can tell it from a network problem.
func refused(r *ghResponse, what string) error {
	err := r.failed(what)
	if r.Status == 401 || r.Status == 403 || r.Status == 404 {
		return fmt.Errorf("%w: %v; the token needs write access to the repository's contents", ErrClaimDenied, err)
	}
	return err
}

func (c *GHClaimHost) CreateRef(ctx context.Context, repo, ns, name string, rec FleetRecord) error {
	if err := checkRef(ns, name); err != nil {
		return err
	}
	sha, err := c.newCommit(ctx, repo, rec)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]string{"ref": "refs/" + ns + "/" + name, "sha": sha})
	resp, err := c.H.ghCall(ctx, "POST", "repos/"+repo+"/git/refs", "", body)
	if err != nil {
		return err
	}
	switch {
	case resp.Status == 201:
		return nil
	case resp.Status == 422 && strings.Contains(strings.ToLower(string(resp.Body)), "already exists"):
		return ErrRefExists
	}
	return refused(resp, "create refs/"+ns+"/"+name)
}

func (c *GHClaimHost) UpdateRef(ctx context.Context, repo, ns, name string, rec FleetRecord) error {
	if err := checkRef(ns, name); err != nil {
		return err
	}
	sha, err := c.newCommit(ctx, repo, rec)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]any{"sha": sha, "force": true})
	resp, err := c.H.ghCall(ctx, "PATCH", refEndpoint(repo, "refs", ns, name), "", body)
	if err != nil {
		return err
	}
	switch {
	case resp.Status == 200:
		return nil
	case resp.Status == 404 || (resp.Status == 422 && strings.Contains(strings.ToLower(string(resp.Body)), "does not exist")):
		return ErrRefGone
	}
	return resp.failed("update refs/" + ns + "/" + name)
}

func (c *GHClaimHost) DeleteRef(ctx context.Context, repo, ns, name string) error {
	if err := checkRef(ns, name); err != nil {
		return err
	}
	resp, err := c.H.ghCall(ctx, "DELETE", refEndpoint(repo, "refs", ns, name), "", nil)
	if err != nil {
		return err
	}
	switch {
	case resp.Status == 204 || resp.Status == 404:
		return nil
	case resp.Status == 422 && strings.Contains(strings.ToLower(string(resp.Body)), "does not exist"):
		return nil
	}
	return resp.failed("delete refs/" + ns + "/" + name)
}
