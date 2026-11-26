// Package bbredis is the Blackbox's read-only Redis reader: a tiny RESP client that talks only to this PC, may send
// only the few commands the recorder needs, and turns the answers into numbers and fixed names. No library, no
// command from settings, no key and no value ever comes back out of it (plan: docs/blackbox/WHOLE-STACK.md 4.3).
package bbredis

import (
	"errors"
	"strconv"
	"strings"
)

// ErrNotAllowed is returned for any command outside the allowlist. It is checked in code before anything is sent.
var ErrNotAllowed = errors.New("bbredis: that command is not allowed")

// infoSections are the INFO sections the recorder may ask for. "all", "everything" and "default" are not on the list:
// the recorder names each section.
var infoSections = map[string]bool{
	"server": true, "clients": true, "memory": true, "stats": true, "replication": true,
	"persistence": true, "commandstats": true, "keyspace": true, "cpu": true,
}

// configParams are the only CONFIG GET parameters the recorder reads. requirepass, masterauth, masteruser and
// anything else that can hold a secret are not here.
var configParams = map[string]bool{
	"maxmemory": true, "maxmemory-policy": true, "appendonly": true, "save": true, "dir": true,
	"logfile": true, "dbfilename": true, "appendfilename": true, "latency-monitor-threshold": true,
	"slowlog-log-slower-than": true, "slowlog-max-len": true, "timeout": true, "maxclients": true,
}

// MaxSlowlogGet bounds SLOWLOG GET so one answer stays small.
const MaxSlowlogGet = 128

// ConfigParams lists the parameters CONFIG GET may ask for, sorted by name.
func ConfigParams() []string {
	out := make([]string, 0, len(configParams))
	for k := range configParams {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}

// Allowed says whether the recorder may send args as one command. The check is on the exact words: the command name
// (any case), then a fixed shape for each command. Anything else, including a command with an extra word, is refused.
//
//	PING
//	INFO [section ...]                    sections from the fixed list
//	SLOWLOG GET [count]                   count 1..128 (never RESET, never LEN)
//	LATENCY LATEST                        (never RESET, never HISTORY)
//	CLIENT LIST                           (never KILL, never SETNAME)
//	CONFIG GET <parameter>                parameter from the fixed list (never SET, never a pattern)
//
// AUTH is not allowed here at all: the password goes through Client.Auth only.
func Allowed(args []string) error {
	if len(args) == 0 {
		return ErrNotAllowed
	}
	for _, a := range args {
		// A bare newline cannot break the RESP framing (bulk strings are length-prefixed), but no allowed command
		// has one, so none is let through.
		if strings.ContainsAny(a, "\r\n\x00") {
			return ErrNotAllowed
		}
	}
	cmd := strings.ToUpper(args[0])
	switch cmd {
	case "PING":
		if len(args) == 1 {
			return nil
		}
	case "INFO":
		for _, s := range args[1:] {
			if !infoSections[strings.ToLower(s)] {
				return ErrNotAllowed
			}
		}
		return nil
	case "SLOWLOG":
		if len(args) >= 2 && strings.EqualFold(args[1], "GET") {
			if len(args) == 2 {
				return nil
			}
			if len(args) == 3 {
				if n, err := strconv.Atoi(args[2]); err == nil && n >= 1 && n <= MaxSlowlogGet {
					return nil
				}
			}
		}
	case "LATENCY":
		if len(args) == 2 && strings.EqualFold(args[1], "LATEST") {
			return nil
		}
	case "CLIENT":
		if len(args) == 2 && strings.EqualFold(args[1], "LIST") {
			return nil
		}
	case "CONFIG":
		if len(args) == 3 && strings.EqualFold(args[1], "GET") && configParams[strings.ToLower(args[2])] {
			return nil
		}
	}
	return ErrNotAllowed
}
