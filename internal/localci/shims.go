package localci

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// shimEnv is what a `uses:` shim gets: the step's interpolated inputs and the runner facts.
type shimEnv struct {
	With     map[string]string
	Workdir  string
	StateDir string
	TmpDir   string
	RunnerOS string
	Env      []string       // the step's process environment
	GitHub   map[string]any // the github context
	Logf     func(format string, a ...any)
}

type shimFunc func(ctx context.Context, s *shimEnv) (map[string]string, error)

// shims maps a `uses` name (without @ref) to its local implementation.
var shims = map[string]shimFunc{
	"actions/checkout":           shimCheckout,
	"actions/setup-go":           shimSetupGo,
	"actions/setup-node":         shimSetupNode,
	"actions/cache":              shimCache,
	"actions/cache/restore":      shimCache,
	"actions/cache/save":         shimCache,
	"actions/upload-artifact":    shimUpload,
	"actions/download-artifact":  shimDownload,
	"trufflesecurity/trufflehog": shimTrufflehog,
}

// shimName returns the `uses` name without @ref.
func shimName(uses string) string {
	if i := strings.Index(uses, "@"); i >= 0 {
		uses = uses[:i]
	}
	return strings.TrimSpace(uses)
}

// runCapture runs a short command in the workdir with the step environment and returns its output.
func (s *shimEnv) runCapture(ctx context.Context, name string, args ...string) (string, error) {
	p, err := lookPathEnv(name, s.Env)
	if err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, p, args...) //nolint:gosec // running the workflow command is the point of the runner
	cmd.Dir = s.Workdir
	cmd.Env = s.Env
	if name == "go" {
		// never let go download a toolchain just to print its version
		cmd.Env = append(append([]string{}, s.Env...), "GOTOOLCHAIN=local")
		if s.TmpDir != "" {
			cmd.Dir = s.TmpDir
		}
	}
	out, err := cmd.CombinedOutput()
	if err != nil && len(out) > 0 {
		err = fmt.Errorf("%w: %s", err, firstLine(string(out)))
	}
	return strings.TrimSpace(string(out)), err
}

// ---------------------------------------------------------------- checkout

func shimCheckout(ctx context.Context, s *shimEnv) (map[string]string, error) {
	if _, err := os.Stat(filepath.Join(s.Workdir, ".git")); err != nil {
		return nil, fmt.Errorf("checkout: %s is not a git checkout (.git is missing)", s.Workdir)
	}
	sha, err := s.runCapture(ctx, "git", "rev-parse", "HEAD")
	if err != nil {
		s.Logf("checkout: local checkout in use, could not read HEAD: %v", err)
	} else {
		s.Logf("checkout: using the local checkout %s at %s", s.Workdir, sha)
	}
	return nil, nil
}

// ---------------------------------------------------------------- versions

// parseVersion reads "go1.27.4", "v22.23.5", "22", "1.22.x", "^1.20" into numeric parts.
func parseVersion(v string) ([]int, bool) {
	v = strings.TrimSpace(v)
	v = strings.TrimLeft(v, "^~>=<vV ")
	v = strings.TrimPrefix(v, "go")
	if v == "" {
		return nil, false
	}
	var parts []int
	for _, p := range strings.Split(v, ".") {
		if p == "x" || p == "X" || p == "*" {
			break
		}
		n := 0
		digits := 0
		for digits < len(p) && p[digits] >= '0' && p[digits] <= '9' {
			digits++
		}
		if digits == 0 {
			return nil, false
		}
		n, _ = strconv.Atoi(p[:digits])
		parts = append(parts, n)
		if digits < len(p) { // rc1, beta2: stop here
			break
		}
	}
	return parts, len(parts) > 0
}

func cmpParts(a, b []int) int {
	for i := 0; i < len(a) || i < len(b); i++ {
		var x, y int
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

// goVersionOK reports whether the installed Go (found, e.g. "go1.27.4") is at least the wanted one
// (e.g. "1.27" or "1.27.4"; a missing patch level means any patch).
func goVersionOK(found, want string) (bool, error) {
	w, ok := parseVersion(want)
	if !ok {
		return true, nil // stable, oldstable and similar: no numeric constraint
	}
	f, ok := parseVersion(found)
	if !ok {
		return false, fmt.Errorf("cannot read the Go version %q", found)
	}
	return cmpParts(f, w) >= 0, nil
}

// nodeVersionOK: same major, and minor >= the pinned minor when given. An older patch level is
// accepted (see nodeOlderPatch, which the shim logs as a warning): a point release does not change
// what a local check proves.
func nodeVersionOK(found, want string) (bool, error) {
	w, ok := parseVersion(want)
	if !ok {
		return true, nil
	}
	f, ok := parseVersion(found)
	if !ok {
		return false, fmt.Errorf("cannot read the Node version %q", found)
	}
	if f[0] != w[0] {
		return false, nil
	}
	if len(w) > 1 {
		return cmpParts(f[:min(2, len(f))], w[:2]) >= 0, nil
	}
	return true, nil
}

// nodeOlderPatch reports whether found is behind the pinned version only at patch level.
func nodeOlderPatch(found, want string) bool {
	w, ok1 := parseVersion(want)
	f, ok2 := parseVersion(found)
	return ok1 && ok2 && cmpParts(f, w) < 0
}

var goModLine = regexp.MustCompile(`(?m)^\s*(go|toolchain)\s+(\S+)`)

// wantedGo reads go.mod: the toolchain line when present, else the go line.
func wantedGo(gomod string) string {
	goV, tool := "", ""
	for _, m := range goModLine.FindAllStringSubmatch(gomod, -1) {
		if m[1] == "go" && goV == "" {
			goV = m[2]
		}
		if m[1] == "toolchain" && tool == "" {
			tool = m[2]
		}
	}
	if tool != "" && tool != "default" {
		return strings.TrimPrefix(tool, "go")
	}
	return goV
}

func shimSetupGo(ctx context.Context, s *shimEnv) (map[string]string, error) {
	want := strings.TrimSpace(s.With["go-version"])
	if f := strings.TrimSpace(s.With["go-version-file"]); want == "" && f != "" {
		data, err := os.ReadFile(filepath.Join(s.Workdir, filepath.FromSlash(f)))
		if err != nil {
			return nil, fmt.Errorf("setup-go: %w", err)
		}
		if filepath.Base(f) == "go.mod" || strings.HasSuffix(f, ".mod") {
			want = wantedGo(string(data))
		} else {
			want = strings.TrimSpace(string(data))
		}
	}
	out, err := s.runCapture(ctx, "go", "version")
	if err != nil {
		return nil, fmt.Errorf("setup-go: go is not installed on this PC (%v)", err)
	}
	fields := strings.Fields(out)
	if len(fields) < 3 {
		return nil, fmt.Errorf("setup-go: unexpected output of go version: %q", out)
	}
	found := strings.TrimPrefix(fields[2], "go")
	if want != "" {
		ok, err := goVersionOK(found, want)
		if err != nil {
			return nil, fmt.Errorf("setup-go: %w", err)
		}
		if !ok {
			return nil, fmt.Errorf("setup-go: need go %s, found %s", strings.TrimPrefix(want, "go"), found)
		}
	}
	s.Logf("setup-go: %s", out)
	return map[string]string{"go-version": found}, nil
}

func shimSetupNode(ctx context.Context, s *shimEnv) (map[string]string, error) {
	want := strings.TrimSpace(s.With["node-version"])
	out, err := s.runCapture(ctx, "node", "--version")
	if err != nil {
		return nil, fmt.Errorf("setup-node: node is not installed on this PC (%v)", err)
	}
	found := strings.TrimPrefix(out, "v")
	if want != "" {
		ok, err := nodeVersionOK(found, want)
		if err != nil {
			return nil, fmt.Errorf("setup-node: %w", err)
		}
		if !ok {
			return nil, fmt.Errorf("setup-node: need node %s, found %s", want, found)
		}
	}
	if want != "" && nodeOlderPatch(found, want) {
		s.Logf("setup-node: warning: this PC has node %s, the workflow pins %s (older patch level, accepted)", found, want)
	}
	s.Logf("setup-node: node %s", found)
	return map[string]string{"node-version": found}, nil
}

// ---------------------------------------------------------------- cache

func shimCache(ctx context.Context, s *shimEnv) (map[string]string, error) {
	s.Logf("cache: skipped, this PC keeps persistent caches")
	return map[string]string{"cache-hit": "false"}, nil
}

// ---------------------------------------------------------------- artifacts

func artifactName(s *shimEnv) string {
	n := strings.TrimSpace(s.With["name"])
	return n
}

// matchGlob matches a slash-separated relative path against a pattern with * ? [..] and **.
func matchGlob(pat, name string) bool {
	return matchSegs2(strings.Split(pat, "/"), strings.Split(name, "/"))
}

func matchSegs2(pat, name []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			for len(pat) > 1 && pat[1] == "**" {
				pat = pat[1:]
			}
			if len(pat) == 1 {
				return true
			}
			for i := 0; i <= len(name); i++ {
				if matchSegs2(pat[1:], name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		ok, err := path.Match(pat[0], name[0])
		if err != nil || !ok {
			return false
		}
		pat, name = pat[1:], name[1:]
	}
	return len(name) == 0
}

// collectFiles resolves patterns (files, directories or globs, relative to root; ! negates) to
// slash-separated relative file paths.
func collectFiles(root string, patterns []string) ([]string, error) {
	found := map[string]bool{}
	var neg []string
	for _, raw := range patterns {
		p := strings.TrimSpace(raw)
		if p == "" {
			continue
		}
		if strings.HasPrefix(p, "!") {
			neg = append(neg, strings.TrimPrefix(filepath.ToSlash(strings.TrimPrefix(p, "!")), "./"))
			continue
		}
		p = filepath.ToSlash(p)
		if filepath.IsAbs(raw) {
			rel, err := filepath.Rel(root, raw)
			if err != nil || strings.HasPrefix(rel, "..") {
				return nil, fmt.Errorf("path %s is outside the workspace", raw)
			}
			p = filepath.ToSlash(rel)
		}
		p = strings.TrimPrefix(p, "./")
		if strings.ContainsAny(p, "*?[") {
			// walk from the longest literal directory prefix
			segs := strings.Split(p, "/")
			base := ""
			for _, sg := range segs[:len(segs)-1] {
				if strings.ContainsAny(sg, "*?[") {
					break
				}
				base = path.Join(base, sg)
			}
			start := filepath.Join(root, filepath.FromSlash(base))
			_ = filepath.WalkDir(start, func(fp string, d fs.DirEntry, err error) error {
				if err != nil {
					return nil //nolint:nilerr // unreadable entries are skipped on purpose; the walk carries on
				}
				if d.IsDir() {
					if d.Name() == ".git" {
						return filepath.SkipDir
					}
					return nil
				}
				rel, _ := filepath.Rel(root, fp)
				rel = filepath.ToSlash(rel)
				if matchGlob(p, rel) {
					found[rel] = true
				}
				return nil
			})
			continue
		}
		full := filepath.Join(root, filepath.FromSlash(p))
		st, err := os.Stat(full)
		if err != nil {
			continue
		}
		if !st.IsDir() {
			found[path.Clean(p)] = true
			continue
		}
		_ = filepath.WalkDir(full, func(fp string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				rel, _ := filepath.Rel(root, fp)
				found[filepath.ToSlash(rel)] = true
			}
			return nil
		})
	}
	var out []string
nextFile:
	for f := range found {
		for _, n := range neg {
			if matchGlob(n, f) || strings.HasPrefix(f, strings.TrimSuffix(n, "/")+"/") {
				continue nextFile
			}
		}
		out = append(out, f)
	}
	sort.Strings(out)
	return out, nil
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	in, err := os.Open(src) //nolint:gosec // the checkout path comes from the workflow by design
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst) //nolint:gosec // the destination path comes from the workflow by design
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func shimUpload(ctx context.Context, s *shimEnv) (map[string]string, error) {
	name := artifactName(s)
	if name == "" {
		name = "artifact"
	}
	if strings.ContainsAny(name, `/\`) || name == ".." {
		return nil, fmt.Errorf("upload-artifact: invalid artifact name %q", name)
	}
	files, err := collectFiles(s.Workdir, strings.Split(s.With["path"], "\n"))
	if err != nil {
		return nil, fmt.Errorf("upload-artifact: %w", err)
	}
	if len(files) == 0 {
		switch strings.TrimSpace(s.With["if-no-files-found"]) {
		case "error":
			return nil, fmt.Errorf("upload-artifact: no files were found with the provided path: %s", strings.TrimSpace(s.With["path"]))
		case "ignore":
		default:
			s.Logf("::warning::upload-artifact: no files were found with the provided path: %s. No artifacts will be uploaded.", strings.TrimSpace(s.With["path"]))
		}
		return nil, nil
	}
	dest := filepath.Join(s.StateDir, "artifacts", name)
	for _, f := range files {
		if err := copyFile(filepath.Join(s.Workdir, filepath.FromSlash(f)), filepath.Join(dest, filepath.FromSlash(f))); err != nil {
			return nil, fmt.Errorf("upload-artifact: %w", err)
		}
	}
	s.Logf("upload-artifact: %s: %d file(s)", name, len(files))
	return nil, nil
}

func copyTree(src, dst string) (int, error) {
	n := 0
	err := filepath.WalkDir(src, func(fp string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(src, fp)
		n++
		return copyFile(fp, filepath.Join(dst, rel))
	})
	return n, err
}

func shimDownload(ctx context.Context, s *shimEnv) (map[string]string, error) {
	dst := s.Workdir
	if p := strings.TrimSpace(s.With["path"]); p != "" {
		dst = p
		if !filepath.IsAbs(dst) {
			dst = filepath.Join(s.Workdir, dst)
		}
	}
	root := filepath.Join(s.StateDir, "artifacts")
	name := artifactName(s)
	if name != "" {
		if strings.ContainsAny(name, `/\`) || name == ".." {
			return nil, fmt.Errorf("download-artifact: invalid artifact name %q", name)
		}
		src := filepath.Join(root, name)
		if st, err := os.Stat(src); err != nil || !st.IsDir() {
			return nil, fmt.Errorf("download-artifact: artifact %q not found", name)
		}
		n, err := copyTree(src, dst)
		if err != nil {
			return nil, fmt.Errorf("download-artifact: %w", err)
		}
		s.Logf("download-artifact: %s: %d file(s) to %s", name, n, dst)
		return map[string]string{"download-path": dst}, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, errors.New("download-artifact: no artifacts exist in this run")
	}
	merge := strings.TrimSpace(s.With["merge-multiple"]) == "true"
	total := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		target := filepath.Join(dst, e.Name())
		if merge {
			target = dst
		}
		n, err := copyTree(filepath.Join(root, e.Name()), target)
		if err != nil {
			return nil, fmt.Errorf("download-artifact: %w", err)
		}
		total += n
	}
	s.Logf("download-artifact: %d file(s) to %s", total, dst)
	return map[string]string{"download-path": dst}, nil
}

// ---------------------------------------------------------------- trufflehog

func ghString(m map[string]any, keys ...string) string {
	var cur any = m
	for _, k := range keys {
		mm, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = mm[k]
	}
	if cur == nil {
		return ""
	}
	return fmt.Sprint(cur)
}

func shimTrufflehog(ctx context.Context, s *shimEnv) (map[string]string, error) {
	exe, err := lookPathEnv("trufflehog", s.Env)
	if err != nil {
		return nil, errors.New("trufflehog is not installed on this PC")
	}
	p := strings.TrimSpace(s.With["path"])
	if p == "" {
		p = "."
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(s.Workdir, p)
	}
	base := strings.TrimSpace(s.With["base"])
	if base == "" {
		base = ghString(s.GitHub, "event", "pull_request", "base", "sha")
	}
	head := strings.TrimSpace(s.With["head"])
	if head == "" {
		head = ghString(s.GitHub, "event", "pull_request", "head", "sha")
	}
	args := []string{"git", "file://" + filepath.ToSlash(p)}
	if base != "" {
		args = append(args, "--since-commit", base)
	}
	if head != "" {
		args = append(args, "--branch", head)
	}
	args = append(args, "--fail", "--no-update")
	args = append(args, strings.Fields(s.With["extra_args"])...)
	s.Logf("trufflehog %s", strings.Join(args, " "))
	cmd := exec.Command(exe, args...) //nolint:gosec // running the scanner binary is the point of this shim
	cmd.Dir = s.Workdir
	cmd.Env = s.Env
	prepareCmd(cmd)
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("trufflehog: %w", err)
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			killTree(cmd)
		case <-done:
		}
	}()
	scanDone := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
		for sc.Scan() {
			s.Logf("%s", sc.Text())
		}
		close(scanDone)
	}()
	werr := cmd.Wait()
	close(done)
	_ = pw.Close()
	<-scanDone
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if werr != nil {
		return nil, fmt.Errorf("trufflehog: %v", werr)
	}
	return nil, nil
}
