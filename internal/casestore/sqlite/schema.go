package sqlite

// schemaVersion is stored in meta. A case written by a newer build is refused.
const schemaVersion = 1

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
`

// pageSize is SQLite's default and the adiantum block size.
const pageSize = 4096
