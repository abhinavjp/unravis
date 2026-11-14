/**
 * What the full-page Slow call screen opens on: the clicked timeline slot, encoded for and parsed back from session storage. Pure TypeScript.
 */
import type { TracingLane, TracingSourceRef, TracingWindow } from '../tracing/model';
import { drillSourceIds, slotWindow } from './target';

/** The shape of a case id. */
const CASE_ID_PATTERN = /^[a-z2-7]{26}$/;
/** The longest stored text that is parsed at all. */
const MAX_TEXT_LENGTH = 8000;
/** The most source ids a target carries (the server's MaxSources). */
const MAX_SOURCE_IDS = 8;
/** The longest source id or call id accepted. */
const MAX_ID_LENGTH = 200;

/** What the screen opens on: one slot of the timeline, the sources to search and, once found, the call itself. */
export interface SlowCallTarget {
  /** The case id, 26 characters of a-z and 2-7. */
  caseId: string;
  /** Absolute grid index of the clicked diamond, an integer of 0 or more. */
  slot: number;
  /** Slot start, ISO. */
  start: string;
  /** Slot end, ISO. */
  end: string;
  /** Audit source ids to search, 0 to 8 of them. */
  sourceIds: string[];
  /** Set once the call is found, so a reload skips the search. */
  call?: { sourceId: string; id: string };
}

/** The target for one clicked slot. Null when the lane is missing, the slot has no usable window or the case id is malformed. */
export function makeCallTarget(
  caseId: string,
  window: TracingWindow,
  slot: number,
  lane: TracingLane | undefined,
  sources: readonly TracingSourceRef[]
): SlowCallTarget | null {
  if (!lane || !CASE_ID_PATTERN.test(caseId)) return null;
  const bounds = slotWindow(window, slot);
  if (!bounds) return null;
  return { caseId, slot, start: bounds.start, end: bounds.end, sourceIds: drillSourceIds(lane, sources) };
}

/** A copy of the target that also names the call found. The input is left as it was. */
export function withCall(target: SlowCallTarget, call: { sourceId: string; id: string }): SlowCallTarget {
  return { ...target, sourceIds: [...target.sourceIds], call: { sourceId: call.sourceId, id: call.id } };
}

/** The target as JSON text for session storage. */
export function encodeCallTarget(target: SlowCallTarget): string {
  return JSON.stringify(target);
}

/** True for a non-empty string of at most 200 characters. */
function isId(value: unknown): value is string {
  return typeof value === 'string' && value.length > 0 && value.length <= MAX_ID_LENGTH;
}

/** The target read back from stored text. Null (never a throw) when anything is missing or malformed; unknown keys are dropped. */
export function parseCallTarget(text: string | null): SlowCallTarget | null {
  if (text === null || typeof text !== 'string' || text.length > MAX_TEXT_LENGTH) return null;
  let raw: unknown;
  try {
    raw = JSON.parse(text);
  } catch {
    return null;
  }
  if (typeof raw !== 'object' || raw === null || Array.isArray(raw)) return null;
  const obj = raw as Record<string, unknown>;
  const { caseId, slot, start, end, sourceIds, call } = obj;
  if (typeof caseId !== 'string' || !CASE_ID_PATTERN.test(caseId)) return null;
  if (typeof slot !== 'number' || !Number.isInteger(slot) || slot < 0) return null;
  if (typeof start !== 'string' || typeof end !== 'string') return null;
  const startMs = Date.parse(start);
  const endMs = Date.parse(end);
  if (Number.isNaN(startMs) || Number.isNaN(endMs) || endMs <= startMs) return null;
  if (!Array.isArray(sourceIds) || sourceIds.length > MAX_SOURCE_IDS || !sourceIds.every(isId)) return null;
  const target: SlowCallTarget = { caseId, slot, start, end, sourceIds: [...(sourceIds as string[])] };
  if (call !== undefined) {
    if (typeof call !== 'object' || call === null || Array.isArray(call)) return null;
    const { sourceId, id } = call as Record<string, unknown>;
    if (!isId(sourceId) || !isId(id)) return null;
    target.call = { sourceId, id };
  }
  return target;
}
