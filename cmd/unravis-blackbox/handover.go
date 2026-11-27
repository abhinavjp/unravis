package main

// handover.go: how setup-sql gets a saved SQL login that the key keeper holds (RV-SEC-2).
//
// Unravis does not put such a login in a file. It asks the keeper to hold it under a one-time id and starts this program with that id and
// the keeper's pipe name. This program asks the keeper for the login itself, once, over the keeper's own pipe, and never prints, logs or
// echoes it. The keeper decides who may take it (the same Windows session as the person who asked), so the id on the command line is of no
// use to anyone else. Only the small keeperwire package is used here, so this program does not link the keeper's stores or drivers.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"api-audit-explorer/internal/keeperwire"
)

// handoverTimeout bounds the whole fetch: the keeper answers at once or not at all.
const handoverTimeout = 20 * time.Second

// reasonLoginHandover is the fixed reason for every way the fetch can fail. It says nothing about which one it was.
const reasonLoginHandover = "login-handover"

func init() {
	sqlReasonSentences[reasonLoginHandover] = "The saved login could not be fetched from the Unravis Key Keeper. It may have been used already or have waited too long. Start the set-up again from Unravis."
}

// dialKeeper is the production dialer: it insists the answering program is the keeper's Windows service (session 0, a trusted owner of
// the pipe), so a program that took the pipe name gets nothing. A test seam.
var dialKeeper = func(pipe string) keeperwire.Dialer { return keeperwire.NewPipeDialer(pipe) }

var errHandover = errors.New("the saved login could not be fetched from the key keeper")

const keeperPipePrefix = `\\.\pipe\`

// validHandover accepts only a 32 character hex id and a pipe name that is a plain name under \\.\pipe\ (no path, no other machine), so a
// bad command line cannot point this elevated program anywhere else.
func validHandover(id, pipe string) bool {
	if len(id) != 32 || len(pipe) <= len(keeperPipePrefix) || len(pipe) > 128 || !strings.HasPrefix(pipe, keeperPipePrefix) {
		return false
	}
	for _, c := range id {
		if !isLowerHex(c) {
			return false
		}
	}
	name := pipe[len(keeperPipePrefix):]
	// The name starts with a letter or digit, so it can never be "." or ".." (which a path cleaner could turn into the pipe folder itself).
	if !isAlnum(rune(name[0])) {
		return false
	}
	for _, c := range name {
		if !isAlnum(c) && c != '.' && c != '_' && c != '-' {
			return false
		}
	}
	return true
}

func isLowerHex(c rune) bool { return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' }

func isAlnum(c rune) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// fetchHandover asks the keeper for the login held under id. The answer is the login document, which the caller wipes after use.
func fetchHandover(ctx context.Context, id, pipe string) ([]byte, error) {
	if !validHandover(id, pipe) {
		return nil, errHandover
	}
	ctx, cancel := context.WithTimeout(ctx, handoverTimeout)
	defer cancel()
	conn, err := dialKeeper(pipe).Dial(ctx)
	if err != nil {
		return nil, errHandover
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(handoverTimeout))
	args, err := json.Marshal(struct {
		ID string `json:"id"`
	}{id})
	if err != nil {
		return nil, errHandover
	}
	if err := keeperwire.WriteJSONFrame(conn, keeperwire.Request{V: keeperwire.ProtocolVersion, Op: keeperwire.OpSQLHandoverTake, Args: args}); err != nil {
		return nil, errHandover
	}
	frame, err := keeperwire.ReadFrame(conn)
	if err != nil {
		return nil, errHandover
	}
	defer wipe(frame)
	var resp keeperwire.Response
	if json.Unmarshal(frame, &resp) != nil || !resp.OK || len(resp.Result) == 0 || string(resp.Result) == "null" {
		wipe(resp.Result)
		return nil, errHandover
	}
	doc := append([]byte(nil), resp.Result...)
	wipe(resp.Result)
	return doc, nil
}
