package main

// redispassword.go: set-redis-password and clear-redis-password.
//
// The only commands that touch the stored Redis password (internal/bbredis/password.go). They run as an
// administrator, read the password from standard input (never from an argument, so it is not in a process list or a
// shell history), test it against the Redis on this PC before saving, seal it with the machine key into the locked
// <folder>\redis folder, and never print, log or answer with any part of it: every failure is a fixed reason and a
// fixed sentence.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"api-audit-explorer/internal/bbredis"
	"api-audit-explorer/internal/blackbox"
)

const maxRedisPasswordInput = 1 << 10

// Fixed reasons of the Redis password commands.
const (
	reasonRedisInvalid     = "redis-password-invalid"
	reasonRedisUnreachable = "redis-unreachable"
	reasonRedisRefused     = "redis-password-refused"
	reasonRedisSave        = "redis-password-save"
	reasonRedisNoPassword  = "redis-no-password"
	reasonRedisNotTrusted  = "redis-not-trusted"
)

var redisReasonSentences = map[string]string{
	reasonRedisInvalid:     "The Redis password is not valid. Use 1 to 256 characters with no line break, on standard input.",
	reasonRedisUnreachable: "Redis did not answer on this PC. Check the port in the Blackbox settings and that Redis is running. Nothing was saved.",
	reasonRedisRefused:     "Redis did not accept that password. Nothing was saved.",
	reasonRedisSave:        "The Redis password could not be saved or removed.",
	reasonRedisNoPassword:  "This Redis does not ask for a password. Nothing was saved and nothing was sent.",
	reasonRedisNotTrusted:  "The program on the Redis port is not an installed program or a service. The password was not sent and nothing was saved.",
}

func (a answer) failRedis(reason string) int {
	msg := redisReasonSentences[reason]
	if a.asJSON {
		_ = json.NewEncoder(a.out).Encode(map[string]any{"ok": false, "reason": reason, "message": msg})
	} else {
		_, _ = fmt.Fprintln(a.err, msg)
	}
	return exitFail
}

// redisCheck tests a password against the Redis on this PC. A variable (on deps) so tests need no server. The
// password is sent only when the server asks for one AND the program on the port is an installed one or a service
// account (any local user can open a port, so loopback alone is not proof).
func redisCheck(port int, password string) error {
	c, err := bbredis.Dial(port, 3*time.Second)
	if err != nil {
		return errRedisUnreachable
	}
	defer c.Close()
	_, perr := c.Do("PING")
	switch {
	case perr == nil:
		if password != "" {
			return errRedisNoPassword // nothing asks for a password: do not send one
		}
		return nil
	case !bbredis.IsNoAuth(perr):
		return errRedisUnreachable
	case password == "":
		return errRedisRefused
	}
	if bbredis.ListenerCheck(port) != nil {
		return errRedisNotTrusted
	}
	if err := c.Auth(password); err != nil {
		if bbredis.IsNoAuth(err) {
			return errRedisRefused
		}
		return errRedisUnreachable
	}
	if _, err := c.Do("PING"); err != nil {
		return errRedisRefused
	}
	return nil
}

var (
	errRedisUnreachable = errors.New("redis unreachable")
	errRedisRefused     = errors.New("redis refused")
	errRedisNoPassword  = errors.New("redis has no password")
	errRedisNotTrusted  = errors.New("redis port not trusted")
)

// redisPasswordDir returns the locked folder for the password, creating it when it is not there. The administrator
// check comes first. The folder is under the Blackbox folder of the INSTALLED settings (the caller passes the installed
// copy, never a path an ordinary user handed in).
func (d deps) redisPasswordDir(cfg blackbox.Config) (string, error) {
	if !d.mgr.Env.IsAdmin() {
		return "", blackbox.ErrNotAdmin
	}
	dir := blackbox.RedisDataDir(cfg.Folder)
	fi, err := os.Lstat(dir)
	switch {
	case err == nil && fi.IsDir() && fi.Mode()&os.ModeSymlink == 0:
		// Already there: write its locked permissions again, so a folder somebody loosened is closed before use.
		if serr := d.mgr.Env.SecureDir(dir, false); serr != nil {
			return "", errRedisSaveFailed
		}
	case err == nil:
		return "", errRedisSaveFailed // a file or a link where the folder should be
	case errors.Is(err, os.ErrNotExist):
		// Made with its locked permissions in the create call, so there is no moment when it has inherited rights. An
		// Env without that (a test fake) makes a plain folder and locks it right after.
		if sc, ok := d.mgr.Env.(blackbox.SecureCreator); ok {
			if cerr := sc.CreateSecureDir(dir, false); cerr != nil {
				return "", errRedisSaveFailed
			}
		} else {
			if merr := os.Mkdir(dir, 0o700); merr != nil {
				return "", errRedisSaveFailed
			}
			if serr := d.mgr.Env.SecureDir(dir, false); serr != nil {
				return "", errRedisSaveFailed
			}
		}
	default:
		return "", errRedisSaveFailed
	}
	return dir, nil
}

var errRedisSaveFailed = errors.New("redis password save failed")

// runRedisPassword handles set-redis-password and clear-redis-password.
func runRedisPassword(cmd, configPath string, d deps, ans answer) int {
	cfg, err := loadInstalledConfig(configPath)
	if err != nil {
		return ans.fail(err)
	}
	dir, err := d.redisPasswordDir(cfg)
	if err != nil {
		if errors.Is(err, blackbox.ErrNotAdmin) {
			return ans.fail(err)
		}
		return ans.failRedis(reasonRedisSave)
	}
	if cmd == "clear-redis-password" {
		if err := bbredis.RemovePassword(dir); err != nil {
			return ans.failRedis(reasonRedisSave)
		}
		return ans.ok("The saved Redis password was removed.", map[string]any{"removed": true})
	}
	in := d.stdin
	if in == nil {
		in = os.Stdin
	}
	raw, err := io.ReadAll(io.LimitReader(in, maxRedisPasswordInput+1))
	if err != nil || len(raw) > maxRedisPasswordInput {
		wipe(raw)
		return ans.failRedis(reasonRedisInvalid)
	}
	pw := strings.TrimRight(string(raw), "\r\n")
	wipe(raw)
	if !bbredis.ValidPassword(pw) {
		return ans.failRedis(reasonRedisInvalid)
	}
	check := d.redisCheck
	if check == nil {
		check = redisCheck
	}
	if err := check(cfg.RedisPort(), pw); err != nil {
		switch {
		case errors.Is(err, errRedisRefused):
			return ans.failRedis(reasonRedisRefused)
		case errors.Is(err, errRedisNoPassword):
			return ans.failRedis(reasonRedisNoPassword)
		case errors.Is(err, errRedisNotTrusted):
			return ans.failRedis(reasonRedisNotTrusted)
		}
		return ans.failRedis(reasonRedisUnreachable)
	}
	prot, err := d.loginSealer()
	if err != nil {
		return ans.failRedis(reasonRedisSave)
	}
	if err := bbredis.SavePassword(dir, pw, prot); err != nil {
		return ans.failRedis(reasonRedisSave)
	}
	return ans.ok("The Redis password was saved, sealed for this PC. The Blackbox uses it at its next sample.", map[string]any{"saved": true})
}
