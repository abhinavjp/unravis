import type { ChecklistItem, ChecklistList, InvestigationAnswer, ReportAnswer, Result, SavedView } from './types';

/**
 * The server's answers, read defensively. A field the engine leaves out becomes an empty list or a missing value, never a crash, so a damaged or
 * older answer shows as "Cannot test yet" on the page instead of a blank screen. Nothing here invents a number.
 */

export const CHECKLIST_LISTS: readonly ChecklistList[] = ['todo', 'doing', 'learned', 'ruled_out'];

const obj = (value: unknown): Record<string, unknown> => (value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {});
const list = <T>(value: unknown): T[] => (Array.isArray(value) ? value as T[] : []);
const text = (value: unknown): string => (typeof value === 'string' ? value : '');
const num = (value: unknown): number => (typeof value === 'number' && Number.isFinite(value) ? value : 0);

const READINESS = new Set(['Ready', 'Partly ready', 'Cannot test yet', 'Failed']);

export function normalizeResult(raw: unknown): Result {
  const r = obj(raw);
  const facts = obj(r.facts);
  const parts = obj(facts.parts);
  const signals = obj(r.signals);
  const origin = obj(r.origin);
  const confidence = obj(r.confidence);
  const steps = list<Record<string, unknown>>(r.steps).map((s) => {
    const step = obj(s);
    return {
      number: num(step.number), name: text(step.name),
      // A word the engine does not use is shown as Cannot test yet: an unknown word is never a pass.
      readiness: (READINESS.has(text(step.readiness)) ? text(step.readiness) : 'Cannot test yet') as Result['steps'][number]['readiness'],
      reasons: list<string>(step.reasons), untestedSpans: list(step.untestedSpans) as Result['steps'][number]['untestedSpans'],
      nutshell: text(step.nutshell), findings: list<string>(step.findings)
    };
  });
  return {
    ...(r as unknown as Result),
    steps,
    suspects: list<Result['suspects'][number]>(r.suspects).map((s) => ({ ...s, evidence: list(obj(s).evidence) })),
    notApplicable: list(r.notApplicable),
    thresholds: { changeMinPercent: 30, changeMinMs: 100, ...obj(r.thresholds) } as Result['thresholds'],
    headline: text(r.headline),
    facts: {
      ...(facts as unknown as Result['facts']),
      routes: list(facts.routes),
      gaps: list(facts.gaps),
      tiers: list(facts.tiers),
      series: list(facts.series),
      parts: { ...(parts as unknown as Result['facts']['parts']), items: list(parts.items) }
    },
    signals: { ...(signals as unknown as Result['signals']), flags: obj(signals.flags) as Result['signals']['flags'] },
    origin: { ...(origin as unknown as Result['origin']), roles: list(origin.roles), status: text(origin.status) || 'not enough data' },
    confidence: {
      ...(confidence as unknown as Result['confidence']),
      sourcesUsed: list(confidence.sourcesUsed), sourcesMissing: list(confidence.sourcesMissing), notTested: list(confidence.notTested)
    }
  };
}

export function normalizeAnswer(raw: unknown): InvestigationAnswer {
  const body = obj(raw);
  const read = obj(body.read);
  return {
    result: normalizeResult(body.result),
    read: { requests: num(read.requests), iisRows: num(read.iisRows), truncated: read.truncated === true, warnings: list<string>(read.warnings).filter((w) => typeof w === 'string') }
  };
}

export function normalizeView(raw: unknown): SavedView {
  const v = obj(raw);
  return { id: text(v.id), name: text(v.name), tool: text(v.tool), saved: text(v.saved), settings: obj(v.settings) };
}

export function normalizeItem(raw: unknown): ChecklistItem {
  const v = obj(raw);
  const listName = text(v.list);
  return {
    id: text(v.id), list: (CHECKLIST_LISTS as readonly string[]).includes(listName) ? listName as ChecklistList : 'todo', text: text(v.text),
    suspect: text(v.suspect) || undefined, order: num(v.order), created: text(v.created), updated: text(v.updated)
  };
}

export function normalizeReport(raw: unknown): ReportAnswer {
  const v = obj(raw);
  return { format: v.format === 'csv' ? 'csv' : 'html', filename: text(v.filename), mime: text(v.mime), sha256: text(v.sha256), bytes: num(v.bytes), content: text(v.content) };
}
