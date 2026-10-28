import { casePath, json, send } from './cases';
import { postSourceTag } from './caseTagsApi';
import type { CaseTag, CaseUseApi } from './blackboxCase';

/** The real calls: GET and POST /api/cases/{id}/sources and DELETE /api/cases/{id}/sources/{tagId}. */
export const caseUseApi: CaseUseApi = {
  async listTags(caseId) {
    const body = await json<{ sources?: unknown }>(await send(casePath(caseId, '/sources'), {}));
    const list = Array.isArray(body.sources) ? body.sources : [];
    return list.flatMap((raw): CaseTag[] => {
      const r = raw as Partial<CaseTag>;
      return typeof r.id === 'number' ? [{ id: r.id, kind: String(r.kind ?? ''), label: String(r.label ?? '') }] : [];
    });
  },
  async postTag(caseId, tag) {
    const stored = await postSourceTag(caseId, tag);
    return { id: stored.id, kind: stored.kind, label: stored.label };
  },
  async removeTag(caseId, tagId) {
    await send(casePath(caseId, '/sources/' + encodeURIComponent(String(tagId))), { method: 'DELETE' });
  }
};
