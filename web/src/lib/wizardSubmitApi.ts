import { api } from './api';
import { CaseApiError, createCase, importProgressPercent, listCases, startCommitJob, startPreviewJob, waitForImportJob } from './cases';
import type { ImportJob } from './cases';
import { discoverClient, outcomeFromError, outcomeFromJob, watchJob } from './discover';
import type { RowOutcome } from './discover';
import { putEnvironments, postSourceTag } from './caseTagsApi';
import { ackTextFor } from './copy/importCopy';
import { heldCollection, heldConnection } from './wizardConnections';
import type { SubmitApi } from './wizardSubmit';

/**
 * The real calls behind `submitWizard` (TSK-19). Every one goes through an existing client, so the token header, the 423 lock signal and
 * the job polling contract are the same as everywhere else. This file keeps nothing: a Mongo connection is looked up in the in-memory hold
 * for the moment of one call and never copied anywhere.
 */

const percentOf = (job: ImportJob): number | null => importProgressPercent(job);

export const wizardSubmitApi: SubmitApi = {
  createCase: async (title) => ({ id: (await createCase(title)).id }),
  async findRecentCase(title, sinceMs) {
    const found = (await listCases())
      .filter((c) => c.title.trim() === title.trim() && Date.parse(c.created) >= sinceMs)
      .sort((a, b) => Date.parse(b.created) - Date.parse(a.created))[0];
    return found ? { id: found.id } : null;
  },
  putEnvironments: (caseId, envs) => putEnvironments(caseId, envs),
  postSourceTag: (caseId, tag) => postSourceTag(caseId, tag),

  async testConnection(connectionId) {
    const held = heldConnection(connectionId);
    // The hold is in memory only. If the page was reloaded it is gone, and the person must test the connection again.
    if (!held) throw new CaseApiError('That connection is no longer held.', 422, 'connection-lost');
    return api.testConnection(held);
  },

  async addAuditSource(connectionId, database) {
    const held = heldConnection(connectionId);
    if (!held) throw new CaseApiError('That connection is no longer held.', 422, 'connection-lost');
    return api.addSource({ ...held, label: database, database, collection: heldCollection(connectionId) });
  },

  confirmHost: (host) => discoverClient.confirmHost?.(host) ?? Promise.resolve(),
  startImports: (caseId, request) => discoverClient.startImports(caseId, request),
  watchJob: (caseId, ref, onProgress) => watchJob(discoverClient, caseId, ref, { onUpdate: (watch) => { if (watch.job) onProgress(percentOf(watch.job)); } }),

  /** Detects the type with a preview that keeps nothing, then keeps the file with the notice text. A tie or an unknown type is a refusal for this row. */
  async importFile(caseId, file, onProgress): Promise<RowOutcome> {
    try {
      const first = await startPreviewJob({ caseId, file });
      const preview = await waitForImportJob(caseId, first, { onUpdate: (job) => onProgress(percentOf(job)) });
      if (!preview.importer) return { kind: 'refused', reason: preview.tied.length > 1 ? 'needs-type' : 'unknown-type' };
      const started = await startCommitJob({ caseId, file, importer: preview.importer, ackText: ackTextFor() });
      let last: ImportJob = started;
      await waitForImportJob(caseId, started, { onUpdate: (job) => { last = job; onProgress(percentOf(job)); } });
      return outcomeFromJob(last) ?? { kind: 'failed', reason: 'unreachable' };
    } catch (error) {
      if (error instanceof DOMException && error.name === 'AbortError') throw error;
      if (error instanceof CaseApiError && error.status === 423) throw error;
      return outcomeFromError(error);
    }
  }
};
