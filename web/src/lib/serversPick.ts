import { MAX_MACHINES, machineChoice, type MachineView } from './servers';
import { machineMemory, type Memory, type MachineMemory } from './serversMemory';

/**
 * The machine list of step 3 for a master with many machines: the search words, the filter, the order and the bulk choices. No DOM.
 */

export type PickFilter = 'all' | 'online' | 'before' | 'blackbox';
export const PICK_FILTERS: PickFilter[] = ['all', 'online', 'before', 'blackbox'];

export interface PickRow { machine: MachineView; memory: MachineMemory; eligible: boolean }

/** Only a machine the master accepted and knows to be online may be ticked. */
export const isEligible = (m: Pick<MachineView, 'state' | 'online'>): boolean => m.state === 'accepted' && m.online === true && machineChoice(m).selectable;

const connectedAt = (m: MachineMemory): number => (m.connected ? Date.parse(m.connected) : -Infinity);

/** Every whitespace-separated word must appear in the id, in any case. */
export function matchesQuery(id: string, query: string): boolean {
  const words = query.toLowerCase().split(/\s+/).filter(Boolean);
  const hay = id.toLowerCase();
  return words.every((w) => hay.includes(w));
}

function passes(row: PickRow, filter: PickFilter): boolean {
  switch (filter) {
    case 'online': return row.machine.online === true;
    case 'before': return !!row.memory.connected;
    case 'blackbox': return row.memory.blackbox === true;
    default: return true;
  }
}

/** The rows to show: filtered and searched, machines used before first (most recent first), the rest in the master's order. */
export function pickRows(machines: readonly MachineView[], memory: Memory, server: string, query: string, filter: PickFilter): PickRow[] {
  const rows: PickRow[] = machines.map((machine) => ({ machine, memory: machineMemory(memory, server, machine.id), eligible: isEligible(machine) }));
  const shown = rows.filter((r) => matchesQuery(r.machine.id, query) && passes(r, filter));
  return shown
    .map((row, index) => ({ row, index }))
    .sort((a, b) => (connectedAt(b.row.memory) - connectedAt(a.row.memory)) || (a.index - b.index))
    .map((x) => x.row);
}

/** How many machines each filter would show, with no search words. */
export function filterCounts(machines: readonly MachineView[], memory: Memory, server: string): Record<PickFilter, number> {
  const out: Record<PickFilter, number> = { all: 0, online: 0, before: 0, blackbox: 0 };
  for (const machine of machines) {
    const row: PickRow = { machine, memory: machineMemory(memory, server, machine.id), eligible: isEligible(machine) };
    for (const f of PICK_FILTERS) if (passes(row, f)) out[f]++;
  }
  return out;
}

/** Add every shown machine that may be ticked, up to the limit. Machines already ticked stay ticked. */
export function selectShown(picks: readonly string[], shown: readonly PickRow[]): { picks: string[]; leftOut: number } {
  const next = [...picks];
  let leftOut = 0;
  for (const row of shown) {
    if (!row.eligible || next.includes(row.machine.id)) continue;
    if (next.length >= MAX_MACHINES) { leftOut++; continue; }
    next.push(row.machine.id);
  }
  return { picks: next, leftOut };
}

/** Untick only the shown machines, so a search can be used to remove a group. */
export const clearShown = (picks: readonly string[], shown: readonly PickRow[]): string[] => {
  const ids = new Set(shown.map((r) => r.machine.id));
  return picks.filter((p) => !ids.has(p));
};

/** `just now`, `5 minutes ago`, `3 hours ago`, `2 days ago`. An older or unreadable time gives its date or an empty string. */
export function relativeAgo(iso: string | undefined, now: number): string {
  const t = iso ? Date.parse(iso) : NaN;
  if (!Number.isFinite(t)) return '';
  const minutes = Math.floor((now - t) / 60_000);
  if (minutes < 1) return 'just now';
  if (minutes < 60) return minutes + (minutes === 1 ? ' minute ago' : ' minutes ago');
  const hours = Math.floor(minutes / 60);
  if (hours < 48) return hours + (hours === 1 ? ' hour ago' : ' hours ago');
  const days = Math.floor(hours / 24);
  if (days < 60) return days + ' days ago';
  return new Date(t).toISOString().slice(0, 10);
}

/** One line for the history column: when it last answered, and when files were last copied from it. Empty when nothing is known. */
export function historyText(m: MachineMemory, now: number): string {
  const parts: string[] = [];
  const seen = relativeAgo(m.connected, now);
  if (seen) parts.push('Connected ' + seen);
  const pulled = relativeAgo(m.pulled, now);
  if (pulled) parts.push('last pulled ' + pulled);
  return parts.join(', ');
}
