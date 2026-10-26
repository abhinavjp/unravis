import type { CaseInfo, ImportRecord } from './cases';
import { CASE_HOME_TEXT, fileDetail, theoryChangedDetail } from './copy/caseHomeCopy';
import { overallText } from './copy/healthCopy';
import { importerLabel } from './copy/importCopy';
import { countByStatus, type HealthRun, type HealthStatus } from './health';
import { stateLabel } from './ledger';
import type { Hypothesis } from './types';

/**
 * The story of one case: a single list in the order things happened, built only from facts the case has already saved
 * (when it started, each file added, each health check run, each theory written or changed). Pure: nothing is fetched here.
 */
export type StoryKind = 'case' | 'file' | 'health' | 'theory';
export type StoryTone = 'plain' | 'ok' | 'warn' | 'bad';

export interface StoryEvent {
  /** Stable key for a list: the kind and the saved id. */
  id: string;
  /** The saved time, as the server wrote it. */
  at: string;
  kind: StoryKind;
  title: string;
  detail: string;
  tone: StoryTone;
}

export interface StoryInput {
  info: CaseInfo | null;
  imports: readonly ImportRecord[];
  health: readonly HealthRun[];
  theories: readonly Hypothesis[];
}

const TONE: Record<HealthStatus, StoryTone> = { ok: 'ok', note: 'plain', warn: 'warn', bad: 'bad', unavailable: 'plain' };
const KIND_ORDER: Record<StoryKind, number> = { case: 0, file: 1, health: 2, theory: 3 };

/** A theory's words, cut at a word edge so a long one stays one line in the list. */
export function shortText(text: string, max = 140): string {
  const flat = text.replace(/\s+/g, ' ').trim();
  if (flat.length <= max) return flat;
  const cut = flat.slice(0, max);
  const space = cut.lastIndexOf(' ');
  return (space > max / 2 ? cut.slice(0, space) : cut).trimEnd() + '…';
}

const time = (value: string): number => {
  const ms = Date.parse(value);
  return Number.isFinite(ms) ? ms : Number.POSITIVE_INFINITY;
};

export function buildStory(input: StoryInput): StoryEvent[] {
  const events: StoryEvent[] = [];
  if (input.info?.created) {
    events.push({ id: 'case:' + input.info.id, at: input.info.created, kind: 'case', title: CASE_HOME_TEXT.eventCase, detail: input.info.title ? '“' + shortText(input.info.title, 80) + '”' : '', tone: 'plain' });
  }
  for (const record of input.imports) {
    const kept = record.status === 'ok';
    events.push({
      id: 'file:' + record.id,
      at: record.imported,
      kind: 'file',
      title: kept ? CASE_HOME_TEXT.eventFile : CASE_HOME_TEXT.eventFileKept,
      detail: kept ? fileDetail(record.fileName, record.rows, importerLabel(record.importer), record.rejected) : (record.reason ?? ''),
      tone: kept ? 'plain' : 'warn'
    });
  }
  for (const run of input.health) {
    const mongo = run.kind === 'mongo';
    const detail = run.report ? [run.label, overallText(run.report.overall, countByStatus(run.report))].filter(Boolean).join(' · ') : run.label;
    events.push({ id: 'health:' + run.id, at: run.at, kind: 'health', title: mongo ? CASE_HOME_TEXT.eventHealthMongo : CASE_HOME_TEXT.eventHealthSql, detail, tone: TONE[run.status] });
  }
  for (const theory of input.theories) {
    events.push({ id: 'theory:' + theory.id, at: theory.createdAt, kind: 'theory', title: CASE_HOME_TEXT.eventTheoryAdded, detail: shortText(theory.text), tone: 'plain' });
    // A theory that was edited after it was written gets a second line, so the change shows where it happened.
    if (theory.updatedAt && time(theory.updatedAt) > time(theory.createdAt) && (theory.history?.length ?? 0) > 0) {
      events.push({ id: 'theory-change:' + theory.id, at: theory.updatedAt, kind: 'theory', title: CASE_HOME_TEXT.eventTheoryChanged, detail: shortText(theory.text, 90) + ' ' + theoryChangedDetail(stateLabel(theory.state)), tone: 'plain' });
    }
  }
  // Oldest first. Equal times keep a steady order (case, file, health check, theory); a time that cannot be read goes last.
  return events
    .map((event, index) => ({ event, index }))
    .sort((a, b) => time(a.event.at) - time(b.event.at) || KIND_ORDER[a.event.kind] - KIND_ORDER[b.event.kind] || a.index - b.index)
    .map((entry) => entry.event);
}
