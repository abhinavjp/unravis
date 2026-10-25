/**
 * Pure helpers for the "Compare theories" matrix in the Ledger drawer. No DOM, no network.
 * The rows show only what the analyst saved. Nothing here scores, ranks or picks a theory.
 */
import { presenceText, stateLabel } from './ledger';
import { theoriesCopy } from './copy/theoriesCopy';
import type { Hypothesis } from './types';

/** The longest the first counterexample is shown in a cell before it is cut with an ellipsis. */
export const FIRST_COUNTER_MAX = 90;

export interface TheoryRow {
  id: string;
  text: string;
  /** The analyst's state label, shown beside the theory. */
  state: string;
  /** "3 of 4 episodes", or "Not stated" when no episodes were counted. */
  supports: string;
  /** "None listed", or "2 listed". */
  cannotExplain: string;
  /** The first counterexample, cut to a readable length. Empty when none is listed. */
  firstCounter: string;
  /** The settling observation, or an empty string when none is written. */
  settle: string;
}

/** Cuts text at a word edge when it is longer than max characters. */
export function clip(text: string, max: number = FIRST_COUNTER_MAX): string {
  const t = text.replace(/\s+/g, ' ').trim();
  if (t.length <= max) return t;
  const cut = t.slice(0, max - 1);
  const space = cut.lastIndexOf(' ');
  return (space > max * 0.6 ? cut.slice(0, space) : cut).trimEnd() + '…';
}

/** One row per saved theory, in ledger order. Theories with no text are left out. */
export function theoryRows(items: Hypothesis[]): TheoryRow[] {
  return items.filter((h) => (h.text ?? '').trim() !== '').map((h) => {
    const facts = h.facts;
    const counters = (facts?.counterexamples ?? []).map((c) => c.trim()).filter(Boolean);
    return {
      id: h.id,
      text: h.text,
      state: stateLabel(h.state),
      supports: presenceText(facts?.presentK ?? 0, facts?.totalN ?? 0) || theoriesCopy.supportsNotStated,
      cannotExplain: counters.length ? theoriesCopy.listed(counters.length) : theoriesCopy.noneListed,
      firstCounter: counters.length ? clip(counters[0]) : '',
      settle: (h.settlingObservation ?? '').trim()
    };
  });
}
