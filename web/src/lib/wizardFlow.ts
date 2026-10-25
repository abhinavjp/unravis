/**
 * The steps of the create wizard and the rules for moving between them. Pure: no DOM, no fetch, no state of its own.
 * wizard.svelte.ts holds the draft; this file answers what the draft allows. The three steps follow layout B (Workspace):
 * Name, Sources, Review. A person may always go back; going forward needs the name to be valid. The Sources step may be skipped.
 */

export type StepId = 'name' | 'sources' | 'review';
export const STEPS: readonly StepId[] = ['name', 'sources', 'review'];

export const TITLE_MIN = 2;
export const TITLE_MAX = 80;

/**
 * One source the person ticked or connected, with its tags (environment > portal > API). Blank tags mean "not named yet".
 * The sources step (TSK-18) fills these in; the review step (TSK-19) reads them. `ref` identifies the finding or connection.
 * The draft never holds a password or a connection string.
 */
export interface DraftSource {
  kind: string;
  ref: string;
  label: string;
  env: string;
  portal: string;
  api: string;
  /** Only a Mongo database uses it: 'audit' | 'other' | 'unused' | ''. */
  classification: string;
}

export const stepIndex = (step: StepId): number => STEPS.indexOf(step);

export type TitleProblem = 'empty' | 'too-short' | 'too-long' | 'bad-characters';

/** The title as it will be sent: line breaks and runs of spaces become one space, the ends are trimmed. */
export const cleanTitle = (text: string): string => String(text ?? '').replace(/\s+/g, ' ').trim();

const REFUSED = /[\p{Cc}\p{Cf}\p{Cs}\p{Zl}\p{Zp}]/u;

/** What is wrong with the case name, or null when it can be used. Counts characters as a person does (an emoji is one). */
export function titleProblem(text: string): TitleProblem | null {
  const title = cleanTitle(text);
  if (!title) return 'empty';
  if (REFUSED.test(title)) return 'bad-characters';
  const length = [...title].length;
  if (length < TITLE_MIN) return 'too-short';
  if (length > TITLE_MAX) return 'too-long';
  return null;
}

export const titleLength = (text: string): number => [...cleanTitle(text)].length;

/** What the step rules need to know about the draft. */
export interface StepFacts { title: string }

/** True when the person may be on this step. Name is always open; Sources and Review need a valid name. */
export function canEnter(step: StepId, facts: StepFacts): boolean {
  if (step === 'name') return true;
  return titleProblem(facts.title) === null;
}

export type StepMove = { ok: true; step: StepId } | { ok: false; step: StepId; reason: string };

/** The step after a request to move. Back is always allowed; forward needs every step on the way to be open. A refused move keeps the step. */
export function moveToStep(from: StepId, to: StepId, facts: StepFacts): StepMove {
  if (!STEPS.includes(to)) return { ok: false, step: from, reason: 'There is no such step.' };
  if (stepIndex(to) <= stepIndex(from)) return { ok: true, step: to };
  if (!canEnter(to, facts)) return { ok: false, step: from, reason: 'The name must be valid before going on.' };
  return { ok: true, step: to };
}

export const nextStepOf = (step: StepId): StepId => STEPS[Math.min(stepIndex(step) + 1, STEPS.length - 1)];
export const backStepOf = (step: StepId): StepId => STEPS[Math.max(stepIndex(step) - 1, 0)];

/** The step to show when a draft is picked up again: where the person was, unless the name no longer allows it. */
export function resumeStep(step: StepId, facts: StepFacts): StepId {
  return STEPS.includes(step) && canEnter(step, facts) ? step : 'name';
}

/** Whether leaving would throw something away. A draft with nothing typed or chosen is not worth a question. */
export function isDirty(draft: { title: string; envText: string; sources: readonly DraftSource[] }): boolean {
  return cleanTitle(draft.title) !== '' || draft.envText.replace(/[,\s]/g, '') !== '' || draft.sources.length > 0;
}

/** One step's state for the progress strip. */
export type StepState = 'current' | 'done' | 'open' | 'locked';

export interface StepView { id: StepId; number: number; state: StepState; enabled: boolean }

/** The progress strip: each step with its number and whether it can be chosen now. A step before the current one is done. */
export function stepViews(current: StepId, facts: StepFacts): StepView[] {
  const at = stepIndex(current);
  return STEPS.map((id, i) => {
    const enabled = canEnter(id, facts);
    const state: StepState = i === at ? 'current' : i < at ? 'done' : enabled ? 'open' : 'locked';
    return { id, number: i + 1, state, enabled };
  });
}
