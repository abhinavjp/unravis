import { casePath, json, send } from './cases';

/**
 * The two routes that write a case's environment and source tags (TSK-07):
 *   PUT  /api/cases/{id}/environments   { environments: string[] }  -> { environments }
 *   POST /api/cases/{id}/sources        { importId, kind, label, env, portal, api, classification }  -> the stored tag
 * They go through the same `send` as every other case route, so a 423 (the master key is locked) shows the unlock screen, and a refusal
 * comes back as a CaseApiError with the server's reason (`bad-tag`, `tag-cap-exceeded`, `case-closed`, `database-not-tested`, ...).
 * A tag holds labels only. It never holds a connection string, a password, a path or a file's content.
 */

export interface SourceTagInput {
  /** 0: the tag is not tied to one import (the wizard tags a source before or without an import). */
  importId?: number;
  kind: string;
  label: string;
  env: string;
  portal: string;
  api: string;
  /** Only a Mongo database uses it: 'audit' | 'other' | 'unused'. */
  classification: string;
}

export interface SourceTag extends Required<SourceTagInput> { id: number }

/** Sets the case's environments (replacing the list). Resolves with what the server kept: trimmed, de-duplicated, in order. */
export async function putEnvironments(caseId: string, environments: readonly string[], signal?: AbortSignal): Promise<string[]> {
  const body = await json<{ environments?: unknown }>(await send(casePath(caseId, '/environments'), { method: 'PUT', body: JSON.stringify({ environments }) }, signal));
  return Array.isArray(body.environments) ? body.environments.filter((name): name is string => typeof name === 'string') : [];
}

/** Adds one source tag. A Mongo database needs a successful connection test in the last 30 minutes, or the server answers `database-not-tested`. */
export async function postSourceTag(caseId: string, tag: SourceTagInput, signal?: AbortSignal): Promise<SourceTag> {
  const payload = { importId: tag.importId ?? 0, kind: tag.kind, label: tag.label, env: tag.env, portal: tag.portal, api: tag.api, classification: tag.classification };
  const raw = await json<Partial<SourceTag>>(await send(casePath(caseId, '/sources'), { method: 'POST', body: JSON.stringify(payload) }, signal));
  return {
    id: typeof raw.id === 'number' ? raw.id : 0, importId: typeof raw.importId === 'number' ? raw.importId : 0, kind: String(raw.kind ?? ''), label: String(raw.label ?? ''),
    env: String(raw.env ?? ''), portal: String(raw.portal ?? ''), api: String(raw.api ?? ''), classification: String(raw.classification ?? '')
  };
}
