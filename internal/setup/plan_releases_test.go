package setup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"
)

// The releases window is an optional companion in the payload: with it, setup places it and a Start-menu link; without it, nothing
// about the install changes; uninstall removes both.

func payloadWithReleases(t *testing.T, version string) *Payload {
	t.Helper()
	m := fstest.MapFS{}
	var man Manifest
	man.Version = version
	for _, n := range []string{AppExeName, KeeperExeName, BlackboxExeName, ReleasesExeName} {
		data := []byte(n + " " + version + " " + strings.Repeat("x", 1000))
		sum := sha256.Sum256(data)
		m[n] = &fstest.MapFile{Data: data, Mode: 0o755}
		man.Files = append(man.Files, PayloadFile{Name: n, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(data))})
	}
	raw, _ := json.Marshal(man)
	m[ManifestName] = &fstest.MapFile{Data: raw}
	p, err := LoadPayload(m)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestNewLayoutHasReleasesPathsBesideTheApp(t *testing.T) {
	d := desktopTestDirs()
	for _, scope := range []Scope{ScopeUser, ScopeMachine} {
		l := NewLayout(Options{Scope: scope, Location: DefaultLocation(scope, d)}, d)
		if l.ReleasesExe == "" || l.ReleasesLink == "" || ReleasesExeName != "unravis-releases.exe" || ReleasesShortcutName != "Unravis Releases.lnk" {
			t.Fatalf("scope %v: %+v", scope, l)
		}
	}
}

func TestPayloadAcceptsTheReleasesFileAndStillNeedsTheApp(t *testing.T) {
	p := payloadWithReleases(t, "1.2.0")
	if _, ok := p.file(ReleasesExeName); !ok {
		t.Fatal("releases file not in the payload list")
	}
}

func TestInstallPlacesTheReleasesWindowAndItsLinkAndUninstallRemovesThem(t *testing.T) {
	for _, scope := range []string{"machine", "user"} {
		t.Run(scope, func(t *testing.T) { releasesRoundTrip(t, scope) })
	}
}

func releasesRoundTrip(t *testing.T, scope string) {
	{
		e := newEnv(t, "1.2.0")
		e.r.Payload = payloadWithReleases(t, "1.2.0")
		o := e.userOpts()
		if scope == "machine" {
			o = e.machineOpts()
		}
		o.Releases = true
		e.mustRun(OpInstall, o, UninstallOptions{})
		l := NewLayout(o, e.dirs)
		if !exists(l.ReleasesExe) {
			t.Fatalf("%s: %s was not placed", scope, l.ReleasesExe)
		}
		target, args := readLink(t, l.ReleasesLink)
		if !samePath(target, l.ReleasesExe) || args != "" {
			t.Errorf("%s: releases link = %q %q", scope, target, args)
		}
		e.mustRun(OpUninstall, o, UninstallOptions{})
		if exists(l.ReleasesExe) || exists(l.ReleasesLink) {
			t.Errorf("%s: releases window or link left behind", scope)
		}
	}
}

func TestInstallWithoutTheReleasesFileWritesNoReleasesLink(t *testing.T) {
	e := newEnv(t, "1.2.0")
	o := e.userOpts()
	e.mustRun(OpInstall, o, UninstallOptions{})
	l := NewLayout(o, e.dirs)
	if exists(l.ReleasesExe) || exists(l.ReleasesLink) {
		t.Fatal("an older payload must install exactly as before")
	}
}

func TestPayloadWithReleasesInstallsNothingExtraUnlessAsked(t *testing.T) {
	e := newEnv(t, "1.2.0")
	e.r.Payload = payloadWithReleases(t, "1.2.0")
	o := e.userOpts() // Releases is off
	e.mustRun(OpInstall, o, UninstallOptions{})
	l := NewLayout(o, e.dirs)
	if exists(l.ReleasesExe) || exists(l.ReleasesLink) {
		t.Fatal("a normal install must not carry the releases window")
	}
}

func TestAnInstalledReleasesWindowIsKeptOnRepairWithoutTheOption(t *testing.T) {
	e := newEnv(t, "1.2.0")
	e.r.Payload = payloadWithReleases(t, "1.2.0")
	o := e.userOpts()
	o.Releases = true
	e.mustRun(OpInstall, o, UninstallOptions{})
	l := NewLayout(o, e.dirs)
	writeFile(t, l.ReleasesExe, "damaged")
	o.Releases = false
	e.mustRun(OpRepair, o, UninstallOptions{})
	if !exists(l.ReleasesExe) || !exists(l.ReleasesLink) {
		t.Fatal("repair must keep (and restore) an installed releases window")
	}
}

func TestReleasesOptionComesFromTheSilentConfig(t *testing.T) {
	c, err := ParseSilentConfig([]byte(`{"scope":"user","releases":true}`))
	if err != nil {
		t.Fatal(err)
	}
	req, err := c.Request(OpInstall)
	if err != nil || !req.Options.Releases {
		t.Fatalf("%v %+v", err, req.Options)
	}
	c, _ = ParseSilentConfig([]byte(`{"scope":"user"}`))
	if req, _ = c.Request(OpInstall); req.Options.Releases {
		t.Fatal("off unless asked for")
	}
}
