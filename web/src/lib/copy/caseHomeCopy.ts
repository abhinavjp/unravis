import type { LintEntry } from './copylint';

/**
 * Words for the case home page: one page per case that holds its story, its theories, its health checks, the analysis and its files.
 * The story lists only what the case has saved, in the order it happened.
 */
export const CASE_HOME_TEXT = {
  heading: 'Your case',
  intro: 'Everything about this case is here. See what happened, your theories, the health checks, the analysis and the files.',
  back: 'All cases',
  backLabel: 'All cases: back to the start screen',
  navButton: 'Case home',
  navLabel: 'Back to the home page of this case',
  loading: 'Opening the case…',
  gone: 'This case could not be found. It may have been erased.',
  failed: 'The case could not be opened. Try again.',
  retry: 'Try again',
  created: 'Started',
  filesCount: 'Files',
  theoriesCount: 'Theories',
  checksCount: 'Health checks',
  untrusted: 'This case came from another place, so it is read-only here.',
  closedNote: 'This case is closed. You can read it, but nothing can be added.',

  analysisHeading: 'Analysis',
  analysisText: 'Open the charts and the ranked list of operations for this case.',
  analysisButton: 'Open the analysis',
  analysisHint: 'Open Nearby activity from the slowest list. It shows one call beside what SQL, Mongo, Redis and IIS did.',

  storyHeading: 'Story so far',
  storyIntro: 'One list, oldest first. It shows only what this case has saved.',
  storyNone: 'Nothing has happened in this case yet. Add a file or run a health check and it will show here.',
  storyGap: 'Analysis runs and connection tests are not saved in the case yet, so they are not in this list. Health checks stand in for them.',

  theoriesHeading: 'Theories',
  theoriesIntro: 'What you think may have happened. Each theory stays in this case and is tested against the evidence.',
  theoriesNone: 'No theories in this case yet. Write the first one when you have a guess to test.',
  theoriesOpen: 'Open the theories',
  theoriesAdd: 'Add a theory',
  theoriesFailed: 'The theories could not be read. Try again.',
  copyHeading: 'Earlier theories found',
  copyText: 'Theories written before cases kept their own are still saved on this computer. You can copy them into this case once.',
  copyButton: 'Copy them into this case',
  copying: 'Copying…',
  copyDone: 'Copied. They are now part of this case.',
  copyFailed: 'The earlier theories could not be copied. Try again.',

  healthHeading: 'Health checks',
  healthText: 'Read-only looks at the SQL Server or MongoDB your logs depend on.',

  filesHeading: 'Files',
  filesNone: 'No files have been added to this case yet.',
  filesAdd: 'Add a file',
  filesRows: 'rows',
  filesSkipped: 'skipped',

  kindCase: 'Case',
  kindFile: 'File',
  kindHealth: 'Health check',
  kindTheory: 'Theory',

  eventCase: 'Case started',
  eventFile: 'File added',
  eventFileKept: 'File read, not kept',
  eventHealthSql: 'Health check on SQL Server',
  eventHealthMongo: 'Health check on MongoDB',
  eventTheoryAdded: 'Theory added',
  eventTheoryChanged: 'Theory updated'
} as const;

/** "1,204 rows from IIS log, 3 skipped." */
export function fileDetail(fileName: string, rows: number, source: string, skipped: number): string {
  const head = `${fileName || 'A file'}: ${rows} ${rows === 1 ? 'row' : 'rows'}${source ? ' from ' + source : ''}`;
  return skipped > 0 ? `${head}, ${skipped} skipped.` : head + '.';
}

export function theoryChangedDetail(stateText: string): string {
  return `Now marked: ${stateText}.`;
}

export const caseHomeCopyEntries = (): LintEntry[] => [
  ...Object.entries(CASE_HOME_TEXT).map(([id, text]) => ({ id: 'caseHome.' + id, text })),
  { id: 'caseHome.sample.file', text: fileDetail('web-2026-10-24.log', 1204, 'IIS log', 3) },
  { id: 'caseHome.sample.theory', text: theoryChangedDetail('Supported by correlation') }
];
