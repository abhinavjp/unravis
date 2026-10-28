import { casePath, json, send } from '../cases';
import { normalizeAnswer, normalizeItem, normalizeReport, normalizeView } from './normalize';
import type {
  ChecklistInput, ChecklistItem, InvestigationAnswer, InvestigationRequest, ReportAnswer, ReportInclude, SavedView, SavedViewInput
} from './types';

/**
 * The guided analysis routes, all under /api/cases/{id}. They go through the same `send` as every other case route, so a locked master key shows
 * the unlock screen and a refusal comes back as a CaseApiError with the server's reason (`case-closed`, `plan-full`, `plan-invalid`, ...).
 * Every one of them only reads audit data. The report holds no payloads, no user ids, no connection details and no passwords.
 */

export async function runInvestigation(caseId: string, request: InvestigationRequest, signal?: AbortSignal): Promise<InvestigationAnswer> {
  return normalizeAnswer(await json<unknown>(await send(casePath(caseId, '/investigation'), { method: 'POST', body: JSON.stringify(request) }, signal)));
}

export async function listViews(caseId: string, signal?: AbortSignal): Promise<SavedView[]> {
  const body = await json<{ views?: unknown }>(await send(casePath(caseId, '/views'), {}, signal));
  return Array.isArray(body.views) ? body.views.map(normalizeView) : [];
}

export async function saveView(caseId: string, view: SavedViewInput, signal?: AbortSignal): Promise<SavedView> {
  return normalizeView(await json<unknown>(await send(casePath(caseId, '/views'), { method: 'POST', body: JSON.stringify(view) }, signal)));
}

export async function deleteView(caseId: string, viewId: string, signal?: AbortSignal): Promise<void> {
  await send(casePath(caseId, '/views/' + encodeURIComponent(viewId)), { method: 'DELETE' }, signal);
}

export async function listChecklist(caseId: string, signal?: AbortSignal): Promise<ChecklistItem[]> {
  const body = await json<{ items?: unknown }>(await send(casePath(caseId, '/checklist'), {}, signal));
  return Array.isArray(body.items) ? body.items.map(normalizeItem) : [];
}

export async function addChecklistItem(caseId: string, item: ChecklistInput, signal?: AbortSignal): Promise<ChecklistItem> {
  return normalizeItem(await json<unknown>(await send(casePath(caseId, '/checklist'), { method: 'POST', body: JSON.stringify(item) }, signal)));
}

export async function updateChecklistItem(caseId: string, itemId: string, item: ChecklistInput, signal?: AbortSignal): Promise<ChecklistItem> {
  return normalizeItem(await json<unknown>(await send(casePath(caseId, '/checklist/' + encodeURIComponent(itemId)), { method: 'PUT', body: JSON.stringify(item) }, signal)));
}

export async function deleteChecklistItem(caseId: string, itemId: string, signal?: AbortSignal): Promise<void> {
  await send(casePath(caseId, '/checklist/' + encodeURIComponent(itemId)), { method: 'DELETE' }, signal);
}

/** Builds the report on the server. `request` is optional: without it the report says nothing was analysed. */
export async function buildReport(
  caseId: string, format: 'html' | 'csv', include: ReportInclude, request?: InvestigationRequest, signal?: AbortSignal
): Promise<ReportAnswer> {
  const body = { format, include, ...(request ? { request } : {}) };
  return normalizeReport(await json<unknown>(await send(casePath(caseId, '/report'), { method: 'POST', body: JSON.stringify(body) }, signal)));
}
