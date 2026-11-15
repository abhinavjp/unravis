// What the Timeline says about reading the database for its window (P5). Plain words; no state is hidden behind a colour alone.
export const SQL_AUTO_TEXT = {
  label: 'Database for this window',
  reading: (source: string) => `Reading ${source} for this window…`,
  readingNote: 'Read only. A long window is read in 3-day slices, so a month can take several minutes.',
  failed: (reason: string) => `The database could not be read: ${reason}`,
  retry: 'Try again',
  overlap: 'A day already kept for this database overlaps these times without being the same, so the database was not read. Use the same start and end, or pick times that do not overlap.',
  suggest: (source: string) => `The saved connection ${source} has not been read for this window. Read it now?`,
  openSources: 'Open Sources',
  suggestAction: 'Read the database',
  kept: (source: string) => `From the database ${source}, kept in the case.`,
  toggle: 'Read the database automatically',
  toggleHelp: 'Reads the saved connection for a new window, read only. Nothing is changed on the server.',
  password: 'This connection needs its password typed, so it is not read automatically. Open Sources to read it.',
  saveFailed: (reason: string) => `The read worked, but could not be kept in the case: ${reason}`
} as const;
