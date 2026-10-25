package sqlite

// schemaVersion is stored in meta. A case written by a newer build is refused.
// Version 2 added the host discovery tables (discoverSQL); a version 1 case is
// migrated on open, adding the tables and nothing else. Version 3 added the two
// series-key indexes (attrIndexSQL): a version 2 case is migrated on open by
// writing the version only, and the indexes are built the first time a query
// asks for an attribute they serve (Case.ensureAttrIndexes), or by the next
// import. A case written without them stays correct, only slower.
const schemaVersion = 3

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
`

const indexSQL = `
CREATE INDEX IF NOT EXISTS ev_ts ON events(ts_ms);
CREATE INDEX IF NOT EXISTS ev_kind_ts ON events(kind, ts_ms);
CREATE INDEX IF NOT EXISTS ev_import ON events(import_id);
` + attrIndexSQL

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
