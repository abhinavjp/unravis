package server

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"api-audit-explorer/internal/audit"
	"api-audit-explorer/internal/history"
	"api-audit-explorer/internal/sourcelinks"
)

// openProtector seals nothing: these tests only need a real links store on disk, not a real seal.
type openProtector struct{}

func (openProtector) Protect(v []byte) ([]byte, error)   { return append([]byte(nil), v...), nil }
func (openProtector) Unprotect(v []byte) ([]byte, error) { return append([]byte(nil), v...), nil }

func realLinkStore(t *testing.T) *sourcelinks.Store {
	t.Helper()
	s, err := sourcelinks.New(filepath.Join(t.TempDir(), "links", "sourcelinks.dat"), openProtector{})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func storedDatabases(t *testing.T, s *sourcelinks.Store) string {
	t.Helper()
	links, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(links))
	for i, l := range links {
		out[i] = l.SavedConnectionID + "/" + l.Database
	}
	return strings.Join(out, ",")
}

// SEC-09: two saved connections that share a database and collection, on sources that carry no selection key. Removing the source of
// one must leave the other connection's link, or it would not come back after a restart.
func TestRemovingASourceKeepsTheLinkOfAnotherSavedConnectionWithTheSameDatabase(t *testing.T) {
	store := realLinkStore(t)
	for _, l := range []sourcelinks.Link{
		{SavedConnectionID: "saved-1", Database: "shared", Collection: "audit", Label: "Shared audit", ConnectionLabel: "Conn one"},
		{SavedConnectionID: "saved-2", Database: "shared", Collection: "audit", Label: "Shared audit", ConnectionLabel: "Conn two"},
	} {
		if err := store.Add(l); err != nil {
			t.Fatal(err)
		}
	}
	// Only the first connection's source is open, and the repository gave it no selection key.
	repo := newReconnRepo(audit.Source{ID: "src-1", Label: "Shared audit", ConnectionLabel: "Conn one", Database: "shared", Collection: "audit"})
	h, ok := New(repo, nil, testToken, false, WithHistory(&reconnHistory{}), WithSourceLinks(store)).(*handler)
	if !ok {
		t.Fatal("New did not return the handler")
	}
	if res := request(h, http.MethodDelete, "/api/sources/src-1", "", true); res.Code != http.StatusNoContent {
		t.Fatalf("delete = %d %s", res.Code, res.Body)
	}
	if got := storedDatabases(t, store); got != "saved-2/shared" {
		t.Errorf("links after removing one connection's source = %q, want only saved-2/shared", got)
	}
}

// A failed delete puts back exactly the link it took, not a neighbour's.
func TestAFailedRemoveRestoresOnlyItsOwnLinkWhenConnectionsShareADatabase(t *testing.T) {
	store := realLinkStore(t)
	for _, l := range []sourcelinks.Link{
		{SavedConnectionID: "saved-1", Database: "shared", Collection: "audit", Label: "Shared audit", ConnectionLabel: "Conn one"},
		{SavedConnectionID: "saved-2", Database: "shared", Collection: "audit", Label: "Shared audit", ConnectionLabel: "Conn two"},
	} {
		if err := store.Add(l); err != nil {
			t.Fatal(err)
		}
	}
	repo := newReconnRepo(audit.Source{ID: "src-1", Label: "Shared audit", ConnectionLabel: "Conn one", Database: "shared", Collection: "audit"})
	repo.deleteErr = fmt.Errorf("the database refused")
	h, _ := New(repo, nil, testToken, false, WithHistory(&reconnHistory{}), WithSourceLinks(store)).(*handler)
	if res := request(h, http.MethodDelete, "/api/sources/src-1", "", true); res.Code == http.StatusNoContent {
		t.Fatalf("the delete should have failed, got %d", res.Code)
	}
	if got := storedDatabases(t, store); got != "saved-2/shared,saved-1/shared" && got != "saved-1/shared,saved-2/shared" {
		t.Errorf("links after a failed delete = %q, want both", got)
	}
}

// removingRepo hands out its source list and then runs a hook, so a test can play "the person removes a source" at the exact moment
// the pass has already decided what to reopen.
type removingRepo struct {
	*reconnRepo
	afterSources func()
}

func (r *removingRepo) Sources(ctx context.Context) ([]audit.Source, error) {
	items, err := r.reconnRepo.Sources(ctx)
	if r.afterSources != nil {
		hook := r.afterSources
		r.afterSources = nil
		hook()
	}
	return items, err
}

// SEC-08: a source the person removes while the start-up pass is running must not be reopened by that pass.
func TestReconnectDoesNotReopenASourceRemovedWhileThePassRuns(t *testing.T) {
	base := newReconnRepo()
	saved := &reconnHistory{items: []history.Recent{savedRecent("saved-1", true)}, passwords: map[string]string{"saved-1": "stored-secret"}}
	links := &reconnLinks{links: []sourcelinks.Link{linkTo("saved-1", "payroll"), linkTo("saved-1", "orders")}}
	repo := &removingRepo{reconnRepo: base}
	repo.afterSources = func() {
		// The pass now holds a list that still says payroll and orders both need opening. The person removes payroll.
		_ = links.Remove("key-payroll-audit", "payroll", "audit")
	}
	h := reconnectHandler(t, repo, saved, links)

	h.reconnectPass(context.Background(), time.Minute, nil, startupCooldown)

	for _, in := range base.adds {
		if in.Database == "payroll" {
			t.Fatalf("payroll was reopened after the person removed it: %+v", base.adds)
		}
	}
	if base.addCount() != 1 || base.adds[0].Database != "orders" {
		t.Errorf("the other saved source should still reopen; adds = %+v", base.adds)
	}
	if links.databases() != "orders" {
		t.Errorf("links = %q, want orders", links.databases())
	}
}

// SEC-10: an unescaped "/" in a password must not stop the cut at the login. The login ends at the last "@" before the path.
func TestAddressOnlyDropsAPasswordThatHoldsASlash(t *testing.T) {
	const pw = "pw/with/slash"
	for _, tc := range []struct{ in, want string }{
		{"mongodb://reader:" + pw + "@db.example:27017/admin?tls=true", "mongodb://db.example:27017/admin?tls=true"},
		{"mongodb://reader:" + pw + "@db.example", "mongodb://db.example"},
		{"mongodb+srv://reader:" + pw + "@c@db.example/admin", "mongodb+srv://db.example/admin"},
		{"mongodb://reader:pw/x@db.example/admin", "mongodb://db.example/admin"},
		// Addresses with no login keep every part, even one with an "@" in the path or the query.
		{"mongodb://db.example:27017/app@x", "mongodb://db.example:27017/app@x"},
		{"mongodb://db.example/app@x", "mongodb://db.example/app@x"},
		{"mongodb://db.example/admin?appName=a@b", "mongodb://db.example/admin?appName=a@b"},
		{"mongodb://[::1]/app@x", "mongodb://[::1]/app@x"},
		{"mongodb://h1:27017,h2:27017/app@x", "mongodb://h1:27017,h2:27017/app@x"},
		// The ordinary forms still work.
		{"mongodb://reader:s3cret@db.example:27017/admin?tls=true", "mongodb://db.example:27017/admin?tls=true"},
	} {
		got := addressOnly(tc.in)
		if got != tc.want {
			t.Errorf("addressOnly(%q) = %q, want %q", tc.in, got, tc.want)
		}
		if strings.Contains(got, "pw/") || strings.Contains(got, "slash") {
			t.Errorf("addressOnly(%q) kept part of the password: %q", tc.in, got)
		}
	}
	h := reconnectHandler(t, newReconnRepo(), &reconnHistory{}, &reconnLinks{})
	h.noteOpenedSource(audit.Source{Database: "payroll", Collection: "audit"}, "", audit.ConnectionInput{URI: "mongodb://reader:" + pw + "@db.example", Password: pw})
	if len(h.reconn.pending) != 1 || strings.Contains(fmt.Sprintf("%+v", h.reconn.pending), "pw/") || strings.Contains(fmt.Sprintf("%+v", h.reconn.pending), "slash") {
		t.Errorf("pending = %+v", h.reconn.pending)
	}
}

// BL-036 (SEC-10 remainder): a password that holds a "/" and also an unescaped "?" or "#" before the "@" is cut out too. A digits-only one
// reads exactly like a port and a path, so it is cut with the typed password as the hint.
func TestAddressOnlyDropsAPasswordWithSlashAndQueryMark(t *testing.T) {
	for _, tc := range []struct{ in, want, secret string }{
		{"mongodb://reader:ab/c?d@db.example/admin", "mongodb://db.example/admin", "ab/c"},
		{"mongodb://reader:ab/c#d@db.example", "mongodb://db.example", "ab/c"},
		{"mongodb://reader:ab/c?d@e@db.example:27017/admin?tls=true", "mongodb://db.example:27017/admin?tls=true", "ab/c"},
		// Still kept whole: no login at all.
		{"mongodb://db.example:27017/app?x=a@b", "mongodb://db.example:27017/app?x=a@b", ""},
		{"mongodb://db.example/admin#a@b", "mongodb://db.example/admin#a@b", ""},
	} {
		got := addressOnly(tc.in)
		if got != tc.want {
			t.Errorf("addressOnly(%q) = %q, want %q", tc.in, got, tc.want)
		}
		if tc.secret != "" && strings.Contains(got, tc.secret) {
			t.Errorf("addressOnly(%q) kept part of the password: %q", tc.in, got)
		}
	}
	for _, tc := range []struct{ in, pw, want string }{
		{"mongodb://reader:12/34?56@db.example/admin", "12/34?56", "mongodb://db.example/admin"},
		{"mongodb://reader:1234/5678@db.example", "1234/5678", "mongodb://db.example"},
		{"mongodb://reader:12%2F34%3F56@db.example/admin", "12/34?56", "mongodb://db.example/admin"},
		// The hint never cuts an address the password is not part of.
		{"mongodb://db.example:27017/app@x", "27017", "mongodb://db.example:27017/app@x"},
	} {
		if got := addressWithout(tc.in, tc.pw); got != tc.want {
			t.Errorf("addressWithout(%q, %q) = %q, want %q", tc.in, tc.pw, got, tc.want)
		}
	}
	h := reconnectHandler(t, newReconnRepo(), &reconnHistory{}, &reconnLinks{})
	h.noteOpenedSource(audit.Source{Database: "payroll", Collection: "audit"}, "", audit.ConnectionInput{URI: "mongodb://reader:12/34?56@db.example", Password: "12/34?56"})
	if got := fmt.Sprintf("%+v", h.reconn.pending); strings.Contains(got, "12/34") || strings.Contains(got, "?56") {
		t.Errorf("pending kept the password: %s", got)
	}
}
