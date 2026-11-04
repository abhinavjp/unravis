import type { LintEntry } from './copylint';
import type { TzCounts, TzState, TzView } from '../sourcesList';

/**
 * The words of the Sources list (packet P11) and of the time-zone notice on the Investigation Timeline. Pure.
 * Plain words: say what the state is and what it means for the timeline. A file's time-zone rule is fixed when it is read, so the way out of
 * "needs a check" is to add the file again with the right computer and time zone.
 */
export const SOURCES_LIST_TEXT = {
  tab: 'Sources',
  heading: 'Sources in this case',
  intro: 'Each source with its environment, portal and API, and how its times are read.',
  loading: 'Reading the sources…',
  loadFailed: 'The sources could not be read. Try again.',
  retry: 'Try again',
  empty: 'This case has no sources yet. Use Add data to add one.',
  noEnv: 'no environment yet',
  filterLabel: 'Show only one environment',
  filterAll: 'Show all',
  filterNone: 'No source matches this environment.',
  countsLabel: 'Time zones',
  moreLabel: 'Details',
  lessLabel: 'Hide details',
  computer: 'Computer',
  portal: 'Portal',
  api: 'API',
  environment: 'Environment',
  notNamed: 'not named yet',
  fixHelp: 'Its time zone was fixed when the file was read. To change it, add the file again with the right computer and time zone.',
  addFiles: 'Add files again',
  refresh: 'Read the list again',
  editLabels: 'Edit labels',
  save: 'Save',
  saving: 'Saving…',
  cancel: 'Cancel',
  labelsSaved: 'Saved.',
  labelsFailed: 'The labels could not be saved. Try again.',
  labelsBad: 'A label was too long, or looked like a password.',
  labelsClosed: 'This case is closed, so its labels cannot be changed.',
  labelsLocked: 'The master key is locked. Unlock it, then save again.',
  labelsGone: 'This source is no longer in the case. Read the list again.',
  zoneLabel: 'Time zone of that computer',
  zoneSave: 'Save time zone',
  zoneSaved: 'Saved.',
  zoneFailed: 'The time zone could not be saved. Try again.',
  zoneLocked: 'The master key is locked. Unlock it, then save again.',
  sizeLabel: 'Size'
} as const;

/** Said under the computer's time zone box: the zone belongs to the computer, so it changes for every file from it. */
export const zoneSharedText = (host: string): string => 'This is the time zone of ' + host + ', so it applies to every file from it.';

export const TZ_STATE_LABEL: Readonly<Record<TzState, string>> = {
  known: 'Time zone known',
  'assumed-utc': 'Assumed UTC',
  'needs-check': 'Needs a time-zone check'
};

/** One sentence for a state: what it is, and what the timeline does with the file. */
export function tzWhyText(view: TzView): string {
  switch (view.why) {
    case 'utc': return 'Its times are UTC, as this type writes them.';
    case 'offset': return 'Each time in the file carries its own offset.';
    case 'header': return 'The file states its own offset from UTC.';
    case 'host': return view.zone === '' ? 'Its times are in the clock of ' + view.host + '.' : 'Its times are in the clock of ' + view.host + ', time zone ' + view.zone + '.';
    case 'assumed': return 'Its times were taken as UTC. This type does not say, so check that the computer’s clock was set to UTC.';
    case 'no-rule': return 'No time zone was set for this file, so the timeline leaves it out.';
    case 'no-host': return 'It writes local time and no computer was named, so the timeline leaves it out.';
    case 'host-gone': return view.host + ' is no longer in your list of computers. Check its time zone.';
    default: return view.host + ' has no time zone saved. Check it.';
  }
}

export const countsText = (counts: TzCounts): string => {
  const parts: string[] = [];
  if (counts.known > 0) parts.push(counts.known + ' known');
  if (counts.assumed > 0) parts.push(counts.assumed + ' assumed UTC');
  if (counts.needs > 0) parts.push(counts.needs + ' to check');
  return parts.join(', ');
};

/** The notice above the Investigation Timeline chart when a lane is left empty for a time zone that was not checked. */
export const TZ_NOTICE_TEXT = {
  title: 'Time zone needs checking',
  lead: (labels: readonly string[]): string => 'A file for the ' + labels.join(' and ') + (labels.length === 1 ? ' lane' : ' lanes') + ' has no usable time zone, so ' + (labels.length === 1 ? 'that lane draws' : 'those lanes draw') + ' nothing.',
  action: 'See the sources',
  addAgain: 'Add files again'
} as const;

export const sourcesListCopyEntries = (): LintEntry[] => [
  ...Object.entries(SOURCES_LIST_TEXT).map(([id, text]) => ({ id: 'srclist.' + id, text })),
  ...Object.entries(TZ_STATE_LABEL).map(([id, text]) => ({ id: 'srclist.state.' + id, text })),
  ...(['utc', 'offset', 'header', 'assumed', 'no-rule', 'no-host'] as const).map((why) => ({ id: 'srclist.why.' + why, text: tzWhyText({ state: 'known', why, zone: '', host: '' }) })),
  { id: 'srclist.why.host', text: tzWhyText({ state: 'known', why: 'host', zone: 'Asia/Dubai', host: 'WEB01' }) },
  { id: 'srclist.why.gone', text: tzWhyText({ state: 'needs-check', why: 'host-gone', zone: '', host: 'WEB01' }) },
  { id: 'srclist.why.nozone', text: tzWhyText({ state: 'needs-check', why: 'no-zone', zone: '', host: 'WEB01' }) },
  { id: 'srclist.zone.shared', text: zoneSharedText('WEB01') },
  { id: 'srclist.counts', text: countsText({ known: 2, assumed: 1, needs: 1 }) },
  { id: 'srclist.notice.title', text: TZ_NOTICE_TEXT.title },
  { id: 'srclist.notice.lead', text: TZ_NOTICE_TEXT.lead(['SQL', 'IIS']) },
  { id: 'srclist.notice.action', text: TZ_NOTICE_TEXT.action },
  { id: 'srclist.notice.add', text: TZ_NOTICE_TEXT.addAgain }
];
