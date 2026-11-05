import type { LintEntry } from './copylint';
import type { HostProblem, LabelProblem, ZoneProblem } from '../zoneChoice';

/**
 * The words of the "Which computer wrote it?" choice (packet P4), used in the file list, on the review step and in the result table.
 * Pure. Plain words; say what the choice is for and what happens if it is left alone. "Computer" is the word for a host.
 */
export const ZONE_CHOICE_TEXT = {
  label: 'Which computer wrote it?',
  labelOptional: 'Which computer wrote it? (not required)',
  pick: 'Pick a computer',
  optionalNone: 'Let the file say',
  add: 'Add a computer…',
  loading: 'Reading the list of computers…',
  loadFailed: 'The list of computers could not be read. Try again, or add a computer.',
  retry: 'Read the list again',
  needHost: 'This type does not say which computer wrote it. Pick one so its marks sit on that computer’s lane.',
  needZone: 'This type writes that computer’s own clock. Pick the computer and give its time zone, so the times line up with other sources.',
  optionalHelp: 'Pick the computer if the server named in the file is not in your list, or if the file names none.',
  // For logs that name their own computer (IIS and log4net): the name is read from the file, so a pick only stands in for a name the list does not hold.
  optionalHelpNamed: 'The computer is read from the file. Pick one only if the name in the file is not in your list.',
  utcNote: 'Its times are UTC. The computer only labels them.',
  addHeading: 'Add a computer',
  nameLabel: 'Name of the computer',
  nameHelp: 'Letters, digits, dot, dash or underscore. For example WEB01.',
  zoneLabel: 'Time zone of its clock',
  zoneLabelOptional: 'Time zone of its clock (not needed for this type)',
  zoneIs: 'Its clock is set to',
  zoneHelp: 'A name such as Asia/Dubai, Europe/London or UTC.',
  save: 'Save this computer',
  saveZone: 'Save the time zone',
  saving: 'Saving…',
  cancel: 'Cancel',
  saved: 'Saved to your list of computers.',
  noZone: 'This computer has no time zone saved yet. Give one below.',
  gone: 'This computer is no longer in the list. Pick another.'
} as const;

export type NeedKind = 'host' | 'host-tz' | 'optional' | 'none';

export const hostProblemText = (problem: HostProblem, need: NeedKind): string => {
  switch (problem) {
    case 'pick': return need === 'host-tz' ? ZONE_CHOICE_TEXT.needZone : ZONE_CHOICE_TEXT.needHost;
    case 'no-zone': return ZONE_CHOICE_TEXT.noZone;
    case 'gone': return ZONE_CHOICE_TEXT.gone;
    default: return '';
  }
};

export const labelProblemText = (problem: LabelProblem): string => {
  switch (problem) {
    case 'empty': return 'Give the computer a name.';
    case 'long': return 'That name is too long.';
    case 'chars': return 'Use letters, digits, dot, dash or underscore, and start with a letter or digit.';
    case 'taken': return 'A computer with that name is already in the list. Pick it from the list instead.';
    default: return '';
  }
};

export const zoneProblemText = (problem: ZoneProblem): string => {
  switch (problem) {
    case 'empty': return 'Give the time zone of its clock.';
    case 'unknown': return 'That is not a time zone name. Try a name such as Asia/Dubai or UTC.';
    default: return '';
  }
};

/** Why the list could not be saved, in words. */
export const saveFailText = (reason: string): string => {
  switch (reason) {
    case 'locked': return 'The master key is locked. Unlock it, then save again.';
    case 'taken': return labelProblemText('taken');
    default: return 'The computer could not be saved. Try again.';
  }
};

/** One option of the list: the name, and its time zone when it has one. */
export const hostOptionText = (entry: { label: string; tz: string }): string => (entry.tz.trim() === '' ? entry.label : entry.label + ' (' + entry.tz.trim() + ')');

export const hostPickLabel = (name: string): string => 'Which computer wrote ' + name;

/** The computer and zone of a file, as one line for the review step and the result table. */
export const hostSummaryText = (label: string, tz: string): string => (label === '' ? '' : tz.trim() === '' ? 'Computer: ' + label : 'Computer: ' + label + ', time zone ' + tz.trim());

/** The help line for a pick that is offered, not needed. IIS logs name their own computer, so a pick only stands in when the name in the
 * file is not in the list; a sql-xe export may name no computer, so there the pick also gives the file its name. */
export const optionalHelpText = (kind: string): string => (kind === 'sql-xe' ? ZONE_CHOICE_TEXT.optionalHelp : ZONE_CHOICE_TEXT.optionalHelpNamed);

export const zoneChoiceCopyEntries = (): LintEntry[] => [
  ...Object.entries(ZONE_CHOICE_TEXT).map(([id, text]) => ({ id: 'zone.' + id, text })),
  { id: 'zone.sample.problem.host', text: hostProblemText('pick', 'host') },
  { id: 'zone.sample.problem.zone', text: hostProblemText('pick', 'host-tz') },
  ...(['empty', 'long', 'chars', 'taken'] as const).map((id) => ({ id: 'zone.label.' + id, text: labelProblemText(id) })),
  ...(['empty', 'unknown'] as const).map((id) => ({ id: 'zone.zonename.' + id, text: zoneProblemText(id) })),
  ...(['locked', 'taken', 'failed'] as const).map((id) => ({ id: 'zone.save.' + id, text: saveFailText(id) })),
  { id: 'zone.sample.option', text: hostOptionText({ label: 'WEB01', tz: 'Asia/Dubai' }) },
  { id: 'zone.sample.pick', text: hostPickLabel('httperr1.log') },
  { id: 'zone.sample.summary', text: hostSummaryText('WEB01', 'Asia/Dubai') },
  { id: 'zone.sample.optional.named', text: optionalHelpText('iis-w3c') },
  { id: 'zone.sample.optional.xe', text: optionalHelpText('sql-xe') }
];
