package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"api-audit-explorer/internal/blackbox"
)

// config set | validate | history | rollback (B5). The verbs are fixed here and in blackbox.ChangeOp: nothing a caller
// writes can name a command, a file to run or a path to open. Each change is checked, applied through the locked
// settings path, watched for a minute, and rolled back by itself if the recorder is not healthy.

const configUsage = `usage:
  unravis-blackbox config show [--json]
  unravis-blackbox config set <collector-id> on|off [--every <seconds|5m>] [--json]
  unravis-blackbox config set <collector-id> --every <seconds|5m> [--json]
  unravis-blackbox config set counters.extra add|remove "<\Object(Instance)\Counter>" [--json]
  unravis-blackbox config validate <file> [--json]
  unravis-blackbox config history [--json]
  unravis-blackbox config rollback [--json]`

// configSub splits "config <sub> ..." into the command name, the words before the first flag, and the flags after.
func configSub(rest []string) (cmd string, words, flags []string, ok bool) {
	if len(rest) == 0 {
		return "", nil, nil, false
	}
	switch rest[0] {
	case "show", "set", "validate", "history", "rollback":
		cmd = "config-" + rest[0]
	default:
		return "", nil, nil, false
	}
	tail := rest[1:]
	i := 0
	for i < len(tail) && !strings.HasPrefix(tail[i], "-") {
		i++
	}
	return cmd, tail[:i], tail[i:], true
}

// parseEvery reads "--every": whole seconds ("60") or a short duration ("60s", "5m"). 0 means every sample. Whether the
// value is one the recorder allows is the settings check's job (Validate).
func parseEvery(v string) (int, error) {
	v = strings.TrimSpace(v)
	if n, err := strconv.Atoi(v); err == nil {
		if n < 0 {
			return 0, fmt.Errorf("--every cannot be negative")
		}
		return n, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 || d%time.Second != 0 {
		return 0, fmt.Errorf("--every must be whole seconds, or a duration such as 60s or 5m")
	}
	return int(d / time.Second), nil
}

// parseSetArgs turns the words of `config set` into one ChangeOp.
func parseSetArgs(words []string, every string) (blackbox.ChangeOp, error) {
	var op blackbox.ChangeOp
	if len(words) >= 1 && words[0] == "counters.extra" {
		if len(words) != 3 || every != "" {
			return op, fmt.Errorf("usage: config set counters.extra add|remove \"<counter path>\"")
		}
		switch words[1] {
		case "add":
			return blackbox.ChangeOp{Verb: blackbox.OpCounterAdd, Path: words[2]}, nil
		case "remove":
			return blackbox.ChangeOp{Verb: blackbox.OpCounterRemove, Path: words[2]}, nil
		}
		return op, fmt.Errorf("counters.extra takes add or remove")
	}
	if len(words) < 1 || len(words) > 2 {
		return op, fmt.Errorf("usage: config set <collector-id> on|off [--every <seconds>]")
	}
	op = blackbox.ChangeOp{Verb: blackbox.OpCollector, ID: words[0]}
	if len(words) == 2 {
		switch words[1] {
		case "on", "off":
			v := words[1] == "on"
			op.On = &v
		default:
			return op, fmt.Errorf("say on or off")
		}
	}
	if every != "" {
		n, err := parseEvery(every)
		if err != nil {
			return op, err
		}
		op.EverySec = &n
	}
	return op, nil
}

func historyText(h blackbox.ConfigHistory) string {
	var b strings.Builder
	if !h.Verified {
		fmt.Fprintf(&b, "WARNING: the change history does not verify (%s). Changes are refused until an administrator looks at it.\n", h.Problem)
	}
	if h.Pending {
		b.WriteString("A change is being checked, or did not finish. The next change command finishes it.\n")
	}
	if len(h.Lines) == 0 {
		b.WriteString("No changes have been made with this command.\n")
	}
	for _, l := range h.Lines {
		fmt.Fprintf(&b, "%3d  %s  %-11s  %s\n", l.Seq, l.At.Format("2006-01-02 15:04:05Z"), cleanText(l.Result, 20), cleanText(l.Detail, 160))
	}
	return strings.TrimRight(b.String(), "\n")
}

// cleanText keeps printable text only and cuts it, so a forged audit line cannot put control characters on a screen.
func cleanText(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	if len(s) > max {
		s = s[:max]
	}
	return s
}

const validateMaxBytes = 1 << 20

// validateFile reads a settings file for `config validate`. It runs as an administrator or SYSTEM, so it opens only an
// ordinary file on a local drive: no network path, device name, pipe, link or junction, and nothing larger than a
// settings file. Every failure to read says the same sentence, so the answer tells nobody what exists on the disk.
func validateFile(path string) (blackbox.Config, error) {
	bad := fmt.Errorf("That file cannot be read as Blackbox settings. Give the full path of an ordinary settings file on this PC: %w", blackbox.ErrInvalidConfig)
	if !filepath.IsAbs(path) || strings.HasPrefix(path, `\\`) || strings.HasPrefix(path, "//") || strings.ContainsAny(path, "\x00") {
		return blackbox.Config{}, bad
	}
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > validateMaxBytes {
		return blackbox.Config{}, bad
	}
	f, err := os.Open(path) //nolint:gosec // checked above: absolute, local, regular, small
	if err != nil {
		return blackbox.Config{}, bad
	}
	defer func() { _ = f.Close() }()
	if fi2, err := f.Stat(); err != nil || !fi2.Mode().IsRegular() || !os.SameFile(fi, fi2) {
		return blackbox.Config{}, bad
	}
	data, err := io.ReadAll(io.LimitReader(f, validateMaxBytes+1))
	if err != nil || len(data) > validateMaxBytes {
		return blackbox.Config{}, bad
	}
	draft, err := blackbox.ParseConfigDraft(data)
	if err != nil {
		// Not settings at all (or an unknown field): no detail, so a non-settings file is not echoed back.
		return blackbox.Config{}, fmt.Errorf("That file is not valid Blackbox settings: %w", blackbox.ErrInvalidConfig)
	}
	if err := draft.Validate(); err != nil {
		return blackbox.Config{}, fmt.Errorf("%s: %w", cleanText(err.Error(), 200), blackbox.ErrInvalidConfig)
	}
	return draft, nil
}
