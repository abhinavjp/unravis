// Command sigtool is the release signing tool for the auto-updater (docs/auto-updater/SPEC.md section 5).
//
//	sigtool keygen   -role stable|rc -out key.json -pub key.pub     make an encrypted key pair (offline, on the owner's PC)
//	sigtool manifest -dir dist -version V -seq N -out manifest.json  describe a release (CI runs this; it signs nothing)
//	sigtool sign     -key key.json -in manifest.json -out manifest.json.sig
//	sigtool feedsig  -in rc.json -sig rc.json.sig -trusted|-keys K -want C   signature only (ignores expiry): which version the feed really names
//	sigtool controlsig -in c.json -sig c.json.sig -keys K[,K2] -want C   control file signature check that ignores expiry; prints its expiry
//	sigtool keycheck -key key.json [-role rc|stable]   check the passphrase opens the key (signs nothing, writes nothing)
//	sigtool refresh  -in rc.json -sig rc.json.sig -trusted -want C -version V -commit SHA -days D -out new.json   same release, later expiry, seq + 1 (then sign it)
//	sigtool control  -channel C [-pause] [-block V,..] [-rollout V -percent N] [-note T] [-days D] -out control.json
//	sigtool sign     -control -key key.json -in control.json -out control.json.sig   (a control file: pause, withdraw, staged rollout)
//	sigtool check-assets -manifest manifest.json -dir downloaded   re-hash the real files against the manifest before signing
//	sigtool preflight -manifest m.json -tag vX [-feed current.json]  sanity-check a manifest against its tag and the live feed
//	sigtool verify   -in manifest.json -sig manifest.json.sig -pub a.pub[,b.pub] [-channel stable|rc]
//
// The passphrase comes from UNRAVIS_KEY_PASSPHRASE or the first line of standard input. It is never a command-line argument.
package main

import (
	"archive/zip"
	"bufio"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"api-audit-explorer/internal/update"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "sigtool:", err)
		os.Exit(1)
	}
}

func passphrase(in io.Reader) (string, error) {
	if p := os.Getenv("UNRAVIS_KEY_PASSPHRASE"); p != "" {
		return p, nil
	}
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		return "", errors.New("no passphrase: set UNRAVIS_KEY_PASSPHRASE or pipe it on standard input")
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func run(args []string, in io.Reader, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: sigtool keygen|manifest|sign|verify (see the file header)")
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	switch args[0] {
	case "keygen":
		role := fs.String("role", "", "stable or rc")
		keyOut := fs.String("out", "", "encrypted private key file to write")
		pubOut := fs.String("pub", "", "public key file to write")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *role != update.ChannelStable && *role != update.ChannelRC {
			return errors.New("-role must be stable or rc")
		}
		if *keyOut == "" || *pubOut == "" {
			return errors.New("-out and -pub are required")
		}
		pass, err := passphrase(in)
		if err != nil {
			return err
		}
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		blob, err := sealKey(*role, priv, pass, pbkdf2Rounds)
		if err != nil {
			return err
		}
		if err := writeNew(*keyOut, blob, 0o600); err != nil {
			return err
		}
		if err := writeNew(*pubOut, []byte(*role+" "+hex.EncodeToString(pub)+"\n"), 0o644); err != nil {
			return err
		}
		fmt.Fprintf(out, "role %s, key id %s\nBack up %s on two USB sticks. Never put it in the repository or CI.\n", *role, update.KeyID(pub), *keyOut)
		return nil

	case "manifest":
		dir := fs.String("dir", "dist", "folder holding the release files")
		ver := fs.String("version", "", "version without the v, e.g. 0.1.0-rc.48")
		seq := fs.Uint64("seq", 0, "ever-rising number (CI run number)")
		days := fs.Int("days", 180, "days until the manifest expires")
		minFrom := fs.String("min-update-from", "", "oldest version that may update straight to this one")
		commit := fs.String("commit", "", "git commit the release was built from (40 hex)")
		outFile := fs.String("out", "update-manifest.json", "file to write")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		m, err := buildManifest(*dir, *ver, *seq, *days, *minFrom, *commit, time.Now().UTC())
		if err != nil {
			return err
		}
		b, _ := json.MarshalIndent(m, "", "  ")
		if err := os.WriteFile(*outFile, append(b, '\n'), 0o644); err != nil {
			return err
		}
		fmt.Fprintf(out, "wrote %s for %s (%s channel)\n", *outFile, m.Version, m.Channel)
		return nil

	case "control":
		channel := fs.String("channel", "", "channel the file is for: stable or rc")
		pause := fs.Bool("pause", false, "offer nothing on this channel")
		block := fs.String("block", "", "comma-separated versions to withdraw")
		rollV := fs.String("rollout", "", "version to roll out in stages")
		pct := fs.Int("percent", 0, "share of machines that may take -rollout (1 to 100)")
		note := fs.String("note", "", "short plain words shown to the person")
		days := fs.Int("days", 30, "days until the file expires (at most 90)")
		outFile := fs.String("out", "control.json", "file to write")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		t := time.Now().UTC().Truncate(time.Second)
		c := update.Control{Schema: update.ControlSchema, Product: update.Product, Channel: *channel, Seq: uint64(t.Unix()),
			Published: t, Expires: t.Add(time.Duration(*days) * 24 * time.Hour), Pause: *pause, Blocked: []string{},
			RolloutVersion: strings.TrimSpace(*rollV), RolloutPercent: *pct, Note: *note}
		for _, v := range strings.Split(*block, ",") {
			if v = strings.TrimSpace(v); v != "" {
				c.Blocked = append(c.Blocked, v)
			}
		}
		if err := c.Validate(t); err != nil {
			return err
		}
		b, _ := json.MarshalIndent(c, "", "  ")
		if err := os.WriteFile(*outFile, append(b, '\n'), 0o644); err != nil {
			return err
		}
		fmt.Fprintf(out, "wrote %s for the %s channel (pause=%v, blocked=%d, rollout=%q %d%%)\n", *outFile, c.Channel, c.Pause, len(c.Blocked), c.RolloutVersion, c.RolloutPercent)
		return nil

	case "refresh":
		inFile := fs.String("in", "", "the manifest now on the feed")
		sigFile := fs.String("sig", "", "its signature file (checked before anything is copied)")
		trusted := fs.Bool("trusted", false, "check it against the keys built into this source tree")
		keysFile := fs.String("keys", "", "or against a trusted_keys.txt file")
		days := fs.Int("days", 180, "days until the new copy expires")
		outFile := fs.String("out", "", "file to write")
		want := fs.String("want", "", "channel the file must be for: stable or rc (required)")
		wantVer := fs.String("version", "", "version the file must be for (required)")
		wantCommit := fs.String("commit", "", "git commit the file must name (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if (*want != update.ChannelStable && *want != update.ChannelRC) || *wantVer == "" || *wantCommit == "" {
			return errors.New("refresh needs -want, -version and -commit, so it only refreshes the release you mean")
		}
		var keys []update.Key
		var err error
		switch {
		case *trusted:
			keys, err = update.TrustedKeys()
		case *keysFile != "":
			var kb []byte
			if kb, err = os.ReadFile(*keysFile); err == nil {
				keys, err = update.ParseKeys(string(kb))
			}
		default:
			err = errors.New("pass -trusted or -keys: the old file must be checked against real keys")
		}
		if err != nil {
			return err
		}
		mb, err := os.ReadFile(*inFile)
		if err != nil {
			return err
		}
		sb, err := os.ReadFile(*sigFile)
		if err != nil {
			return err
		}
		old, err := update.VerifySignatureOnly(mb, sb, update.Trust{Keys: keys})
		if err != nil {
			return fmt.Errorf("the manifest on the feed does not verify: %w", err)
		}
		if old.Channel != *want || old.Version != *wantVer || old.Commit != *wantCommit {
			return fmt.Errorf("the file on the feed is %s %s (commit %s), not %s %s (commit %s)", old.Channel, old.Version, old.Commit, *want, *wantVer, *wantCommit)
		}
		now := time.Now().UTC().Truncate(time.Second)
		nw := *old
		nw.Published, nw.Expires, nw.Seq = now, now.Add(time.Duration(*days)*24*time.Hour), old.Seq+1
		if old.Published.After(now) {
			return errors.New("the manifest on the feed was published in the future; check this PC's clock")
		}
		if err := nw.Validate(now); err != nil {
			return err
		}
		b, _ := json.MarshalIndent(&nw, "", "  ")
		if err := os.WriteFile(*outFile, append(b, '\n'), 0o644); err != nil {
			return err
		}
		fmt.Fprintf(out, "wrote %s: %s again, seq %d -> %d, expires %s\n", *outFile, nw.Version, old.Seq, nw.Seq, nw.Expires.Format("2006-01-02"))
		return nil

	case "feedsig":
		// The release scripts take the version on the feed as the baseline for their gates. A token holder could edit that JSON, so
		// its signature is checked first. Expiry is ignored on purpose: an expired feed still names its release truly.
		inFile := fs.String("in", "", "the manifest on the feed")
		sigFile := fs.String("sig", "", "its signature file")
		trusted := fs.Bool("trusted", false, "check against the keys built into this source tree")
		keysFile := fs.String("keys", "", "or against a trusted_keys.txt file")
		want := fs.String("want", "", "channel the file must be for: stable or rc (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *want != update.ChannelStable && *want != update.ChannelRC {
			return errors.New("-want must be stable or rc")
		}
		var keys []update.Key
		var err error
		switch {
		case *trusted:
			keys, err = update.TrustedKeys()
		case *keysFile != "":
			var kb []byte
			if kb, err = os.ReadFile(*keysFile); err == nil {
				keys, err = update.ParseKeys(string(kb))
			}
		default:
			err = errors.New("pass -trusted or -keys")
		}
		if err != nil {
			return err
		}
		mb, err := os.ReadFile(*inFile)
		if err != nil {
			return err
		}
		sb, err := os.ReadFile(*sigFile)
		if err != nil {
			return err
		}
		m, err := update.VerifySignatureOnly(mb, sb, update.Trust{Keys: keys})
		if err != nil {
			return fmt.Errorf("the feed file does not verify: %w", err)
		}
		if m.Channel != *want {
			return fmt.Errorf("the feed file is for %s, not %s", m.Channel, *want)
		}
		fmt.Fprintf(out, "%s\n", m.Version)
		return nil

	case "controlsig":
		// Set-Control -KeepInForce must know whether the control file on the feed is genuine before it reads anything from it,
		// expiry included (an edited date must not make real holds look expired). The signature is checked at the file's own
		// publish time, so an expired but genuine file still verifies; its expiry is then read from the verified content.
		inFile := fs.String("in", "", "control file")
		sigFile := fs.String("sig", "", "its signature file")
		keysFiles := fs.String("keys", "", "comma-separated trusted_keys.txt files; any of them may hold the signing key")
		want := fs.String("want", "", "channel the file must be for: stable or rc (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		var keys []update.Key
		for _, kf := range strings.Split(*keysFiles, ",") {
			if kf = strings.TrimSpace(kf); kf == "" {
				continue
			}
			kb, err := os.ReadFile(kf)
			if err != nil {
				return err
			}
			k, err := update.ParseKeys(string(kb))
			if err != nil {
				return err
			}
			keys = append(keys, k...)
		}
		if len(keys) == 0 {
			return errors.New("pass -keys")
		}
		body, err := os.ReadFile(*inFile)
		if err != nil {
			return err
		}
		sb, err := os.ReadFile(*sigFile)
		if err != nil {
			return err
		}
		pre, err := update.ParseControl(body)
		if err != nil {
			return err
		}
		c, err := update.VerifyControl(body, sb, update.Trust{Keys: keys, Channel: *want, Want: *want}, pre.Published.Add(time.Second))
		if err != nil {
			return fmt.Errorf("the control file does not verify: %w", err)
		}
		fmt.Fprintf(out, "%s\n", c.Expires.UTC().Format(time.RFC3339))
		return nil

	case "keycheck":
		// The release manager runs this before anything is tagged, so a wrong passphrase or key is found while nothing has changed.
		keyPath := fs.String("key", "", "encrypted private key file")
		wantRole := fs.String("role", "", "the channel the key must be for (optional)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		pass, err := passphrase(in)
		if err != nil {
			return err
		}
		kb, err := os.ReadFile(*keyPath)
		if err != nil {
			return err
		}
		role, priv, err := openKey(kb, pass)
		if err != nil {
			return err
		}
		if *wantRole != "" && role != *wantRole {
			return fmt.Errorf("this is a %s key, not %s", role, *wantRole)
		}
		pub := priv.Public().(ed25519.PublicKey)
		fmt.Fprintf(out, "ok: %s key %s %s\n", role, update.KeyID(pub), hex.EncodeToString(pub))
		return nil

	case "sign":
		isControl := fs.Bool("control", false, "the input is a control file, not a manifest")
		keyPath := fs.String("key", "", "encrypted private key file")
		inFile := fs.String("in", "", "manifest to sign")
		outFile := fs.String("out", "", "signature file to write")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		pass, err := passphrase(in)
		if err != nil {
			return err
		}
		kb, err := os.ReadFile(*keyPath)
		if err != nil {
			return err
		}
		role, priv, err := openKey(kb, pass)
		if err != nil {
			return err
		}
		mb, err := os.ReadFile(*inFile)
		if err != nil {
			return err
		}
		if *isControl {
			c, err := update.ParseControl(mb)
			if err != nil {
				return fmt.Errorf("refusing to sign: %w", err)
			}
			if err := c.Validate(time.Now()); err != nil {
				return fmt.Errorf("refusing to sign: %w", err)
			}
			if c.Channel != role {
				return fmt.Errorf("refusing to sign: this is a %s key but the control file is for the %s channel", role, c.Channel)
			}
			pub := priv.Public().(ed25519.PublicKey)
			if err := os.WriteFile(*outFile, []byte(update.FormatSignature(pub, ed25519.Sign(priv, mb))), 0o644); err != nil {
				return err
			}
			fmt.Fprintf(out, "signed the %s control file (seq %d) with key %s\n", c.Channel, c.Seq, update.KeyID(pub))
			return nil
		}
		// Check the manifest before signing it: a key never signs a manifest of the wrong channel or one that does not validate.
		m, err := update.ParseManifest(mb) // the same strict reader the programs use: it will not sign what they would refuse
		if err != nil {
			return fmt.Errorf("refusing to sign: %w", err)
		}
		if err := m.Validate(time.Now()); err != nil {
			return fmt.Errorf("refusing to sign: %w", err)
		}
		if m.Channel != role {
			return fmt.Errorf("refusing to sign: this is a %s key but the manifest is for the %s channel", role, m.Channel)
		}
		sig := ed25519.Sign(priv, mb)
		pub := priv.Public().(ed25519.PublicKey)
		if err := os.WriteFile(*outFile, []byte(update.FormatSignature(pub, sig)), 0o644); err != nil {
			return err
		}
		fmt.Fprintf(out, "signed %s with key %s\n", m.Version, update.KeyID(pub))
		return nil

	case "check-assets":
		mf := fs.String("manifest", "", "manifest to check")
		dir := fs.String("dir", "", "folder holding the downloaded release files")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		mb, err := os.ReadFile(*mf)
		if err != nil {
			return err
		}
		m, err := update.ParseManifest(mb)
		if err != nil {
			return err
		}
		if err := m.Validate(time.Now()); err != nil {
			return err
		}
		if err := checkAssets(m, *dir); err != nil {
			return err
		}
		fmt.Fprintf(out, "ok: %d release file(s) and the Blackbox exe inside its zip match the manifest for %s\n", len(m.Assets), m.Version)
		return nil

	case "preflight":
		mf := fs.String("manifest", "", "manifest about to be signed or published")
		tag := fs.String("tag", "", "release tag, e.g. v0.1.0-rc.48")
		feed := fs.String("feed", "", "the manifest the feed holds now (optional)")
		commit := fs.String("commit", "", "the commit the local tag points at (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return preflight(*mf, *tag, *commit, *feed, out)

	case "verify":
		isControl := fs.Bool("control", false, "the input is a control file")
		inFile := fs.String("in", "", "manifest")
		sigFile := fs.String("sig", "", "signature file")
		pubs := fs.String("pub", "", "comma-separated public key files (or use -trusted)")
		trusted := fs.Bool("trusted", false, "use the keys built into this source tree (internal/update/trusted_keys.txt) instead of -pub")
		keysFile := fs.String("keys", "", "a trusted_keys.txt file (for example from an older tag) instead of -pub")
		channel := fs.String("channel", "", "channel of the build checking: stable or rc (required)")
		want := fs.String("want", "", "channel the file must be for: stable or rc (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		var keys []update.Key
		if *trusted {
			var err error
			if keys, err = update.TrustedKeys(); err != nil {
				return err
			}
		} else if *keysFile != "" {
			kb, err := os.ReadFile(*keysFile)
			if err != nil {
				return err
			}
			if keys, err = update.ParseKeys(string(kb)); err != nil {
				return err
			}
		} else {
			for _, p := range strings.Split(*pubs, ",") {
				k, err := readPub(strings.TrimSpace(p))
				if err != nil {
					return err
				}
				keys = append(keys, k)
			}
		}
		mb, err := os.ReadFile(*inFile)
		if err != nil {
			return err
		}
		sb, err := os.ReadFile(*sigFile)
		if err != nil {
			return err
		}
		if *isControl {
			c, err := update.VerifyControl(mb, sb, update.Trust{Keys: keys, Channel: *channel, Want: *want}, time.Now())
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "ok: control file for %s, seq %d\n", c.Channel, c.Seq)
			return nil
		}
		m, err := update.Verify(mb, sb, update.Trust{Keys: keys, Channel: *channel, Want: *want}, time.Now())
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "ok: %s on channel %s, seq %d\n", m.Version, m.Channel, m.Seq)
		return nil
	}
	return fmt.Errorf("unknown command %q", args[0])
}

func writeNew(path string, b []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("%s: %w (it will not overwrite an existing file)", path, err)
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func readPub(path string) (update.Key, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return update.Key{}, err
	}
	f := strings.Fields(string(b))
	if len(f) != 2 {
		return update.Key{}, fmt.Errorf("%s: not a public key file", path)
	}
	raw, err := hex.DecodeString(f[1])
	if err != nil || len(raw) != ed25519.PublicKeySize {
		return update.Key{}, fmt.Errorf("%s: bad public key", path)
	}
	return update.Key{Role: f[0], Pub: raw}, nil
}

func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func buildManifest(dir, ver string, seq uint64, days int, minFrom, commit string, now time.Time) (*update.Manifest, error) {
	v, err := update.ParseVersion(ver)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(ver, "v") {
		return nil, errors.New("give the version without the leading v")
	}
	m := &update.Manifest{
		Schema: update.SchemaVersion, Product: update.Product, Version: v.String(), Channel: update.ChannelOf(v),
		Published: now.Truncate(time.Second), Expires: now.Add(time.Duration(days) * 24 * time.Hour).Truncate(time.Second),
		Seq: seq, Commit: commit, MinUpdateFrom: minFrom, Assets: map[string]update.Asset{},
	}
	files := map[string]string{
		update.AssetSetup:    "unravis-setup-v" + ver + "-windows-x64.exe",
		update.AssetApp:      "unravis-v" + ver + "-windows-x64.zip",
		update.AssetBlackbox: "unravis-blackbox-v" + ver + "-windows-x64.zip",
		update.AssetLite:     "unravis-lite-v" + ver + "-windows-x64.zip", // optional until Unravis Lite exists
	}
	for key, name := range files {
		sum, size, err := hashFile(filepath.Join(dir, name))
		if err != nil {
			if key == update.AssetLite && errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}
		m.Assets[key] = update.Asset{Name: name, SHA256: sum, Size: size}
	}
	m.BlackboxExeSHA256, _, err = hashFile(filepath.Join(dir, "unravis-blackbox.exe"))
	if err != nil {
		return nil, err
	}
	if err := m.Validate(now); err != nil {
		return nil, err
	}
	return m, nil
}

// checkAssets hashes the real files and compares them with the manifest. The owner runs it before signing, so a manifest
// is never signed on trust: what is signed is what is about to be published.
func checkAssets(m *update.Manifest, dir string) error {
	for key, a := range m.Assets {
		sum, size, err := hashFile(filepath.Join(dir, a.Name))
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		if sum != a.SHA256 || size != a.Size {
			return fmt.Errorf("%s (%s) does not match the manifest", key, a.Name)
		}
	}
	// blackboxExeSha256 is what the app pins the helper to, so it is checked against the exe inside the Blackbox zip.
	zr, err := zip.OpenReader(filepath.Join(dir, m.Assets[update.AssetBlackbox].Name))
	if err != nil {
		return fmt.Errorf("blackbox zip: %w", err)
	}
	defer zr.Close()
	found := 0
	for _, f := range zr.File {
		if strings.EqualFold(f.Name, "unravis-blackbox.exe") {
			found++
		}
	}
	if found > 1 {
		return errors.New("the Blackbox zip has more than one unravis-blackbox.exe")
	}
	for _, f := range zr.File {
		if f.Name != "unravis-blackbox.exe" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		defer rc.Close()
		h := sha256.New()
		if _, err := io.Copy(h, io.LimitReader(rc, update.MaxAssetSize+1)); err != nil {
			return err
		}
		if hex.EncodeToString(h.Sum(nil)) != m.BlackboxExeSHA256 {
			return errors.New("blackboxExeSha256 does not match the exe inside the Blackbox zip")
		}
		return nil
	}
	return errors.New("the Blackbox zip has no unravis-blackbox.exe")
}

// preflight stops the owner signing a manifest that is not what the tag says, or that would not move the feed forward.
// It prints the facts a person should look at. The feed copy is only a guard here: it is unverified and a wrong one can
// only make this check stricter or looser, never make a bad manifest installable (the programs verify the signature themselves).
func preflight(manifestPath, tag, commit, feedPath string, out io.Writer) error {
	mb, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	m, err := update.ParseManifest(mb)
	if err != nil {
		return err
	}
	if err := m.Validate(time.Now()); err != nil {
		return err
	}
	if m.Commit != commit {
		return fmt.Errorf("the manifest was built from commit %s but the tag %s points at %s: the draft's files are not from the commit you trust", m.Commit, tag, commit)
	}
	if left := time.Until(m.Expires); left < 60*24*time.Hour {
		return fmt.Errorf("the manifest expires in %d days: build the release again", int(left.Hours()/24))
	}
	if "v"+m.Version != tag {
		return fmt.Errorf("the manifest is for %s but the tag is %s", m.Version, tag)
	}
	want := map[string]string{
		update.AssetSetup:    "unravis-setup-" + tag + "-windows-x64.exe",
		update.AssetApp:      "unravis-" + tag + "-windows-x64.zip",
		update.AssetBlackbox: "unravis-blackbox-" + tag + "-windows-x64.zip",
		update.AssetLite:     "unravis-lite-" + tag + "-windows-x64.zip",
	}
	for k, a := range m.Assets {
		if a.Name != want[k] {
			return fmt.Errorf("asset %s is named %q, expected %q", k, a.Name, want[k])
		}
	}
	fmt.Fprintf(out, "commit %s\nversion %s, channel %s, seq %d\npublished %s, expires %s, minUpdateFrom %q\n",
		m.Commit, m.Version, m.Channel, m.Seq, m.Published.Format(time.RFC3339), m.Expires.Format(time.RFC3339), m.MinUpdateFrom)
	for _, k := range []string{update.AssetSetup, update.AssetApp, update.AssetBlackbox, update.AssetLite} {
		if a, ok := m.Assets[k]; ok {
			fmt.Fprintf(out, "  %-8s %s  %d bytes\n", k, a.Name, a.Size)
		}
	}
	if feedPath != "" {
		fb, err := os.ReadFile(feedPath)
		if err != nil {
			return err
		}
		cur, err := update.ParseManifest(fb)
		if err != nil {
			return fmt.Errorf("the feed's current manifest: %w", err)
		}
		cv, _ := update.ParseVersion(cur.Version)
		nv, _ := update.ParseVersion(m.Version)
		if m.Channel != cur.Channel || m.Seq <= cur.Seq || update.Compare(nv, cv) <= 0 {
			return fmt.Errorf("this would not move the feed forward (feed has %s, seq %d)", cur.Version, cur.Seq)
		}
		fmt.Fprintf(out, "feed now has %s (seq %d): this moves it forward\n", cur.Version, cur.Seq)
	}
	return nil
}
