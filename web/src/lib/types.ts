/** http is synchronous, asynchronous and unknown HTTP rows together; http_unknown is only the unknown ones. */
export type Kind = 'synchronous' | 'asynchronous' | 'hangfire' | 'http' | 'http_unknown' | 'all';
export type Outcome = 'success' | 'failure' | 'client_error' | 'server_error' | 'all';
export type Metric = 'total' | 'api';

/** keeper (P14b): "held" when the key keeper opened the source from a saved link and answers its reads, "stopped" when it cannot be reached now. */
export interface Source { selectionKey?: string; connectionId?: string; connectionLabel?: string; id: string; label: string; database: string; collection: string; keeper?: 'held' | 'stopped' }
export type TLSMode = '' | 'uri' | 'system' | 'custom' | 'insecure' | 'off';
export interface ConnectionInput { uri: string; username: string; password: string; authSource: string; tlsMode: TLSMode; caCertificate: string; savedConnectionId?: string }
export interface ConnectionTestResult { databases: string[]; warning?: string }
export interface SourceInput extends ConnectionInput { connectionLabel?: string; label: string; database: string; collection: string }
export interface RecentConnection {
  id: string; label: string; uri: string; username: string; authSource: string; tlsMode: TLSMode;
  caCertificate: string; collection: string; hasCredentials: boolean; lastUsedAt: string;
}
/** SQL Server details as SQL Server Management Studio asks for them. Stored only; nothing connects with them yet. */
export type SqlAuth = 'sql' | 'windows';
export type SqlEncrypt = 'mandatory' | 'optional' | 'strict';
export interface SqlConnectionInput {
  label: string; server: string; auth: SqlAuth; username: string; password: string; rememberPassword: boolean;
  database: string; encrypt: SqlEncrypt; trustServerCertificate: boolean;
}
/** A saved SQL Server entry. The password is never returned, only whether one is stored. */
export interface SavedSqlConnection {
  id: string; label: string; server: string; host: string; port: number; auth: SqlAuth; username: string; database: string;
  encrypt: SqlEncrypt; trustServerCertificate: boolean; hasPassword: boolean; savedAt: string;
}
/** The answer to "Test and list databases" for SQL Server. A login that connects but cannot list databases still succeeds (listError says why). */
export interface SqlTestResult { ok: boolean; version: string; edition: string; databases: string[]; listError?: string }
/** What the server accepts for a test or a check: a typed login, or a kept one (savedId) with a password or database typed for this run only. */
export interface SqlTestRequest extends Partial<SqlConnectionInput> { savedId?: string }
export interface RememberConnectionInput { label: string; connection: ConnectionInput; collection: string; rememberPassword: boolean }
export interface SourceResult { sourceId: string; ok: boolean; error: string; version: string; indexWarning: string }
export interface Info {
  demo: boolean;
  /** Set only by the capture-only demo cases: the demo has cases, so Home and Case home show. */
  captureCases?: boolean;
  limits: { maxSources: number; maxPageSize: number; maxSummaryRows: number; maxTimeBuckets: number };
  now: string;
}
/** What /api/version answers: the build's stamped version, commit and date. Empty strings mean not stamped. */
export interface VersionInfo { version: string; commit: string; date: string }
export interface UpdateCheckReply { status: 'latest' | 'available' | 'dev' | 'unreachable'; current: string; latest: string; url: string }
/** Record-level completion: complete rows have a usable duration, pending rows are accepted asynchronous jobs. */
export type Completion = 'all' | 'complete' | 'incomplete' | 'pending' | 'invalid';
/** An inclusive numeric bound; either end may be absent, and a range with neither end is not in use. */
export interface NumberRange { min?: number; max?: number }
/** The range filters the server accepts, with the JSON key each travels under. */
export type RangeKey = 'observedMs' | 'apiTimeMs' | 'auditCreateTimeMs' | 'requestKb' | 'responseKb' | 'incompleteAgeMs';
/** Additive record filters; every field is absent when it is not in use and the server drops absent ones. */
export interface RecordFilters {
  userIds?: number[]; uiVersions?: string[]; userAgentContains?: string; userIps?: string[]; urlContains?: string;
  auditId?: string; statusCodes?: string[]; completion?: Completion; /** "Controller.Action", split on the first dot. */ operation?: string;
  /** Range filters. A time range implies Complete and the age range implies Incomplete or Pending; a row without the field never matches a bounded range. */
  observedMs?: NumberRange; apiTimeMs?: NumberRange; auditCreateTimeMs?: NumberRange;
  requestKb?: NumberRange; responseKb?: NumberRange; incompleteAgeMs?: NumberRange;
}
/** How the server reads records. Absent or "exact" reads every match; "hint" reads a narrow _id range (API only, no UI control). */
export type QueryMode = 'exact' | 'hint';
export interface Query extends RecordFilters {
  /** Additive: absent means exact. */
  queryMode?: QueryMode | '';
  sourceIds: string[]; start: string; end: string; baselineStart: string; baselineEnd: string;
  kind: Kind; outcome: Outcome; metric: Metric; bucketSeconds: number; controller: string; action: string;
}
export interface Quantile { lowerMs: number; upperMs: number | null; valueMs: number | null }
export interface BinCount { bin: number; count: number }
export interface Stats {
  count: number; meanMs: number | null; minMs: number | null; maxMs: number | null;
  p50: Quantile | null; p95: Quantile | null; histogram: BinCount[];
  /** Additive: absent from older payloads. p99 is also absent below 100 samples. */
  p90?: Quantile | null; p99?: Quantile | null;
  /** Additive: mean of each phase over the documents that carry it; absent when none do. */
  phases?: PhaseMeans | null;
  /** Additive: documents of this cohort that were 4xx (clientErrors) or 5xx plus failed Hangfire jobs (serverErrors), and their share of the cohort's events; rates are absent for unknown-outcome cohorts. */
  clientErrors?: number; serverErrors?: number; clientErrorRate?: number | null; serverErrorRate?: number | null;
  /** Additive: valid samples whose observed time lacks AuditCreateTime (legacy rows without RequestInitiatedAt); absent when none. */
  observedPartial?: number;
  /** Additive: events is every document of the cohort; count (valid), incomplete (unfinished, not pending), pending (accepted asynchronous work) and invalid add up to it, each absent when zero. completionRate = count / (count + incomplete + pending); invalid is never in the denominator and the rate is absent when it is 0. */
  events?: number; incomplete?: number; pending?: number; invalid?: number; completionRate?: number | null;
}
/** Audit and api time add up to the total; payload is written after the response and is not latency. Each is absent without samples; in phaseDeltaMs it is current minus reference. */
export interface PhaseMeans { auditMs?: number | null; apiMs?: number | null; payloadMs?: number | null }
export interface Comparison {
  key: string; sourceId: string; controller: string; action: string; kind: string; outcome: string;
  state: string; current: Stats; reference: Stats; medianRatio: number | null; p95Ratio: number | null;
  medianDeltaMs: number | null; p95DeltaMs: number | null; currentEvents: number; referenceEvents: number;
  /** Additive: absent when undefined (fewer than 100 samples in either window). */
  p99Ratio?: number | null; p99DeltaMs?: number | null;
  /** Additive. impactMs = (current mean - reference mean) x current count; absent unless state is compared. */
  impactMs?: number | null; totalTimeMs?: number;
  /** Mann-Whitney z from histogram counts (positive = current slower); absent below 20 samples. */
  shiftZ?: number | null; significant?: boolean;
  /** Additive: change in each phase's mean (current minus reference); a phase is absent when either window has no samples for it. */
  phaseDeltaMs?: PhaseMeans | null;
}
/** count, p50, p95 and the other percentiles describe the slot's valid timings; count is null only for a chart placeholder that has no point. Additive: count4xx, count5xx (5xx plus failed Hangfire jobs) and incomplete count every state and are absent when zero; p99 is absent below 100 samples. */
export interface Point { at: string; count: number | null; p50: Quantile | null; p95: Quantile | null; p95Ratio: number | null; count4xx?: number; count5xx?: number; incomplete?: number; p90?: Quantile | null; p99?: Quantile | null }
/** loadCorrelation: Spearman rho of other traffic's estimated concurrency vs this series' per-bucket P95; absent below 8 paired buckets. */
export interface Series { key: string; points: Point[]; loadCorrelation?: number | null }
/** Per-API, per-kind current-window throughput (sources merged); requests and failures count known outcomes only. errorRate is absent when requests is 0. */
export interface Rollup {
  controller: string; action: string; kind?: string; requests: number; failures: number; errorRate?: number | null; perMinute: number;
  /** Additive split of errorRate: 4xx, and 5xx plus failed Hangfire jobs. Absent when errorRate is, or under an outcome filter. */
  clientErrorRate?: number | null; serverErrorRate?: number | null;
  /** Additive, Task 12: all events and valid completions in the current window, and the same measures for the reference window. Reference rates, P95 and every delta are absent when the reference has fewer than 20 valid samples. */
  events?: number; completed?: number; completionRate?: number | null; p95Ms?: number | null;
  referenceRequests?: number; referenceFailures?: number; referenceEvents?: number; referenceCompleted?: number;
  referenceErrorRate?: number | null; referenceClientErrorRate?: number | null; referenceServerErrorRate?: number | null;
  referenceCompletionRate?: number | null; referenceP95Ms?: number | null;
  /** trafficDelta is a relative change (0.25 = +25%); the rate deltas are percentage points as fractions; p95DeltaMs is milliseconds. */
  trafficDelta?: number | null; completionDelta?: number | null; clientErrorDelta?: number | null; serverErrorDelta?: number | null; p95DeltaMs?: number | null;
  /** Additive: the current window's events that are not completed, split into incomplete (not pending), pending (accepted asynchronous work) and invalid, each absent when zero. completionShare = completed / (completed + incomplete + pending): invalid is never in the denominator and the share is absent when it is 0. completionRate above stays completed / events. */
  incomplete?: number; pending?: number; invalid?: number; completionShare?: number | null;
}
/** pendingAsync is additive and absent when zero: Asynchronous requests that only answered 202 Accepted, counted inside incomplete. */
export interface QualityCounts { incomplete: number; invalid: number; unknownOutcome: number; pendingAsync?: number }
/** byKind splits the counts by row kind (synchronous, asynchronous, hangfire, unknown); only kinds with something to report appear. */
export interface Quality { incomplete: number; invalid: number; unknownOutcome: number; pendingAsync?: number; byKind?: Record<string, QualityCounts> }
/** One window of one kind's overview. The reference window's rates and quantiles are absent below 20 valid samples; the current window is not gated on valid samples: its completion share needs only events above zero and its error shares need events above zero, known outcomes with every outcome returned, and status counters that account for every failure (so they can appear with no valid sample), and its quantiles need at least one valid sample, so the row's state (insufficient_data) is what flags them as rough. Incomplete and pending rows are counted in events but never treated as fast or successful. */
export interface OverviewWindow {
  events: number; completed: number; incomplete: number; invalid: number; unknownOutcome: number; clientErrors: number; serverErrors: number;
  perMinute: number; completionRate?: number | null; clientErrorRate?: number | null; serverErrorRate?: number | null;
  p50?: Quantile | null; p90?: Quantile | null; p95?: Quantile | null; p99?: Quantile | null;
}
/** Deltas against the reference window; every one is absent unless the reference has at least 20 valid samples. Traffic is relative, rates are percentage points as fractions, latency is ms. */
export interface OverviewDelta {
  traffic?: number | null; completionRate?: number | null; clientErrorRate?: number | null; serverErrorRate?: number | null;
  p50Ms?: number | null; p90Ms?: number | null; p95Ms?: number | null; p99Ms?: number | null;
}
/** One row per kind in scope (synchronous, asynchronous, hangfire, plus unknown only when present); histograms are never merged across kinds. */
export interface OverviewRow extends OverviewWindow {
  kind: string; state: string; reference: OverviewWindow; delta: OverviewDelta;
}
/** The measured gap between an ObjectId time and RequestInitiatedAt, used to bound a hint-mode read. */
export interface IDMargin {
  marginMs: number; observedMs: number; padMs: number; percentile: number; sampleSize: number;
  sampleFrom: string; sampleTo: string; calibratedAt: string; label: string;
}
export type ProvenanceWindow = 'current' | 'reference' | 'page' | 'breakdown' | 'coverage';
export type ScanClass = 'narrow' | 'broad';
export type Completeness = 'complete' | 'ran_to_end' | 'partial' | 'hint';
export type PartialReason = 'timeout' | 'rowcap' | 'error';
/** How one read of one source and window ran. docsExamined is null when it was not measured (always on a live source). */
export interface Provenance {
  sourceId: string; window: ProvenanceWindow | string; start: string; end: string;
  queryMode: QueryMode | string; scanClass: ScanClass | string; completeness: Completeness | string;
  partialReason?: PartialReason | string;
  docsReturned: number; docsExamined: number | null; sampleSize: number; elapsedMs: number; maxTimeMs: number;
  margin?: IDMargin; note?: string;
}
export interface Analysis {
  /** Additive: absent from older servers. */
  provenance?: Provenance[];
  overview?: OverviewRow[];
  loadWindows?: LoadWindow[];
  requestVolume?: VolumeBucket[];
  query: Query; apis: Comparison[]; timeline: Series[]; sources: SourceResult[];
  quality: Quality; approximate: boolean; warnings: string[];
  timings?: SourceTiming[];
  rollups?: Rollup[];
}
export interface SourceTiming { sourceId: string; window: 'current' | 'reference'; readMs: number; rows: number }
export interface FieldPresence { name: string; present: number; types: string[] }
export interface SchemaCensus { sampled: number; fields: FieldPresence[] }
export interface RequestRecord {
  id: string; sourceId: string; controller: string; action: string; kind: string; outcome: string;
  /** Additive: the row's own state (valid, incomplete or invalid) as the summary classed it. */
  state?: string;
  status: string; url: string; initiatedAt: string | null; completedAt: string | null;
  durationMs: number | null; apiTimeMs: number | null; auditCreateTimeMs: number | null;
  payloadRecordTimeMs: number | null; requestKB: number; responseKB: number;
  /** Additive audit-evidence columns: absent from older servers and when the stored field is empty. */
  userId?: number; userIp?: string; userAgent?: string; uiVersion?: string;
  createdOn?: string; updatedOn?: string; pollingTypeRaw?: string;
}
export interface Page { rows: RequestRecord[]; nextCursor: string; sources: SourceResult[]; truncated: boolean; /** Additive: absent from older servers. */ provenance?: Provenance[] }
export interface Payload { request: string; response: string; truncated: boolean }
/** One record with every schema field, its derived kind/state/outcome, and capped payloads. */
export interface RecordDetail extends RequestRecord { state: string; pendingAsync?: boolean; payload: Payload }
export interface ActivityInput { sourceIds: string[]; start: string; end: string; cursor: string; limit: number }
export interface VolumeBucket { controller: string; action: string; at: string; count: number }
export interface LoadWindow { at:string; count:number; timing:Stats }
export type BreakdownDim = 'statusCode' | 'userId' | 'uiVersion' | 'userAgent' | 'userIp' | 'pollingType' | 'hourOfDay' | 'requestSizeBucket';
/** The Query fragment of a breakdown row: a few of the record filters, merged into the query to list the row's requests. The polling type, a missing value, a value that may have been cut and the Other row have none. */
export type BreakdownFilter = Pick<RecordFilters, 'userIds' | 'uiVersions' | 'userAgentContains' | 'userIps' | 'statusCodes'>;
/** One value of a breakdown dimension. filter selects this value (a user agent filter is a case-insensitive substring, so it can list more rows than the count); stats percentiles come from valid completed records only. */
export interface BreakdownRow {
  value: string; missing: boolean; count: number; complete: number; incomplete: number; pending: number;
  count4xx: number; count5xx: number; stats: Stats; filter?: BreakdownFilter; values?: number;
}
/** pooledKinds is set when the query kind (All, HTTP) pools request types: counts stay, but no row carries a percentile. */
export interface Breakdown { dim: BreakdownDim; rows: BreakdownRow[]; other: BreakdownRow | null; total: number; distinct: number; pooledKinds?: boolean; sources: SourceResult[]; /** Additive: absent from older servers. */ provenance?: Provenance[] }
export interface BreakdownInput { query: Query; dim: BreakdownDim; top: number; /** Only hourOfDay reads it: the zone the hours are cut in. */ tz?: string }
/** Coverage census (GET /api/sources/{id}/coverage). Pct runs 0 to 100 with one decimal. */
export interface FieldCoverage { nonNull: number; pct: number }
export interface CoverageRow {
  /** synchronous, asynchronous, hangfire or unknown; the total row has kind "all" and an empty day. */
  kind: string; day: string; docs: number;
  requestInitiatedAt: FieldCoverage; createdOn: FieldCoverage; apiTime: FieldCoverage; accountId: FieldCoverage;
}
export interface CoverageReport {
  sourceId: string; rows: CoverageRow[]; total: CoverageRow;
  /** "available", or the unavailable sentence with the populated share. */
  tenantAttribution: string; provenance: Provenance[];
}
export interface CoverageRefused { refused: string }

/** P2-lite incident patterns (POST /api/patterns). Mirrors internal/audit/patterns_types.go. */
export interface PatternInput {
  sourceIds: string[]; start: string; end: string; stallMs?: number; episodeGapSeconds?: number; /** Opt in to B6g, which reads every row in the window. */ idleGaps?: boolean;
  /** The display zone (an IANA name) for B9 and the audit-create series. Empty or absent means UTC. */
  timeZone?: string;
  /** Opt in to B9: the window it dates a step in, up to 120 days. It is separate from the main window. */
  stepChange?: { start: string; end: string };
  /** Opt in to the audit-create series and the fixed-duration check. */
  auditSeries?: boolean;
}
export interface PatternParams { stallMs: number; episodeGapSeconds: number; auditStallMs: number; auditStallShare: number; volleyWindowMs: number }
export interface PatternStall { sourceId: string; id: string; controller: string; action: string; initiatedAt: string; completedAt?: string; observedMs: number; apiTimeMs?: number; userId?: number; episode: number }
export interface PatternEpisode { index: number; start: string; end: string; requests: number; users: number; maxMs: number; operations: string[] }
export interface PatternVolley { episode: number; userId: number; requests: number; firstStart: string; startSpreadMs: number; endStaggerMs: number; endGapsMs: number[]; incompleteEnds: number }
export interface PatternAuditStall { sourceId: string; id: string; controller: string; action: string; initiatedAt: string; observedMs: number; auditCreateTimeMs: number; share: number }
export type EvidenceLabel = 'Symptom' | 'Correlation' | 'Strong correlation';
export interface PatternFinding { rule: 'B1' | 'B2' | 'B6' | 'B12' | 'B3a' | 'A3' | 'B12r' | 'B6g' | 'B9' | 'B6f'; label: EvidenceLabel; headline: string; n: number; lowN: boolean; evidenceId: string }
/** B3a: a minute of the hour at which stall episodes started on several days. `minute` is 0 to 59; `episodes` are the episode indexes, or their count. */
export interface PatternOnsetMinute { minute: number; days: number; onsets: number; episodes: number | Array<number | string> }
/** A3: one operation or user whose share of stall requests is compared with its share of all requests. Shares are fractions from 0 to 1. `episodes` is a count, or the episode indexes. */
export interface PatternConcentration {
  dimension: 'operation' | 'user'; key: string; stallShare: number; trafficShare: number; lift: number; stallN: number; allN: number;
  episodes: number | Array<number | string>; withheld: boolean | string;
}
/** B12r: one user who started the same operation again while an earlier one was still running. `sameSize` is false when the requests differed in size. */
export interface PatternRepeat { episode: number; userId: number; operation: string; repeats: number; sameSize: boolean | number; longestChain: number }
/** B6g: audit create time by the quiet gap before each request. `toS` is null or 0 for the open-ended band. Rates are fractions from 0 to 1. */
export interface PatternIdleBand { fromS: number; toS: number | null; n: number; medianMs: number | null; p95Ms: number | null; slowRate: number | null }
export interface PatternIdleLift { gS: number; value: number | null; withheld: boolean | string }
export interface PatternIdleGaps { bands: PatternIdleBand[]; lift: PatternIdleLift[]; excludedHangfire: number; partial: boolean | string; /** Why the gap read stopped early. Absent when it ran to the end. */ partialReason?: string }
/** B9: one operation's own two-part fit. `date` is a local calendar day (YYYY-MM-DD). Quantities are absent when no fit was made. */
export interface StepOperation {
  operation: string; volume: number; daysUsed: number; found: boolean; date?: string;
  beforeMs?: number; afterMs?: number; ratio?: number; trafficRatio?: number; reason?: string;
}
/** A suggested before and after split. It is only a suggestion: the page applies it when the analyst confirms. */
export interface SuggestedSplit { date: string; zone: string; baselineStart: string; baselineEnd: string; start: string; end: string }
export interface StepChange {
  zone: string; start: string; end: string; daysRead: number; daysLeftOut: number; operations: StepOperation[];
  date?: string; sharing?: string[]; split?: SuggestedSplit; partial: boolean;
}
/** One local day of the audit-create series. Quantities are null for a day with too few rows or a part day. */
export interface AuditSeriesDay { date: string; n: number; p50Ms: number | null; p95Ms: number | null; insertShare: number | null; partDay?: boolean }
export interface StallBand { fromMs: number; toMs: number; count: number; days: string[]; ofLong: number }
export interface AuditSeries {
  zone: string; days: AuditSeriesDay[]; band: StallBand | null; excludedHangfire: number; withoutAudit: number; partial: boolean; partialReason?: string;
}
/** GET /api/endpoint-daily. One entry for each whole local day. `low` is a day with fewer than 20 timed requests, `partial` a day cut by the window or the row cap. */
export interface EndpointDailyDay { date: string; start: string; end: string; count: number; p50Ms: number | null; p95Ms: number | null; low?: boolean; partial?: boolean }
export interface EndpointDaily { operation: string; zone: string; days: EndpointDailyDay[]; provenance?: Provenance[]; warnings: string[] }
export interface PatternCoverage { run: boolean; requestInitiatedAtPct?: number; accountIdPct?: number; note: string }
export interface PatternReport {
  params: PatternParams; stalls: PatternStall[]; episodes: PatternEpisode[]; volleys: PatternVolley[]; auditStalls: PatternAuditStall[];
  recurrence: 'none' | 'single_episode' | 'two_episodes' | 'recurring';
  findings: PatternFinding[]; quality: { incomplete: number; pending: number; invalid: number };
  coverage?: PatternCoverage; truncated: boolean; provenance?: Provenance[]; warnings: string[];
  /** The stall read (B1, B2, B12) hit the row cap. Additive: absent from older servers, which send only `truncated`. */
  stallsTruncated?: boolean;
  /** The audit-create read (B6) hit the row cap. `truncated` is the OR of the two. */
  auditsTruncated?: boolean;
  /** Additive rules from the incident-gap addendum. Each is absent from older servers and when the rule did not run. */
  onsetMinutes?: PatternOnsetMinute[]; concentration?: PatternConcentration[]; repeats?: PatternRepeat[]; idleGaps?: PatternIdleGaps;
  /** B9 and the audit-create series. Each is absent when it was not asked for or did not run. */
  stepChange?: StepChange; auditSeries?: AuditSeries;
}

/** The hypothesis ledger (/api/ledger). Mirrors internal/ledger/types.go. It holds no row data and no identifiers. */
export type HypothesisState = 'open' | 'supported_by_correlation' | 'supported_by_strong_correlation' | 'contradicted' | 'not_testable';
export interface HypothesisAlternative { text: string; status: 'open' | 'ruled_out' | 'not_testable' }
export interface HypothesisFacts {
  presentK: number; totalN: number; counterexamples: string[]; alternatives: HypothesisAlternative[];
  joinAmbiguousPct?: number; timezoneRecorded: boolean; confounderOverlap: boolean; coverageGap: boolean; overlapOnly: boolean;
}
export interface HypothesisInput { text: string; state: HypothesisState; evidenceIds: string[]; notes: string; settlingObservation: string; facts: HypothesisFacts }
export interface Hypothesis {
  id: string; text: string; state: HypothesisState; evidenceIds: string[]; notes: string;
  /** One observation, written by the analyst, that would settle the theory either way. Up to 500 characters. */
  settlingObservation: string; facts: HypothesisFacts;
  /** Reasons "strong correlation" is blocked right now; empty when it would be allowed. */
  guards: string[]; createdAt: string; updatedAt: string; history: { state: HypothesisState; at: string; note: string }[];
}

// P13 evidence export. Counts below 20 arrive masked as "<20".
export interface EvidenceCount { value?: number; masked: boolean; text: string }
export interface EvidenceRecord {
  version: number; evidenceId: string; source: string; windowStart: string; windowEnd: string; joinClass: string; tolerance: string;
  provenance: { kind: string; ref: string; hash: string }; label: string; rule: string; headline: string; n: EvidenceCount; lowN: boolean;
  knownGaps: string[];
  sourceQuality: { sourceVersion?: string; threshold?: string; coverageFirst?: string; coverageLast?: string; sampling?: string; timezoneRule?: string };
}
/** One imported file in a case export (version 2). The file name is never in it; the ref ties it to the manifest. */
export interface ExportImport {
  ref: string; importer: string; host?: string; tzRule?: string; rows: EvidenceCount; rejected: EvidenceCount; sourceQuality: EvidenceRecord['sourceQuality'];
}
export interface ExportEvent {
  evidenceId: string; source: string; kind: string; host?: string; start: string; end: string; durationMs?: number; value?: number; unit?: string; severity?: string; line: number; tzRule: string; attrs?: Record<string, string>;
}
export interface ManifestImport { ref: string; importer: string; sha256: string; acknowledgement: string; knownGaps: string[] }
export interface EvidenceExport {
  manifest: { schemaVersion: number; toolVersion: string; createdAt: string; bodySha256: string; masking: { statements: string[] }; /** Case export only. */ imports?: ManifestImport[] };
  body: {
    records: EvidenceRecord[]; pseudonyms?: { label: string; requests: EvidenceCount }[];
    ledger?: { text: string; state: HypothesisState; evidenceIds: string[] }[];
    /** Case export only (version 2). */
    imports?: ExportImport[]; events?: ExportEvent[];
  };
}
export interface ExportPreviewInput { sourceIds: string[]; start: string; end: string; includePseudonyms: boolean; includeLedger: boolean }
export interface ExportPreview { previewId: string; export: EvidenceExport }
export interface ExportFile { filename: string; content: string; sha256: string; /** Case export only: the events as a spreadsheet-safe CSV, with its own hash. */ eventsCsvFilename?: string; eventsCsv?: string; eventsCsvSha256?: string }
