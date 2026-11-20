package server

import (
	"context"
	"net/http"
	"testing"

	"api-audit-explorer/internal/update"
)

func TestUpdaterDoesNotOfferASkippedVersionButOffersTheNext(t *testing.T) {
	setVersion(t, "0.1.0-rc.47")
	k, priv := rcKey(t)
	feed := newFakeFeed(t)
	feed.publish(t, "rc", "0.1.0-rc.48", 4801, k, priv)
	u := testUpdater(t, feed, k)
	(&update.Tracker{StateDir: u.dir}).Skip("0.1.0-rc.48")
	h := updHandler(u)

	res := request(h, http.MethodPost, "/api/update-check", "", true)
	if s := decodeStatus(t, res.Code, res.Body.String()); s.Status == "available" {
		t.Fatalf("a skipped version was offered: %+v", s)
	}
	if u.availableManifest() != nil {
		t.Fatal("a skipped version can still be downloaded")
	}
	// A newer release is offered as usual.
	feed.publish(t, "rc", "0.1.0-rc.49", 4901, k, priv)
	r, _ := u.checkOnce(context.Background())
	if r.Status != update.StatusAvailable || r.Manifest == nil || r.Manifest.Version != "0.1.0-rc.49" {
		t.Fatalf("%+v", r)
	}
}
