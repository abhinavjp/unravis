import { casePath, json, send } from './cases';
import type { SqlTestRequest } from './types';

/**
 * Health checks for a case (GET/POST /api/cases/{id}/health[/run]). Each run is a fixed set of read-only checks the server writes itself;
 * nothing typed here is ever run as a query. A run only starts when the person presses "Run checks". The report holds no host, user or password.
 */

export type HealthStatus = 'ok' | 'note' | 'warn' | 'bad' | 'unavailable';

export interface HealthFinding {
  id: string; title: string; status: HealthStatus; summary: string; why: string; columns: string[]; rows: string[][];
}
export interface HealthReport {
  kind: 'sql' | 'mongo'; label: string; database: string; version: string; startedAt: string; finishedAt: string; overall: HealthStatus; findings: HealthFinding[];
}
export interface HealthRun { id: number; at: string; kind: 'sql' | 'mongo'; label: string; status: HealthStatus; report: HealthReport | null }
export interface HealthRunResult { saved: boolean; saveError: string; run: HealthRun }

/** What to check: a kept SQL login (with a password typed for this run if none is kept), or a kept MongoDB connection. */
export type HealthTarget =
  | { kind: 'sql'; sql: SqlTestRequest }
  | { kind: 'mongo'; mongo: { savedConnectionId: string; uri: string; username: string; password: string; authSource: string; tlsMode: string; caCertificate: string; database: string; collection: string } };

const STATUSES: readonly HealthStatus[] = ['ok', 'note', 'warn', 'bad', 'unavailable'];
const str = (v: unknown): string => (typeof v === 'string' ? v : '');
const strs = (v: unknown): string[] => (Array.isArray(v) ? v.map((x) => str(x)) : []);
const status = (v: unknown): HealthStatus => (STATUSES.includes(v as HealthStatus) ? (v as HealthStatus) : 'unavailable');

export function toReport(raw: unknown): HealthReport | null {
  if (!raw || typeof raw !== 'object') return null;
  const r = raw as Record<string, unknown>;
  const findings = Array.isArray(r.findings) ? r.findings.filter((f): f is Record<string, unknown> => !!f && typeof f === 'object').map((f) => ({
    id: str(f.id), title: str(f.title), status: status(f.status), summary: str(f.summary), why: str(f.why), columns: strs(f.columns),
    rows: Array.isArray(f.rows) ? f.rows.map((row) => strs(row)) : []
  })) : [];
  return {
    kind: r.kind === 'mongo' ? 'mongo' : 'sql', label: str(r.label), database: str(r.database), version: str(r.version),
    startedAt: str(r.startedAt), finishedAt: str(r.finishedAt), overall: status(r.overall), findings
  };
}

export function toRun(raw: unknown): HealthRun {
  const r = (raw && typeof raw === 'object' ? raw : {}) as Record<string, unknown>;
  return { id: typeof r.id === 'number' ? r.id : 0, at: str(r.at), kind: r.kind === 'mongo' ? 'mongo' : 'sql', label: str(r.label), status: status(r.status), report: toReport(r.report) };
}

export async function fetchHealthRuns(caseId: string, signal?: AbortSignal): Promise<HealthRun[]> {
  const body = await json<{ runs?: unknown }>(await send(casePath(caseId, '/health'), {}, signal));
  return Array.isArray(body.runs) ? body.runs.map(toRun) : [];
}

export async function runHealth(caseId: string, target: HealthTarget, signal?: AbortSignal): Promise<HealthRunResult> {
  const payload = target.kind === 'sql' ? { kind: 'sql', sql: target.sql } : { kind: 'mongo', mongo: target.mongo };
  const body = await json<{ saved?: unknown; saveError?: unknown; run?: unknown }>(await send(casePath(caseId, '/health/run'), { method: 'POST', body: JSON.stringify(payload) }, signal));
  return { saved: body.saved === true, saveError: str(body.saveError), run: toRun(body.run) };
}

/** Findings that need a look first: bad, then warn, then note, then ok, then the ones that could not run. Ties keep the server's order. */
const ORDER: Record<HealthStatus, number> = { bad: 0, warn: 1, note: 2, ok: 3, unavailable: 4 };
export const sortedFindings = (report: HealthReport): HealthFinding[] =>
  report.findings.map((f, i) => ({ f, i })).sort((a, b) => ORDER[a.f.status] - ORDER[b.f.status] || a.i - b.i).map((x) => x.f);

export const countByStatus = (report: HealthReport): Record<HealthStatus, number> => {
  const out: Record<HealthStatus, number> = { ok: 0, note: 0, warn: 0, bad: 0, unavailable: 0 };
  for (const f of report.findings) out[f.status]++;
  return out;
};
