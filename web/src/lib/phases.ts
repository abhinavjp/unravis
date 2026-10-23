import type { PhaseMeans } from './types';
const finite = (value: number | null | undefined): value is number => value != null && Number.isFinite(value) && value >= 0;
/** Bar segments for the two phases that make up latency; widths are shares of audit + api, null when neither is known. */
export const phaseSegments = (phases: PhaseMeans | null | undefined) => {
  const audit = finite(phases?.auditMs) ? phases.auditMs : null, api = finite(phases?.apiMs) ? phases.apiMs : null;
  const total = (audit ?? 0) + (api ?? 0);
  if (total <= 0) return null;
  return { audit, api, total, auditPct: (audit ?? 0) / total * 100, apiPct: (api ?? 0) / total * 100 };
};
export const phaseMs = (value: number | null | undefined) => !finite(value) ? 'N/A' : value >= 1000 ? (value / 1000).toFixed(2) + ' s' : (value >= 100 ? value.toFixed(0) : value.toFixed(1)) + ' ms';
const signed = (value: number) => (value > 0 ? '+' : value < 0 ? '−' : '') + phaseMs(Math.abs(value));
/** Tooltip for a phase bar: what each part is and, when comparable, how each phase moved since the reference. */
export const phaseTip = (delta?: PhaseMeans | null) => {
  const parts = ['Mean time per request. Audit (writing the audit row) and API (processing) add up to the total latency; payload is written after the response is sent and is not part of latency.'];
  const changes = [['audit', delta?.auditMs], ['api', delta?.apiMs], ['payload', delta?.payloadMs]].filter((entry): entry is [string, number] => typeof entry[1] === 'number' && Number.isFinite(entry[1]));
  if (changes.length) parts.push('Change versus reference: ' + changes.map(([name, value]) => name + ' ' + signed(value)).join(', ') + '.');
  return parts.join(' ');
};
