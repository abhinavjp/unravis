import { releaseConnections } from './wizardConnections';
import { parseEnvs } from './wizardEnvs';
import type { ParsedEnvs } from './wizardEnvs';
import { newKeyProblem, type NewKeyProblem } from './caseKey';
import { backStepOf, cleanTitle, isDirty as draftIsDirty, moveToStep, nextStepOf, resumeStep, titleProblem } from './wizardFlow';
import type { DraftSource, StepId } from './wizardFlow';

/**
 * The draft of the create wizard: what the person has typed and chosen so far. It lives in memory only and nothing here touches the server
 * (the case is made at the last step, TSK-19). Going back and forward keeps every entry. It holds no password and no connection string.
 *
 * The sources step (TSK-18) reads and writes `wizard.sources` (or calls `setSources`); the review step (TSK-19) reads `draftSnapshot()`.
 */
export interface WizardDraft {
  step: StepId;
  /** The case name exactly as typed. Use `cleanTitle` / `draftSnapshot().title` for the value to send. */
  title: string;
  /** The environments field exactly as typed (commas or line breaks). Use `draftEnvs()` for the parsed list. */
  envText: string;
  /** Sources ticked or connected on the sources step, each with its environment / portal / API tags. */
  sources: DraftSource[];
  /** The optional key of this case (decision A12): off by default. The key and its repeat live here only until the case is made, then are cleared. */
  caseKey: { on: boolean; key: string; again: string };
}

const blank = (): WizardDraft => ({ step: 'name', title: '', envText: '', sources: [], caseKey: { on: false, key: '', again: '' } });

/** The one draft. Bind the fields with `bind:value={wizard.title}`; change the step with `goToStep`, `nextStep` and `backStep`. */
export const wizard = $state<WizardDraft>(blank());

const resetHooks: Array<() => void> = [];

/**
 * Registers something to clear when the draft is thrown away (the sources step's scan and connection state, TSK-18). A hook is added once;
 * it keeps this file free of imports from the steps that sit on top of it.
 */
export function onWizardReset(hook: () => void) {
  if (!resetHooks.includes(hook)) resetHooks.push(hook);
}

/** Throws the draft away (leave, or after the case was created). Held connections are forgotten too. */
export function resetWizard() {
  Object.assign(wizard, blank());
  releaseConnections();
  for (const hook of resetHooks) hook();
}

/** True when leaving would throw something away. */
export const wizardIsDirty = (): boolean => draftIsDirty(wizard);

/** The environments typed so far, parsed. */
export const draftEnvs = (): ParsedEnvs => parseEnvs(wizard.envText);

/** The first problem with the case key when one is asked for (none when it is off). */
export const draftKeyProblem = (): NewKeyProblem | null => (wizard.caseKey.on ? newKeyProblem(wizard.caseKey.key, wizard.caseKey.again) : null);

/** The key and its repeat for the one create call, or null when the case gets no key of its own. */
export const draftCaseKey = (): { key: string; confirm: string } | null => (wizard.caseKey.on ? { key: wizard.caseKey.key, confirm: wizard.caseKey.again } : null);

/** Clears the key and its repeat (the case was made, or the person turned the key off). */
export function wipeCaseKey(): void {
  wizard.caseKey.key = '';
  wizard.caseKey.again = '';
}

/** The problem with the case name, or null. */
export const draftTitleProblem = () => titleProblem(wizard.title);

/** Asks to move to a step. A forward move needs a valid name; a refused move keeps the step. Returns whether the wizard is on `to` now. */
export function goToStep(to: StepId): boolean {
  const move = moveToStep(wizard.step, to, { title: wizard.title, keyOk: draftKeyProblem() === null });
  if (!move.ok) return false;
  wizard.step = move.step;
  return true;
}

/** Next step; false when the name is not valid or this is the last step. */
export function nextStep(): boolean {
  const to = nextStepOf(wizard.step);
  return to !== wizard.step && goToStep(to);
}

/** Previous step; false on the first step. */
export function backStep(): boolean {
  const to = backStepOf(wizard.step);
  return to !== wizard.step && goToStep(to);
}

/** Replaces the chosen sources (the sources step owns the list; the shell only counts it). */
export function setSources(list: readonly DraftSource[]) {
  wizard.sources = list.map((source) => ({ ...source }));
}

/** Puts the wizard back on a step it may show, for a draft that is picked up again. */
export function resumeWizard() {
  wizard.step = resumeStep(wizard.step, { title: wizard.title, keyOk: draftKeyProblem() === null });
}

/** A plain copy of what will be created, safe to hand to the submit step. Title and environments are cleaned and bounded. */
export interface DraftSnapshot { title: string; envs: string[]; sources: DraftSource[] }

export function draftSnapshot(): DraftSnapshot {
  return { title: cleanTitle(wizard.title), envs: draftEnvs().names, sources: wizard.sources.map((source) => ({ ...source })) };
}
