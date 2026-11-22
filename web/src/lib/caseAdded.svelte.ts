import type { SubmitState } from './wizardSubmit';

/**
 * "Something was added to this case." Every add that succeeds on a case that exists (files through the Add files pop-up, and the Add data
 * run for Mongo, SQL, Redis, Blackbox and what was found on this PC) calls `noteCaseAdded` once. The Case home page watches `seq` and reads
 * the case story again, quietly, once per call. A failed add never calls it, so nothing is read for it.
 */
export const caseAdded = $state({ caseId: '', seq: 0 });

export function noteCaseAdded(caseId: string): void {
  if (!caseId) return;
  caseAdded.caseId = caseId;
  caseAdded.seq += 1;
}

/** How many steps of a run went through (a source tagged, an audit database made a source, a file or finding imported). It only goes up while a case keeps its run. */
export function landedCount(state: SubmitState): number {
  const done = (steps: Record<string, { status: string }>): number => Object.values(steps).filter((step) => step.status === 'done').length;
  return done(state.tags) + done(state.audit) + done(state.imports);
}
