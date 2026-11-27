// The words for an unreadable saved MongoDB connections file (RV-DAT-1, W-13). Plain words for a first-time investigator:
// what happened, what the one button does, what it moves, and that nothing is deleted.
export const SAVED_FILE_TEXT = {
  /** Used when the server's own sentence (which names the real folder) did not arrive. */
  damaged: 'The saved connections file cannot be read on this account or by this version of Unravis. You can set it aside and start a new list. The old file is kept, not deleted, and Unravis will not read it again.',
  button: 'Set aside and start a new list',
  /** The question before anything moves: the whole list goes out of use, passwords included. */
  ask: 'Move the whole saved list, and the passwords saved with it, out of use? The old file is kept in the same folder and is not deleted, but Unravis will not read it again. You start with an empty list.',
  /** Added to the question when the list is the whole PC's (the key keeper's). */
  askShared: ' This list is shared, so the change is for everyone who uses Unravis on this PC.',
  yes: 'Yes, set it aside',
  keep: 'No, leave it as it is',
  working: 'Setting it aside…',
  /** The set-aside worked: the old file is renamed, never deleted. */
  kept: (name: string, folder: string) => `The old saved connections file was set aside and kept. It is saved as ${name} in ${folder === '' ? 'the same folder as the saved connections file' : folder}. Unravis does not read it again.`,
  /** The file turned out to read fine (another window fixed it, or it was only busy): nothing was touched. */
  readsFine: 'The saved connections file reads fine now, so nothing was set aside.',
  failed: 'The saved connections file could not be set aside. It was left as it was.'
} as const;

/** The question shown before the move; shared is true for the key keeper's list. */
export const askText = (shared: boolean): string => SAVED_FILE_TEXT.ask + (shared ? SAVED_FILE_TEXT.askShared : '');

/** What the page says after a set-aside. An empty name means the file read fine and nothing moved. */
export const keptNote = (name: string, folder = ''): string => (name === '' ? SAVED_FILE_TEXT.readsFine : SAVED_FILE_TEXT.kept(name, folder));
