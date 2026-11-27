package saltmaster

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"api-audit-explorer/internal/remote/transport"
)

// fakeMaster plays the jump host: it records each command and answers like `salt` would for one Linux minion that holds one file.
type fakeMaster struct {
	cmds   []string
	id     string
	file   string // path of the one file
	data   []byte
	link   string // when set, realpath answers with this instead
	down   bool
	garble bool // chunk answers are an error text
	short  bool // chunk answers lose their last byte
}

func (f *fakeMaster) reply(v any) []byte {
	b, _ := json.Marshal(map[string]any{f.id: v})
	return b
}

var (
	reTail = regexp.MustCompile(`dd if='([^']*)' iflag=nofollow,skip_bytes,count_bytes skip=(\d+) count=(\d+) status=none \| base64 -w0`)
)

func (f *fakeMaster) Exec(_ context.Context, cmd string, _ int) ([]byte, error) {
	f.cmds = append(f.cmds, cmd)
	switch {
	case strings.Contains(cmd, "salt-key"):
		return []byte(`{"minions":["web-01","db-01","bad id"],"minions_pre":["new-01"],"minions_denied":[],"minions_rejected":["old-01"]}`), nil
	case strings.Contains(cmd, "salt-run"):
		return []byte("[WARNING] something\n" + `{"up":["web-01"],"down":["db-01"]}`), nil
	}
	if f.down {
		return f.reply("Minion did not return. [Not connected]"), nil
	}
	switch {
	case strings.Contains(cmd, "'test.ping'"):
		return f.reply(true), nil
	case strings.Contains(cmd, "'grains.item'"):
		return f.reply(map[string]string{"kernel": "Linux", "osfullname": "Ubuntu", "osrelease": "22.04"}), nil
	case strings.Contains(cmd, "'date +%s'"):
		return f.reply("1700000000"), nil
	case strings.Contains(cmd, "realpath -e"):
		if f.link != "" {
			return f.reply(f.link), nil
		}
		m := regexp.MustCompile(`realpath -e -- '\\''([^']*)'\\''`).FindStringSubmatch(cmd)
		return f.reply(m[1]), nil
	case strings.Contains(cmd, "ls -l"):
		return f.reply("total 8\n-rw-r--r-- 1 root root 12 1700000100 a b.log\ndrwxr-xr-x 2 root root 4096 1700000100 sub\n-rw-r--r-- 1 root root 5 1700000200 c.log"), nil
	case strings.Contains(cmd, "stat -c %s"):
		return f.reply(strconv.Itoa(len(f.data))), nil
	case strings.Contains(cmd, "sha256sum"):
		h := sha256.Sum256(f.data)
		return f.reply(hex.EncodeToString(h[:]) + "  " + f.file), nil
	case strings.Contains(cmd, "base64 -w0"):
		if f.garble {
			return f.reply("sudo: a password is required"), nil
		}
		flat := strings.ReplaceAll(cmd, `'\''`, "'")
		m := reTail.FindStringSubmatch(flat)
		if m == nil {
			return nil, errors.New("fake: unexpected chunk command: " + cmd)
		}
		lo, _ := strconv.Atoi(m[2])
		n, _ := strconv.Atoi(m[3])
		hi := lo + n
		if hi > len(f.data) {
			hi = len(f.data)
		}
		chunk := f.data[lo:hi]
		if f.short && len(chunk) > 0 {
			chunk = chunk[:len(chunk)-1]
		}
		return f.reply(base64.StdEncoding.EncodeToString(chunk)), nil
	}
	return nil, errors.New("fake: unexpected command: " + cmd)
}

func newFake(size int) *fakeMaster {
	d := make([]byte, size)
	for i := range d {
		d[i] = byte(i*7 + 3) // every byte value occurs: a text-only path would corrupt it
	}
	return &fakeMaster{id: "web-01", file: "/var/log/blackbox/s.zip", data: d}
}

func master(f *fakeMaster) Master {
	return Master{R: f, OS: "linux", Sudo: true, ChunkBytes: 1000}
}

func TestMachinesListsKeysAndOnline(t *testing.T) {
	f := newFake(1)
	got, err := master(f).Machines(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"db-01": "accepted", "web-01": "accepted", "new-01": "pending", "old-01": "rejected"}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for _, m := range got {
		if want[m.ID] != m.State {
			t.Fatalf("%s: %s", m.ID, m.State)
		}
		if m.State == "accepted" && m.Online == nil {
			t.Fatalf("%s: online unknown", m.ID)
		}
		if m.State != "accepted" && m.Online != nil {
			t.Fatalf("%s: online shown for a key that is not accepted", m.ID)
		}
		if m.ID == "web-01" && !*m.Online || m.ID == "db-01" && *m.Online {
			t.Fatalf("%s: wrong online state", m.ID)
		}
	}
	if !strings.HasPrefix(f.cmds[0], "sudo -n salt-key --out=json --no-color '-L'") {
		t.Fatalf("command shape: %s", f.cmds[0])
	}
	if strings.Contains(f.cmds[0], "--static") {
		t.Fatal("salt-key takes no --static")
	}
}

func TestOnlyExactMinionIDs(t *testing.T) {
	f := newFake(1)
	m := master(f)
	for _, id := range []string{"*", "web-*", "web-01,db-01", "G@os:Linux", "web 01", "-t", "web-01'; rm -rf /; '"} {
		if err := m.Ping(context.Background(), id); err == nil {
			t.Fatalf("%q was accepted", id)
		}
		if _, err := m.For(id, "linux", nil); err == nil {
			t.Fatalf("For(%q) was accepted", id)
		}
	}
	if len(f.cmds) != 0 {
		t.Fatalf("a refused id reached the master: %v", f.cmds)
	}
}

func TestCommandShapeAndQuoting(t *testing.T) {
	f := newFake(1)
	m := master(f)
	if err := m.Ping(context.Background(), "web-01"); err != nil {
		t.Fatal(err)
	}
	if f.cmds[0] != "sudo -n salt --out=json --no-color --static -t 30 'web-01' 'test.ping'" {
		t.Fatalf("got %s", f.cmds[0])
	}
	w := Master{R: f, OS: "windows"}
	c, err := w.command("salt", 30, "web-01", "cmd.run", "dir D:\\x")
	if err != nil || c != `salt --out=json --no-color --static -t 30 "web-01" "cmd.run" "dir D:\x"` {
		t.Fatalf("windows jump host: %q %v", c, err)
	}
	if _, err := w.command("salt", 30, `a"b`); err == nil {
		t.Fatal("a double quote reached a Windows command line")
	}
	if _, err := w.command("salt", 30, "%PATH%"); err == nil {
		t.Fatal("a percent sign reached a Windows command line")
	}
	c, _ = m.command("salt", 30, "it's")
	if !strings.HasSuffix(c, `'it'\''s'`) {
		t.Fatalf("quote escaping: %s", c)
	}
}

func TestPingOfflineAndIdentify(t *testing.T) {
	f := newFake(1)
	m := master(f)
	idn, err := m.Identify(context.Background(), "web-01")
	if err != nil || idn.OS != "linux" || idn.Description != "Ubuntu 22.04" {
		t.Fatalf("%+v %v", idn, err)
	}
	f.down = true
	if err := m.Ping(context.Background(), "web-01"); !errors.Is(err, ErrOffline) {
		t.Fatalf("offline ping: %v", err)
	}
}

func TestListParsesAndSkipsFolders(t *testing.T) {
	f := newFake(1)
	tr, _ := master(f).For("web-01", "linux", []string{"/var/log/blackbox"})
	files, err := tr.List(context.Background(), "/var/log/blackbox")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 || files[0].Name != "a b.log" || files[0].Size != 12 || files[1].Name != "c.log" || files[1].MTime.Unix() != 1700000200 {
		t.Fatalf("%+v", files)
	}
}

func TestPathsOutsideAllowedNeverReachTheMaster(t *testing.T) {
	f := newFake(10)
	tr, _ := master(f).For("web-01", "linux", []string{"/var/log/blackbox"})
	for _, p := range []string{"/etc/passwd", "/var/log/blackbox/../../etc/passwd", "/var/log/blackbox/*", "/var/log/blackbox2/x", `/var/log/blackbox\x`, "relative.log", "/var/log/blackbox/a'b", "/var/log/blackbox/a;b", "/var/log/blackbox/$(id)"} {
		if _, err := tr.List(context.Background(), p); !errors.Is(err, transport.ErrNotAllowed) {
			t.Fatalf("List %q: %v", p, err)
		}
		if _, err := tr.Open(context.Background(), p, 0); !errors.Is(err, transport.ErrNotAllowed) {
			t.Fatalf("Open %q: %v", p, err)
		}
		if _, err := tr.Digest(context.Background(), p); !errors.Is(err, transport.ErrNotAllowed) {
			t.Fatalf("Digest %q: %v", p, err)
		}
	}
	if len(f.cmds) != 0 {
		t.Fatalf("a refused path reached the master: %v", f.cmds)
	}
}

func TestLinkOutOfAllowedFolderIsRefused(t *testing.T) {
	f := newFake(10)
	f.file = "/var/log/blackbox/s.zip"
	f.link = "/etc/shadow"
	tr, _ := master(f).For("web-01", "linux", []string{"/var/log/blackbox"})
	if _, err := tr.Open(context.Background(), f.file, 0); !errors.Is(err, transport.ErrNotAllowed) {
		t.Fatalf("a link to /etc/shadow: %v", err)
	}
	if _, err := tr.Digest(context.Background(), f.file); !errors.Is(err, transport.ErrNotAllowed) {
		t.Fatalf("digest through a link: %v", err)
	}
}

func readAll(t *testing.T, tr *Transport, path string, off int64) ([]byte, error) {
	t.Helper()
	rc, err := tr.Open(context.Background(), path, off)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

func TestOpenStreamsBinaryInChunksAndResumes(t *testing.T) {
	f := newFake(5*1000 + 337) // five full chunks and a partial one
	tr, _ := master(f).For("web-01", "linux", []string{"/var/log/blackbox"})
	got, err := readAll(t, tr, f.file, 0)
	if err != nil || !bytes.Equal(got, f.data) {
		t.Fatalf("whole file: len %d err %v", len(got), err)
	}
	got, err = readAll(t, tr, f.file, 2345)
	if err != nil || !bytes.Equal(got, f.data[2345:]) {
		t.Fatalf("resume at 2345: len %d err %v", len(got), err)
	}
	got, err = readAll(t, tr, f.file, int64(len(f.data)))
	if err != nil || len(got) != 0 {
		t.Fatalf("resume at the end: len %d err %v", len(got), err)
	}
	if _, err := readAll(t, tr, f.file, int64(len(f.data))+1); !errors.Is(err, transport.ErrNotAllowed) {
		t.Fatalf("offset past the end: %v", err)
	}
	// No chunk may ask for more than the configured size.
	for _, c := range f.cmds {
		if m := reTail.FindStringSubmatch(strings.ReplaceAll(c, `'\''`, "'")); m != nil {
			if n, _ := strconv.Atoi(m[3]); n > 1000 {
				t.Fatalf("chunk of %d bytes", n)
			}
		}
	}
}

func TestBadChunksNeverLookLikeTheEnd(t *testing.T) {
	for name, mod := range map[string]func(*fakeMaster){"garbled": func(f *fakeMaster) { f.garble = true }, "short": func(f *fakeMaster) { f.short = true }} {
		f := newFake(3000)
		mod(f)
		tr, _ := master(f).For("web-01", "linux", []string{"/var/log/blackbox"})
		got, err := readAll(t, tr, f.file, 0)
		if err == nil || !errors.Is(err, ErrShortChunk) {
			t.Fatalf("%s: err %v (got %d bytes)", name, err, len(got))
		}
	}
}

func TestDigestMatchesAndOfflineIsAnError(t *testing.T) {
	f := newFake(2048)
	tr, _ := master(f).For("web-01", "linux", []string{"/var/log/blackbox"})
	got, err := tr.Digest(context.Background(), f.file)
	h := sha256.Sum256(f.data)
	if err != nil || got != hex.EncodeToString(h[:]) {
		t.Fatalf("%s %v", got, err)
	}
	f.down = true
	if _, err := tr.Digest(context.Background(), f.file); !errors.Is(err, ErrOffline) {
		t.Fatalf("offline digest: %v", err)
	}
	if _, err := tr.Probe(context.Background()); !errors.Is(err, ErrOffline) {
		t.Fatalf("offline probe: %v", err)
	}
}

func TestProbeReportsClockAndSystem(t *testing.T) {
	f := newFake(1)
	tr, _ := master(f).For("web-01", "linux", nil)
	info, err := tr.Probe(context.Background())
	if err != nil || info.OS != "linux" || info.Description != "Ubuntu 22.04" || info.ClockOffset == 0 {
		t.Fatalf("%+v %v", info, err)
	}
}

func TestWindowsMinionScripts(t *testing.T) {
	f := newFake(1)
	tr, err := master(f).For("web-01", "windows", []string{`D:\Blackbox\snapshots`})
	if err != nil {
		t.Fatal(err)
	}
	// The generated PowerShell must hold nothing Salt could read as YAML, and must not use a quote the master's shell word would break.
	p := `D:\Blackbox\snapshots\s one.zip`
	var scripts []string
	f2 := &recorder{id: "web-01", reply: p}
	tr.m.R = f2
	tr.Open(context.Background(), p, 0)
	tr.Digest(context.Background(), p)
	tr.List(context.Background(), `D:\Blackbox\snapshots`)
	tr.readChunk(context.Background(), p, 10, 100)
	scripts = f2.cmds
	if len(scripts) == 0 {
		t.Fatal("nothing was sent")
	}
	for _, s := range scripts {
		if strings.Contains(s, "shell=powershell") == false && !strings.Contains(s, "salt-") {
			continue
		}
		if strings.Contains(s, `"'"`) {
			t.Fatalf("quote clash: %s", s)
		}
	}
	joined := strings.Join(scripts, "\n")
	for _, want := range []string{"ReparsePoint", "Get-FileHash", `[IO.File]::Open('\''D:\Blackbox\snapshots\s one.zip'\'',3,1,3)`, "'shell=powershell'", "ToBase64String"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in\n%s", want, joined)
		}
	}
}

// recorder answers every call with an empty string so the Windows commands can be inspected.
type recorder struct {
	id    string
	reply string
	cmds  []string
}

func (r *recorder) Exec(_ context.Context, cmd string, _ int) ([]byte, error) {
	r.cmds = append(r.cmds, cmd)
	b, _ := json.Marshal(map[string]string{r.id: r.reply})
	return b, nil
}

func TestFirstJSONSkipsWarningsAndRefusesTrailingText(t *testing.T) {
	good := "[WARNING] odd {\"a\":1} thing\n{\"web-01\":true}\n"
	got, err := firstJSON([]byte(good))
	if err != nil || string(got) != `{"web-01":true}` {
		t.Fatalf("%s %v", got, err)
	}
	if _, err := firstJSON([]byte("{\"a\":1} and then text")); err == nil {
		t.Fatal("trailing text was accepted")
	}
}

func TestMinionDownOnlyAtTheStart(t *testing.T) {
	if !reMinionDown.MatchString("Minion did not return. [Not connected]") {
		t.Fatal("a down minion was missed")
	}
	if reMinionDown.MatchString("line one\nthe log said no response from upstream") {
		t.Fatal("log text mentioning no response was taken for a down minion")
	}
}
