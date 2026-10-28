import { api } from './api';
import { CaseApiError, createCase, importProgressPercent, putRetries, startCommitJob, startPreviewJob, waitForImportJob } from './cases';
import type { ImportJob } from './cases';
import { discoverClient, outcomeFromError, outcomeFromJob, watchJob } from './discover';
import type { RowOutcome } from './discover';
import { putEnvironments, postSourceTag } from './caseTagsApi';
import { ackTextFor } from './copy/importCopy';
import { heldCollection, heldConnection } from './wizardConnections';
import type { SubmitApi } from './wizardSubmit';
import { registerSourceSaved, resolveSavedRecent, suggestedConnectionLabel } from './sourceSaved';

/**
 * The real calls behind `submitWizard` (TSK-19). Every one goes through an existing client, so the token header, the 423 lock signal and
 * the job polling contract are the same as everywhere else. This file keeps nothing: a Mongo connection is looked up in the in-memory hold
 * for the moment of one call and never copied anywhere.
 */

const percentOf = (job: ImportJob): number | null => importProgressPercent(job);

/**
 * Names the saved connection a new source came from, so the case can reopen it later. The saved record that is already there is used as it
 * is: saving the details again would REPLACE it and wipe its stored password. One is made (without a password) only when the list, read
 * again right now, has none for this address and collection. Quiet: if the list cannot be read or nothing could be saved, the case only
 * cannot reconnect this source by itself.
 */
async function rememberForCase(held: Parameters<typeof api.rememberConnection>[0]['connection'], collection: string, source: { id: string; connectionId?: string }): Promise<void> {
  try {
    const { recent } = await resolveSavedRecent({
      details: { uri: held.uri, username: held.username, collection, authSource: held.authSource, tlsMode: held.tlsMode, caCertificate: held.caCertificate },
      savedId: held.savedConnectionId,
      recents: [],
      refresh: async () => {
        const list = await api.recentConnections();
        return Array.isArray(list) ? list.filter((item) => !!item && typeof item.id === 'string' && typeof item.uri === 'string') : null;
      },
      create: async () => {
        const made = await api.rememberConnection({ label: suggestedConnectionLabel(held.uri), connection: { ...held, savedConnectionId: undefined }, collection, rememberPassword: false });
        return made && typeof made.id === 'string' ? made : null;
      }
    });
    if (recent) registerSourceSaved(source, recent.id);
  } catch { /* nothing to do */ }
}

export const wizardSubmitApi: SubmitApi = {
  createCase: async (title, requestId, caseKey) => ({ id: (await createCase(title, requestId, undefined, caseKey ?? undefined)).id }),
  saveRetries: (caseId, items) => putRetries(caseId, items),
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
    const collection = heldCollection(connectionId);
    const source = await api.addSource({ ...held, label: database, database, collection });
    await rememberForCase(held, collection, source);
    return source;
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
