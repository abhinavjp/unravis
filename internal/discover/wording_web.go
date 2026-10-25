package discover

// webShowKeys are the Shows keys the web detector sets. Their sentences are in
// wording.go already, so this file adds no wording; the web tests check that each
// key resolves through ShowsText.
var webShowKeys = []string{"discover.nginx.shows", "discover.apache.shows"}
