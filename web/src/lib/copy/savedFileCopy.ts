// The words for an unreadable saved MongoDB connections file (RV-DAT-1, W-13). Plain words for a first-time investigator:
// what happened, what the one button does, and that nothing is deleted.
export const SAVED_FILE_TEXT = {
  damaged: 'The saved connections file is damaged and cannot be read. You can set it aside and start a new list. The old file is kept.',
  button: 'Set aside and start a new list',
  working: 'Setting it aside…',
  /** The set-aside worked: the old file is renamed, never deleted. */
  kept: (name: string) => `The damaged file was set aside and kept. It is saved as ${name} in the Unravis data folder. Unravis does not read it again.`,
  /** The file turned out to read fine (another window fixed it, or it was only busy): nothing was touched. */
  readsFine: 'The saved connections file reads fine now, so nothing was set aside.',
  failed: 'The damaged file could not be set aside. It was left as it was. Try again.'
} as const;

/** What the page says after a set-aside. An empty name means the file read fine and nothing moved. */
export const keptNote = (name: string): string => (name === '' ? SAVED_FILE_TEXT.readsFine : SAVED_FILE_TEXT.kept(name));
