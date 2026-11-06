package sqlite

import (
	"fmt"
	"math"
)

// schemaVersion is stored in meta. A case written by a newer build is refused.
// Version 2 added the host discovery tables (discoverSQL); a version 1 case is
// migrated on open, adding the tables and nothing else. Version 3 added the two
// series-key indexes (attrIndexSQL): a version 2 case is migrated on open by
// writing the version only, and the indexes are built the first time a query
// asks for an attribute they serve (Case.ensureAttrIndexes), or by the next
// import. A case written without them stays correct, only slower. Version 4
// added the environment and source-tag tables (tagsSQL): a case below 4 is
// migrated on open by adding those two small tables and writing the version, in
// one transaction. No existing table or column changes.
const schemaVersion = 4

// refusesSchema reports whether a build that knows schema version known must
// refuse a case stored at version v: one written by a newer build. A schema-4
// case therefore never opens in a build that knows only 3, so a rolled-back
// binary cannot write into tables it does not understand.
func refusesSchema(v, known int) bool { return v > known }

// schemaSQL is the case schema (importer wave plan section 1.5). The three
// indexes are not part of it: they are created after each bulk load, inside the
// import transaction (storage decision, "Benchmark").
const schemaSQL = `
CREATE TABLE meta(k TEXT PRIMARY KEY, v TEXT NOT NULL);
CREATE TABLE imports(id INTEGER PRIMARY KEY, importer TEXT NOT NULL, file_name TEXT NOT NULL, sha256 TEXT NOT NULL UNIQUE,
  bytes INTEGER, rows INTEGER, rejected INTEGER, host_label TEXT, tz_rule TEXT, quality_json TEXT, gaps_json TEXT,
  imported_utc INTEGER NOT NULL, status TEXT NOT NULL, ack_gating INTEGER NOT NULL CHECK(ack_gating=1), ack_text TEXT NOT NULL);
CREATE TABLE events(evidence_id TEXT PRIMARY KEY, import_id INTEGER NOT NULL REFERENCES imports(id) ON DELETE CASCADE,
  source TEXT, kind TEXT, host TEXT, ts_ms INTEGER NOT NULL, te_ms INTEGER NOT NULL, duration_ms REAL, value REAL,
  unit TEXT, severity TEXT, line INTEGER, tz_rule TEXT, attrs_json TEXT) WITHOUT ROWID;
CREATE TABLE quarantine(id INTEGER PRIMARY KEY, importer TEXT, file_name TEXT, sha256 TEXT, reason TEXT,
  column_name TEXT, rows_seen INTEGER, at_utc INTEGER);
` + tagsSQL

// tagsSQL is the schema-4 addition: the environment names and the source tags
// (casestore.Tagger). It is written with IF NOT EXISTS so a new case, an old case
// being migrated and a repeat of the migration all run the same text. A source
// row holds only short words the user chose: no path, connection string or secret.
const tagsSQL = `
CREATE TABLE IF NOT EXISTS case_sources(id INTEGER PRIMARY KEY, import_id INTEGER, kind TEXT NOT NULL, label TEXT, env TEXT,
  portal TEXT, api TEXT, classification TEXT, created_ms INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS case_envs(name TEXT PRIMARY KEY, ord INTEGER);
`

const coreIndexSQL = `
CREATE INDEX IF NOT EXISTS ev_ts ON events(ts_ms);
CREATE INDEX IF NOT EXISTS ev_kind_ts ON events(kind, ts_ms);
CREATE INDEX IF NOT EXISTS ev_import ON events(import_id);
` + attrIndexSQL

// indexSQL is every index an import makes in its own transaction. The timeline
// indexes are left out of it for a case that already holds events without them (see
// timelineLate): those are built after the commit, best effort.
const indexSQL = coreIndexSQL + timelineIndexSQL

// statusIs5xxExpr is the test BucketCounts uses for a server error (a status
// stored as exactly three digits starting with 5, the same rule as
// casestore.IsServerErrorStatus). It is written once so that the partial index
// below and the query carry the identical expression, which is how SQLite
// recognises that the index serves the query. The pattern must stay a literal.
const statusIs4xxExpr = `json_extract(attrs_json,'$.status') GLOB '4[0-9][0-9]'`

// timingBinExpr is audit.BinIndex over duration_ms in SQL: bin 0 under 1 ms,
// the overflow bin (193) from 1.1^192 up, else floor(ln(ms)/ln(1.1))+1. It may
// differ from BinIndex by one bin at an exact boundary, which the reader of the
// bins tolerates (it clips to the observed minimum and maximum).
var timingBinExpr = fmt.Sprintf(`CASE WHEN duration_ms < 1 THEN 0 WHEN duration_ms >= %v THEN 193 `+
	`ELSE CAST(ln(duration_ms) / %v AS INTEGER) + 1 END`, math.Pow(1.1, 192), math.Log(1.1))

const statusIs5xxExpr =`json_extract(attrs_json,'$.status') GLOB '5[0-9][0-9]'`

// timelineIndexSQL are the two indexes the investigation timeline reads. They
// carry no schema-version bump: a case without them is correct, only slow, and
// they are built in the background when such a case is opened
// (Case.ensureTimelineIndexes) or by the next import.
//
// ev_src_ts serves the per-lane window reads and the bucket counts, which filter
// by source and a time range: without it every row in the range costs a random
// lookup in the (WITHOUT ROWID) table, whatever its source. import_id is last so
// the grouped count is answered from the index alone.
//
// ev_err_ts is partial: only the server-error rows are in it, so it is small, and
// the error count of a bucket reads it instead of parsing attrs_json per row.
const timelineIndexSQL = `
CREATE INDEX IF NOT EXISTS ev_src_ts ON events(source, ts_ms, import_id);
CREATE INDEX IF NOT EXISTS ev_err_ts ON events(source, ts_ms, import_id) WHERE ` + statusIs5xxExpr + `;
`

// timelineIndexNames lists the indexes of timelineIndexSQL.
var timelineIndexNames = []string{"ev_src_ts", "ev_err_ts"}

// attrIndexSQL is the schema-3 addition: the series key of a counter or a metric
// event, so a query for one series reads only its events. They are partial
// (only events that carry the attribute) so a log-heavy case pays nothing, and
// the expressions are exactly those eventFilter writes, which is how SQLite
// recognises them. Keep the two in step: attrIndexNames lists the attributes.
const attrIndexSQL = `
CREATE INDEX IF NOT EXISTS ev_kind_counter ON events(kind, json_extract(attrs_json,'$.counter'),
  json_extract(attrs_json,'$.object'), json_extract(attrs_json,'$.instance'), ts_ms)
  WHERE json_extract(attrs_json,'$.counter') IS NOT NULL;
CREATE INDEX IF NOT EXISTS ev_kind_metric ON events(kind, json_extract(attrs_json,'$.metric'),
  json_extract(attrs_json,'$.process'), json_extract(attrs_json,'$.pid'), ts_ms)
  WHERE json_extract(attrs_json,'$.metric') IS NOT NULL;
`

// attrIndexNames are the attributes that lead the two indexes of attrIndexSQL
// (casestore.IndexedAttrs): a query that names one of them reads the index.
var attrIndexNames = []string{"counter", "metric"}

// attrIndexColumns are every attribute the indexes hold. A filter on one of
// them is written as the literal path the index uses (so SQLite can match the
// expression and test it on the index entry, without reading the row); any
// other attribute goes through a bound path and is tested on the row.
var attrIndexColumns = []string{"counter", "metric", "object", "instance", "process", "pid"}

// pageSize is SQLite's default and the adiantum block size.
const pageSize = 4096
