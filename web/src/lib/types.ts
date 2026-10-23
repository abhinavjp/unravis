export type Kind = 'synchronous' | 'asynchronous' | 'hangfire' | 'all';
export type Outcome = 'success' | 'failure' | 'all';
export type Metric = 'total' | 'api';

export interface Source { selectionKey?: string; connectionId?: string; connectionLabel?: string; id: string; label: string; database: string; collection: string }
export type TLSMode = '' | 'uri' | 'system' | 'custom' | 'insecure' | 'off';
export interface ConnectionInput { uri: string; username: string; password: string; authSource: string; tlsMode: TLSMode; caCertificate: string; savedConnectionId?: string }
export interface ConnectionTestResult { databases: string[]; warning?: string }
export interface SourceInput extends ConnectionInput { connectionLabel?: string; label: string; database: string; collection: string }
export interface RecentConnection {
  id: string; label: string; uri: string; username: string; authSource: string; tlsMode: TLSMode;
  caCertificate: string; collection: string; hasCredentials: boolean; lastUsedAt: string;
}
export interface RememberConnectionInput { label: string; connection: ConnectionInput; collection: string; rememberPassword: boolean }
export interface SourceResult { sourceId: string; ok: boolean; error: string; version: string; indexWarning: string }
export interface Info {
  demo: boolean;
  limits: { maxSources: number; maxPageSize: number; maxSummaryRows: number; maxTimeBuckets: number };
  now: string;
}
export interface Query {
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
}
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
}
export interface Point { at: string; count: number | null; p50: Quantile | null; p95: Quantile | null; p95Ratio: number | null }
export interface Series { key: string; points: Point[] }
export interface Quality { incomplete: number; invalid: number; unknownOutcome: number }
export interface Analysis {
  loadWindows?: LoadWindow[];
  requestVolume?: VolumeBucket[];
  query: Query; apis: Comparison[]; timeline: Series[]; sources: SourceResult[];
  quality: Quality; approximate: boolean; warnings: string[];
  timings?: SourceTiming[];
}
export interface SourceTiming { sourceId: string; window: 'current' | 'reference'; readMs: number; rows: number }
export interface FieldPresence { name: string; present: number; types: string[] }
export interface SchemaCensus { sampled: number; fields: FieldPresence[] }
export interface RequestRecord {
  id: string; sourceId: string; controller: string; action: string; kind: string; outcome: string;
  status: string; url: string; initiatedAt: string | null; completedAt: string | null;
  durationMs: number | null; apiTimeMs: number | null; auditCreateTimeMs: number | null;
  payloadRecordTimeMs: number | null; requestKB: number; responseKB: number;
}
export interface Page { rows: RequestRecord[]; nextCursor: string; sources: SourceResult[]; truncated: boolean }
export interface Payload { request: string; response: string; truncated: boolean }
export interface ActivityInput { sourceIds: string[]; start: string; end: string; cursor: string; limit: number }
export interface VolumeBucket { controller: string; action: string; at: string; count: number }
export interface LoadWindow { at:string; count:number; timing:Stats }
