/**
 * The words of the settling observation box and the "Compare theories" matrix in the Ledger drawer. Plain and factual:
 * they say what the analyst wrote and what the data holds, never why anything happened. Every string here passes the copy
 * lint (tests/theories.test.cjs).
 */
export const theoriesCopy = {
  settlingLabel: 'One observation that would settle this',
  settlingHint: 'Write one thing you could look for, in either direction, that would settle this theory. Up to 500 characters.',
  settlingPlaceholder: 'For example: the stall is gone on the next quiet night.',
  settlingCount: (used: number, max: number) => used + ' of ' + max + ' characters',
  compareToggle: 'Compare theories',
  compareIntro: 'Your theories side by side. These are your own words and numbers. The app adds nothing.',
  compareEmpty: 'Add a theory to compare it here.',
  tableLabel: 'Theories compared',
  columnTheory: 'Theory',
  columnSupports: 'What the data supports',
  columnCannotExplain: 'What it cannot explain',
  columnSettle: 'What would settle it',
  supportsNotStated: 'Not stated',
  noneListed: 'None listed',
  notWritten: 'not written yet',
  firstPrefix: 'First: ',
  listed: (count: number) => count + ' listed'
} as const;

/** Every fixed string the matrix and the settling box show, with sample outputs of the template ones, for the copy lint. */
export function allTheoriesStrings(): { id: string; text: string }[] {
  const entries: { id: string; text: string }[] = [];
  for (const [key, value] of Object.entries(theoriesCopy)) {
    entries.push({ id: 'theories.' + key, text: typeof value === 'function' ? '' : value });
  }
  entries.push({ id: 'theories.settlingCount', text: theoriesCopy.settlingCount(120, 500) });
  entries.push({ id: 'theories.listed.one', text: theoriesCopy.listed(1) });
  entries.push({ id: 'theories.listed.many', text: theoriesCopy.listed(3) });
  return entries.filter((e) => e.text !== '');
}
