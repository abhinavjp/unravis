// Command sigtool is the release signing tool for the auto-updater (docs/auto-updater/SPEC.md section 5).
//
//	sigtool keygen   -role stable|rc -out key.json -pub key.pub     make an encrypted key pair (offline, on the owner's PC)
//	sigtool manifest -dir dist -version V -seq N -out manifest.json  describe a release (CI runs this; it signs nothing)
//	sigtool sign     -key key.json -in manifest.json -out manifest.json.sig
//	sigtool check-assets -manifest manifest.json -dir downloaded   re-hash the real files against the manifest before signing
//	sigtool verify   -in manifest.json -sig manifest.json.sig -pub a.pub[,b.pub] [-channel stable|rc]
//
// The passphrase comes from UNRAVIS_KEY_PASSPHRASE or the first line of standard input. It is never a command-line argument.
package main

import (
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
		outFile := fs.String("out", "update-manifest.json", "file to write")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		m, err := buildManifest(*dir, *ver, *seq, *days, *minFrom, time.Now().UTC())
		if err != nil {
			return err
		}
		b, _ := json.MarshalIndent(m, "", "  ")
		if err := os.WriteFile(*outFile, append(b, '\n'), 0o644); err != nil {
			return err
		}
		fmt.Fprintf(out, "wrote %s for %s (%s channel)\n", *outFile, m.Version, m.Channel)
		return nil

	case "sign":
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
		// Check the manifest before signing it: a key never signs a manifest of the wrong channel or one that does not validate.
		var m update.Manifest
		if err := json.Unmarshal(mb, &m); err != nil {
			return fmt.Errorf("manifest: %w", err)
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
		var m update.Manifest
		if err := json.Unmarshal(mb, &m); err != nil {
			return err
		}
		if err := checkAssets(&m, *dir); err != nil {
			return err
		}
		fmt.Fprintf(out, "ok: %d file(s) match the manifest for %s\n", len(m.Assets)+1, m.Version)
		return nil

	case "verify":
		inFile := fs.String("in", "", "manifest")
		sigFile := fs.String("sig", "", "signature file")
		pubs := fs.String("pub", "", "comma-separated public key files")
		channel := fs.String("channel", update.ChannelRC, "channel of the build checking (stable or rc)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		var keys []update.Key
		for _, p := range strings.Split(*pubs, ",") {
			k, err := readPub(strings.TrimSpace(p))
			if err != nil {
				return err
			}
			keys = append(keys, k)
		}
		mb, err := os.ReadFile(*inFile)
		if err != nil {
			return err
		}
		sb, err := os.ReadFile(*sigFile)
		if err != nil {
			return err
		}
		m, err := update.Verify(mb, sb, update.Trust{Keys: keys, Channel: *channel}, time.Now())
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

func buildManifest(dir, ver string, seq uint64, days int, minFrom string, now time.Time) (*update.Manifest, error) {
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
		Seq: seq, MinUpdateFrom: minFrom, Assets: map[string]update.Asset{},
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
	return nil
}
