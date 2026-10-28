package keeper

import (
	"context"
	"errors"
	"os"

	"api-audit-explorer/internal/bbsaved"
	"api-audit-explorer/internal/history"
	"api-audit-explorer/internal/sqlconn"
)

// Legacy is the person's old per-user saved files (sealed for them, in their own profile), which the move copies into the keeper. Any
// field may be nil.
type Legacy struct {
	SQL   *sqlconn.Service
	Mongo *history.Service
	BB    *bbsaved.Store
}

// Pending counts the old records not yet in the keeper. Duplicates counts the old records whose address the keeper already holds under
// another id (someone else on this PC saved it first, so the move skipped the person's copy): it is not in Total, but removing the old
// files needs the person to confirm them, because the keeper's copy (and its password) is not theirs.
type Pending struct {
	SQL        int `json:"sql"`
	Mongo      int `json:"mongo"`
	Blackbox   int `json:"blackbox"`
	Duplicates int `json:"duplicates,omitempty"`
}

// Total is the sum of the missing counts (Duplicates is not in it).
func (p Pending) Total() int { return p.SQL + p.Mongo + p.Blackbox }

// Migration moves the old per-user saved files into the keeper. The old files are removed only by RemoveOld, after the person confirms
// and only once every old record is verified in the keeper.
type Migration struct {
	Legacy Legacy
	Client *Client
}

// ErrNotVerified means an old record is not (yet) in the keeper with its secret, so the old files are kept.
var ErrNotVerified = errors.New("not every old saved record is in the key keeper yet")

// ErrDuplicatesUnconfirmed means some old records were kept in the keeper only as someone else's copy, and the person has not confirmed
// that their own copies may go.
var ErrDuplicatesUnconfirmed = errors.New("some old saved records are in the key keeper only as someone else's copy")

// match is how an old record stands against the keeper.
type match int

const (
	missing   match = iota
	verified        // the keeper holds this very record (same id), with its secret when it had one
	duplicate       // the keeper holds the same address under another id: someone else's record, kept instead of this one
)

func exists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// OldFilesPresent reports whether any old per-user saved file is still on disk.
func (m *Migration) OldFilesPresent() bool {
	return (m.Legacy.SQL != nil && exists(m.Legacy.SQL.Path())) || (m.Legacy.Mongo != nil && exists(m.Legacy.Mongo.Path())) || (m.Legacy.BB != nil && exists(m.Legacy.BB.Path()))
}

// keeperView is what the keeper lists, by id and by address.
type keeperView struct {
	sql   []sqlconn.Recent
	mongo []history.Recent
	bb    []bbsaved.Entry
}

func (m *Migration) view() (keeperView, error) {
	var v keeperView
	var err error
	if m.Legacy.SQL != nil {
		if v.sql, err = NewSQLStore(m.Client).List(); err != nil {
			return v, err
		}
	}
	if m.Legacy.Mongo != nil {
		if v.mongo, err = NewMongoStore(m.Client).List(); err != nil {
			return v, err
		}
	}
	if m.Legacy.BB != nil {
		if v.bb, err = NewBBStore(m.Client).List(); err != nil {
			return v, err
		}
	}
	return v, nil
}

// Verification is by id only: the import keeps ids, so the person's own record is in the keeper under its own id. A record at the same
// address under another id is someone else's (the import skipped the person's copy) and never counts as the person's being verified;
// when that other copy lacks the secret the person's copy has, the person's secret is in no keeper record, so it counts as missing.
func sqlIn(v keeperView, r sqlconn.Record) match {
	dup := false
	for _, k := range v.sql {
		if k.ID == r.ID {
			if !r.HasPassword || k.HasPassword {
				return verified
			}
			return missing
		}
		if k.Host == r.Host && k.Port == r.Port && k.Username == r.Username && k.Auth == r.Auth && equalFold(k.Database, r.Database) && (!r.HasPassword || k.HasPassword) {
			dup = true
		}
	}
	if dup {
		return duplicate
	}
	return missing
}

func mongoIn(v keeperView, r history.Record) match {
	dup := false
	for _, k := range v.mongo {
		if k.ID == r.ID {
			if !r.HasCredentials || k.HasCredentials {
				return verified
			}
			return missing
		}
		if k.URI == r.URI && k.Username == r.Username && k.AuthSource == r.AuthSource && k.TLSMode == r.TLSMode && k.CACertificate == r.CACertificate && (!r.HasCredentials || k.HasCredentials) {
			dup = true
		}
	}
	if dup {
		return duplicate
	}
	return missing
}

func bbIn(v keeperView, r bbsaved.Record) match {
	dup := false
	for _, k := range v.bb {
		if k.ID == r.ID {
			if !r.HasKey || k.HasKey {
				return verified
			}
			return missing
		}
		if (k.Canon == r.Canon || (r.RecorderHost != "" && r.RecorderName != "" && equalFold(k.RecorderHost, r.RecorderHost) && equalFold(k.RecorderName, r.RecorderName))) && (!r.HasKey || k.HasKey) {
			dup = true
		}
	}
	if dup {
		return duplicate
	}
	return missing
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		x, y := a[i], b[i]
		if 'A' <= x && x <= 'Z' {
			x += 'a' - 'A'
		}
		if 'A' <= y && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}

type legacyRecords struct {
	sql   []sqlconn.Record
	mongo []history.Record
	bb    []bbsaved.Record
}

func (m *Migration) read() (legacyRecords, error) {
	var l legacyRecords
	var err error
	if m.Legacy.SQL != nil && exists(m.Legacy.SQL.Path()) {
		if l.sql, err = m.Legacy.SQL.Export(); err != nil {
			return l, err
		}
	}
	if m.Legacy.Mongo != nil && exists(m.Legacy.Mongo.Path()) {
		if l.mongo, err = m.Legacy.Mongo.Export(); err != nil {
			return l, err
		}
	}
	if m.Legacy.BB != nil && exists(m.Legacy.BB.Path()) {
		if l.bb, err = m.Legacy.BB.Export(); err != nil {
			return l, err
		}
	}
	return l, nil
}

func count(l legacyRecords, v keeperView) Pending {
	var p Pending
	tally := func(m match, missingCount *int) {
		switch m {
		case missing:
			*missingCount++
		case duplicate:
			p.Duplicates++
		}
	}
	for _, r := range l.sql {
		tally(sqlIn(v, r), &p.SQL)
	}
	for _, r := range l.mongo {
		tally(mongoIn(v, r), &p.Mongo)
	}
	for _, r := range l.bb {
		tally(bbIn(v, r), &p.Blackbox)
	}
	return p
}

// Pending counts the old records the keeper does not have yet. It needs the keeper running.
func (m *Migration) Pending() (Pending, error) {
	l, err := m.read()
	if err != nil {
		return Pending{}, err
	}
	if len(l.sql)+len(l.mongo)+len(l.bb) == 0 {
		return Pending{}, nil
	}
	v, err := m.view()
	if err != nil {
		return Pending{}, err
	}
	return count(l, v), nil
}

// Migrate copies every old record into the keeper (ids kept, records already there skipped) and reports what is still missing after.
// The old files are not touched.
func (m *Migration) Migrate(ctx context.Context) (Pending, error) {
	l, err := m.read()
	if err != nil {
		return Pending{}, err
	}
	if len(l.sql) > 0 {
		if err := m.Client.Call(ctx, OpSQLImport, struct {
			Records []sqlconn.Record `json:"records"`
		}{l.sql}, nil); err != nil {
			return Pending{}, native(err, kindSQL)
		}
	}
	if len(l.mongo) > 0 {
		// A record saved without "remember password" carries no password in the file, so there is nothing to strip.
		if err := m.Client.Call(ctx, OpMongoImport, struct {
			Records []history.Record `json:"records"`
		}{l.mongo}, nil); err != nil {
			return Pending{}, native(err, kindMongo)
		}
	}
	if len(l.bb) > 0 {
		if err := m.Client.Call(ctx, OpBBImport, struct {
			Records []bbsaved.Record `json:"records"`
		}{l.bb}, nil); err != nil {
			return Pending{}, native(err, kindBB)
		}
	}
	v, err := m.view()
	if err != nil {
		return Pending{}, err
	}
	return count(l, v), nil
}

// RemoveOld deletes the old per-user files once every record in them is verified in the keeper with its secret (by id). Records the
// keeper holds only as someone else's copy need confirmDuplicates. It is called only after the person confirms on the page.
func (m *Migration) RemoveOld(confirmDuplicates bool) error {
	l, err := m.read()
	if err != nil {
		return err
	}
	v, err := m.view()
	if err != nil {
		return err
	}
	p := count(l, v)
	if p.Total() != 0 {
		return ErrNotVerified
	}
	if p.Duplicates > 0 && !confirmDuplicates {
		return ErrDuplicatesUnconfirmed
	}
	for _, p := range []string{pathOf(m.Legacy.SQL), pathOf(m.Legacy.Mongo), pathOf(m.Legacy.BB)} {
		if p == "" {
			continue
		}
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func pathOf(s interface{ Path() string }) string {
	switch v := s.(type) {
	case *sqlconn.Service:
		if v == nil {
			return ""
		}
	case *history.Service:
		if v == nil {
			return ""
		}
	case *bbsaved.Store:
		if v == nil {
			return ""
		}
	}
	return s.Path()
}
