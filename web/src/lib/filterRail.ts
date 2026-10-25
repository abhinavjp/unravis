import type { PanelGroup } from './depth';

// The group rail of the More filters panel: five groups, one pane visible at a time. These helpers are pure so the keyboard
// and selection rules can be tested without mounting the component.
export type RailGroup = 'when' | PanelGroup;

export const RAIL_GROUPS: readonly { id: RailGroup; label: string }[] = [
  { id: 'when', label: 'When' },
  { id: 'what', label: 'What' },
  { id: 'details', label: 'Request details' },
  { id: 'ranges', label: 'Ranges' },
  { id: 'chart', label: 'Chart' }
];

export const RAIL_TEXT = {
  railLabel: 'Filter groups',
  problem: 'has a problem',
  inUse: 'in use',
  problemsLead: 'Needs attention:'
} as const;

// The group shown when the panel opens: the first one (in rail order) that holds an active filter, else When.
export function firstActiveGroup(counts: Readonly<Record<PanelGroup, number>>): RailGroup {
  return RAIL_GROUPS.find(group => group.id !== 'when' && counts[group.id as PanelGroup] > 0)?.id ?? 'when';
}

// Where a key moves the selection. Up and Left go back, Down and Right go forward (both wrap), Home and End jump.
// Any other key returns null so it keeps its normal behaviour (Esc must still close the panel).
export function railTarget(key: string, index: number, total: number): number | null {
  if (total < 1) return null;
  if (key === 'ArrowDown' || key === 'ArrowRight') return (index + 1) % total;
  if (key === 'ArrowUp' || key === 'ArrowLeft') return (index - 1 + total) % total;
  if (key === 'Home') return 0;
  if (key === 'End') return total - 1;
  return null;
}
